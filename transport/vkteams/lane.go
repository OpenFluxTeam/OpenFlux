package vkteams

// The media lane: one real WebRTC PeerConnection between the two tunnel ends.
// It is the "normal" lane the user asked for - a genuine participant that joins
// the call, negotiates over the custom-data bus, and carries tunnel data as RTP
// media (the Telemost technique: data in the payload). Because the peer is our
// own pion, the payload need not be valid VP8 - SRTP hides it from the relay,
// and the only thing the TURN relay classifies on is the cleartext RTP header's
// first byte (0x80), which genuine SRTP media carries by nature. That is what
// lifts the lane out of the DTLS/DataChannel rate class (~10 KB/s) into the
// media class (~260 KB/s per allocation). Additional throughput comes from the
// cheap raw-allocation lanes (rawlane.go), not from more PeerConnections.

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"

	"github.com/p1neappleXpress/OpenFlux/transport"
	"github.com/p1neappleXpress/OpenFlux/transport/yandex"
	"github.com/p1neappleXpress/OpenFlux/utils"
)

// vp8Cap is the codec the media lane negotiates. The payload it carries is
// opaque tunnel data, so the codec only has to be one both ends register; VP8
// is what a browser offers first.
var vp8Cap = webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8, ClockRate: 90000}

const vp8PayloadType = 96

// newMediaAPI builds a minimal WebRTC API: VP8 only, no header extensions. The
// peer is our own pion (no SFU to satisfy), so one shared codec is all the
// negotiation needs.
func newMediaAPI() (*webrtc.API, error) {
	me := &webrtc.MediaEngine{}
	if err := me.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: vp8Cap,
		PayloadType:        vp8PayloadType,
	}, webrtc.RTPCodecTypeVideo); err != nil {
		return nil, err
	}
	return webrtc.NewAPI(webrtc.WithMediaEngine(me)), nil
}

// callCtx is the shared per-call state a group of lanes runs on: the HTTP join
// result (endpoint + ICE servers) and the one media-signaling bus. One per
// node; all lanes of a group share it.
type callCtx struct {
	code   string
	isExit bool
	info   *CallInfo
	bus    *signalBus
}

// joinCall runs the guest join and opens the signaling bus.
func joinCall(code string, isExit bool) (*callCtx, error) {
	info, err := GuestJoin(code)
	if err != nil {
		return nil, fmt.Errorf("join: %w", err)
	}
	bus, err := dialSignal(info.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("signal: %w", err)
	}
	utils.Infof("[vkteams] joined call (exit=%v): %d turn url(s), p2pForbidden=%v",
		isExit, len(info.TURNURLs), info.P2PForbidden)
	return &callCtx{code: code, isExit: isExit, info: info, bus: bus}, nil
}

func (c *callCtx) iceServers() []webrtc.ICEServer {
	var out []webrtc.ICEServer
	if len(c.info.STUNURLs) > 0 {
		out = append(out, webrtc.ICEServer{URLs: c.info.STUNURLs})
	}
	if len(c.info.TURNURLs) > 0 {
		out = append(out, webrtc.ICEServer{
			URLs:       c.info.TURNURLs,
			Username:   c.info.TURNUser,
			Credential: c.info.TURNCred,
		})
	}
	return out
}

// mediaLane is one media PeerConnection as a transport.Transport lane.
type mediaLane struct {
	*transport.BaseTransport
	cc         *callCtx
	forceRelay bool
	api        *webrtc.API

	grouped bool // true when managed by a Group (the group owns the bus handler)
	laneIdx int  // index within the group; included in signaling so the peer routes correctly

	mu          sync.Mutex
	pc          *webrtc.PeerConnection
	track       *yandex.TelemostTrack
	myPeerID    string
	peerID      string
	epoch       int64
	haveRemote  bool
	localAnswer string
	pending     []webrtc.ICECandidateInit
	seq         uint16
	ts          uint32
}

func newMediaLane(cc *callCtx, forceRelay bool, config ...transport.TransportConfig) (*mediaLane, error) {
	api, err := newMediaAPI()
	if err != nil {
		return nil, err
	}
	cfg := transport.DefaultConfig()
	if len(config) > 0 {
		cfg = config[0]
	}
	return &mediaLane{
		BaseTransport: transport.NewBaseTransport(cfg),
		cc:            cc,
		forceRelay:    forceRelay,
		api:           api,
		ts:            rand.Uint32(),
	}, nil
}

func (l *mediaLane) Start() error {
	_ = l.BaseTransport.Start()
	if !l.grouped {
		l.cc.bus.onCustomData(l.onCustom)
	}
	if !l.cc.isExit {
		go l.clientOfferLoop()
	}
	return nil
}

func (l *mediaLane) Stop() error {
	_ = l.BaseTransport.Stop()
	l.mu.Lock()
	pc := l.pc
	l.pc = nil
	l.mu.Unlock()
	if pc != nil {
		_ = pc.Close()
	}
	return nil
}

// buildPC creates the PeerConnection, adds our send track, and wires receive,
// ICE and state callbacks. Peer ids (used by the ICE callback's signalling)
// must be set before this is called.
func (l *mediaLane) buildPC() error {
	cfg := webrtc.Configuration{ICEServers: l.cc.iceServers()}
	if l.forceRelay {
		cfg.ICETransportPolicy = webrtc.ICETransportPolicyRelay
	}
	pc, err := l.api.NewPeerConnection(cfg)
	if err != nil {
		return err
	}
	track := yandex.NewTelemostTrack(vp8Cap, webrtc.RTPCodecTypeVideo, randomID(), randomID())
	if _, err := pc.AddTrack(track); err != nil {
		_ = pc.Close()
		return err
	}
	pc.OnTrack(func(remote *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		utils.Debugf("[vkteams] remote track %s codec=%s (exit=%v)", remote.ID(), remote.Codec().MimeType, l.cc.isExit)
		go l.readTrack(remote)
	})
	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		if l.forceRelay && c.Typ != webrtc.ICECandidateTypeRelay {
			return
		}
		raw, err := json.Marshal(c.ToJSON())
		if err != nil {
			return
		}
		l.sendSignal("ice-candidate", map[string]any{"candidate": string(raw)})
	})
	pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		utils.Debugf("[vkteams] pc state=%s (exit=%v)", s, l.cc.isExit)
		switch s {
		case webrtc.PeerConnectionStateConnected:
			l.SetConnected(true)
		case webrtc.PeerConnectionStateDisconnected,
			webrtc.PeerConnectionStateFailed,
			webrtc.PeerConnectionStateClosed:
			l.SetConnected(false)
		}
	})

	l.mu.Lock()
	l.pc = pc
	l.track = track
	l.mu.Unlock()
	return nil
}

// ---- client role ----

func (l *mediaLane) clientOfferLoop() {
	l.mu.Lock()
	l.myPeerID = "cli-" + randPeer()
	l.epoch = time.Now().UnixNano()
	l.mu.Unlock()

	if err := l.buildPC(); err != nil {
		utils.Infof("[vkteams] client buildPC: %v", err)
		return
	}
	offer, err := l.pc.CreateOffer(nil)
	if err != nil {
		utils.Infof("[vkteams] createOffer: %v", err)
		return
	}
	if err := l.pc.SetLocalDescription(offer); err != nil {
		utils.Infof("[vkteams] setLocalDescription(offer): %v", err)
		return
	}

	// Re-send until connected: the exit may not be on the bus yet, and a
	// reconnected exit needs the offer again.
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		l.sendSignal("sdp-offer", map[string]any{"sdp": offer.SDP})
		select {
		case <-l.Done():
			return
		case <-l.cc.bus.done():
			return
		case <-t.C:
			if l.IsConnected() {
				return
			}
		}
	}
}

func (l *mediaLane) handleAnswer(d map[string]any) {
	sdp, _ := d["sdp"].(string)
	if sdp == "" {
		return
	}
	l.mu.Lock()
	if l.pc == nil || l.haveRemote {
		l.mu.Unlock()
		return
	}
	l.haveRemote = true
	pc := l.pc
	l.mu.Unlock()

	if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: sdp}); err != nil {
		utils.Infof("[vkteams] setRemoteDescription(answer): %v", err)
		l.mu.Lock()
		l.haveRemote = false
		l.mu.Unlock()
		return
	}
	l.flushPending()
}

// ---- exit role ----

func (l *mediaLane) handleOffer(d map[string]any, epoch int64) {
	sdp, _ := d["sdp"].(string)
	clientID, _ := d["clientId"].(string)
	if sdp == "" || clientID == "" {
		return
	}

	l.mu.Lock()
	// A re-sent offer with the same epoch (the client re-sends every 2s): keep
	// the established connection and just re-send our answer.
	if l.pc != nil && epoch <= l.epoch {
		answer := l.localAnswer
		l.mu.Unlock()
		if answer != "" {
			l.sendSignal("sdp-answer", map[string]any{"sdp": answer})
		}
		return
	}
	// First offer, or a newer epoch (client reconnected): (re)build.
	old := l.pc
	l.pc = nil
	l.haveRemote = false
	l.localAnswer = ""
	l.pending = nil
	l.epoch = epoch
	l.peerID = clientID
	if l.myPeerID == "" {
		l.myPeerID = "exit-" + randPeer()
	}
	l.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}

	if err := l.buildPC(); err != nil {
		utils.Infof("[vkteams] exit buildPC: %v", err)
		return
	}
	pc := l.pc
	if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: sdp}); err != nil {
		utils.Infof("[vkteams] setRemoteDescription(offer): %v", err)
		return
	}
	l.mu.Lock()
	l.haveRemote = true
	l.mu.Unlock()
	l.flushPending()

	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		utils.Infof("[vkteams] createAnswer: %v", err)
		return
	}
	if err := pc.SetLocalDescription(answer); err != nil {
		utils.Infof("[vkteams] setLocalDescription(answer): %v", err)
		return
	}
	l.mu.Lock()
	l.localAnswer = answer.SDP
	l.mu.Unlock()
	l.sendSignal("sdp-answer", map[string]any{"sdp": answer.SDP})
}

// ---- shared signalling ----

func (l *mediaLane) onCustom(d map[string]any) {
	typ, _ := d["type"].(string)
	from, _ := d["from"].(string)
	var epoch int64
	if e, ok := d["epoch"].(float64); ok {
		epoch = int64(e)
	}

	if l.cc.isExit {
		if from != "client" {
			return
		}
		switch typ {
		case "sdp-offer":
			l.handleOffer(d, epoch)
		case "ice-candidate":
			if cid, _ := d["clientId"].(string); cid != "" {
				l.mu.Lock()
				mismatch := l.peerID != "" && l.peerID != cid
				l.mu.Unlock()
				if mismatch {
					return
				}
			}
			l.handleCandidate(d)
		}
		return
	}

	if from != "exit" {
		return
	}
	// Answers/candidates addressed to this client only.
	l.mu.Lock()
	mine := l.myPeerID
	l.mu.Unlock()
	if cid, _ := d["clientId"].(string); cid != "" && cid != mine {
		return
	}
	switch typ {
	case "sdp-answer":
		if eid, _ := d["exitId"].(string); eid != "" {
			l.mu.Lock()
			l.peerID = eid
			l.mu.Unlock()
		}
		l.handleAnswer(d)
	case "ice-candidate":
		l.handleCandidate(d)
	}
}

func (l *mediaLane) handleCandidate(d map[string]any) {
	cs, _ := d["candidate"].(string)
	if cs == "" {
		return
	}
	var init webrtc.ICECandidateInit
	if json.Unmarshal([]byte(cs), &init) != nil || init.Candidate == "" {
		return
	}
	if l.forceRelay && !strings.Contains(init.Candidate, "typ relay") {
		return
	}
	l.mu.Lock()
	if l.pc == nil || !l.haveRemote {
		l.pending = append(l.pending, init)
		l.mu.Unlock()
		return
	}
	pc := l.pc
	l.mu.Unlock()
	if err := pc.AddICECandidate(init); err != nil {
		utils.Debugf("[vkteams] addICECandidate: %v", err)
	}
}

func (l *mediaLane) flushPending() {
	l.mu.Lock()
	pc := l.pc
	pend := l.pending
	l.pending = nil
	l.mu.Unlock()
	if pc == nil {
		return
	}
	for _, c := range pend {
		_ = pc.AddICECandidate(c)
	}
}

// sendSignal relays one typed custom-data payload to the peer end.
func (l *mediaLane) sendSignal(typ string, extra map[string]any) {
	l.mu.Lock()
	payload := map[string]any{"type": typ, "epoch": l.epoch, "laneIdx": l.laneIdx}
	if l.cc.isExit {
		payload["from"] = "exit"
		payload["clientId"] = l.peerID
		payload["exitId"] = l.myPeerID
	} else {
		payload["from"] = "client"
		payload["clientId"] = l.myPeerID
	}
	l.mu.Unlock()
	for k, v := range extra {
		payload[k] = v
	}
	_ = l.cc.bus.sendCustom(payload)
}

// ---- data ----

func (l *mediaLane) readTrack(remote *webrtc.TrackRemote) {
	for {
		if !l.IsRunning() {
			return
		}
		pkt, _, err := remote.ReadRTP()
		if err != nil {
			utils.Debugf("[vkteams] track read: %v", err)
			return
		}
		if len(pkt.Payload) == 0 {
			continue
		}
		payload := make([]byte, len(pkt.Payload)) // ReadRTP reuses its buffer
		copy(payload, pkt.Payload)
		l.RecordReceive(len(payload))
		l.CallReceive(payload)
	}
}

func (l *mediaLane) Send(piece []byte) error {
	l.mu.Lock()
	track := l.track
	l.mu.Unlock()
	if track == nil {
		return fmt.Errorf("vkteams: media lane not ready")
	}
	ssrc := track.SSRC()
	if ssrc == 0 {
		return fmt.Errorf("vkteams: media track not bound")
	}
	pt := uint8(vp8PayloadType)
	if t := track.PayloadType(); t != 0 {
		pt = uint8(t)
	}

	l.mu.Lock()
	l.seq++
	seq := l.seq
	l.ts += 3000 // ~90kHz/30fps; value is irrelevant to the carried data
	ts := l.ts
	l.mu.Unlock()

	n, err := track.WriteHeader(rtp.Header{
		Version:        2,
		Marker:         true,
		PayloadType:    pt,
		SequenceNumber: seq,
		Timestamp:      ts,
		SSRC:           uint32(ssrc),
	}, piece)
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("vkteams: srtp not ready")
	}
	l.RecordSend(len(piece))
	return nil
}

func randPeer() string { return fmt.Sprintf("%09d", rand.Intn(1000000000)) }
