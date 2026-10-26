package vkteams

// The media-signaling bus. videowebrtc exposes a notification WebSocket (/ws2)
// that relays a participant's "custom-data" payloads to the other participants
// of the call. We ride it purely as a signaling channel between the two tunnel
// ends: SDP offer/answer and ICE candidates for the media lane, and the raw
// lanes' relayed addresses. Joining the call already happened over HTTP in
// GuestJoin; tgt=join in the URL attaches this socket to that membership, so no
// further join handshake is sent here.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/p1neappleXpress/OpenFlux/utils"
)

// wsSuffix is the query the web client appends to the bus endpoint. The
// endpoint already carries the call's token; this only declares the client.
const wsSuffix = "&platform=WEB&appVersion=1.1&version=6&device=browser&capabilities=253F&clientType=VK_TEAMS&tgt=join"

// signalBus is one media-bus WebSocket connection.
type signalBus struct {
	conn *websocket.Conn

	wmu sync.Mutex // one writer at a time (gorilla requirement)
	seq atomic.Uint64

	onCustom atomic.Pointer[func(map[string]any)]

	closeOnce sync.Once
	closed    chan struct{}
}

// dialSignal opens the media-bus WebSocket for a joined call.
func dialSignal(endpoint string) (*signalBus, error) {
	d := websocket.Dialer{
		HandshakeTimeout: 20 * time.Second,
		ReadBufferSize:   64 << 10, // SDP notifications are large
		WriteBufferSize:  64 << 10,
	}
	h := http.Header{}
	h.Set("Origin", "https://myteam.mail.ru")
	h.Set("User-Agent", userAgent)

	conn, resp, err := d.Dial(endpoint+wsSuffix, h)
	if err != nil {
		code := 0
		if resp != nil {
			code = resp.StatusCode
		}
		return nil, fmt.Errorf("ws dial: status %d: %w", code, err)
	}
	b := &signalBus{conn: conn, closed: make(chan struct{})}
	go b.readLoop()
	return b, nil
}

// onCustomData sets the handler for inbound custom-data payloads. Each payload
// is the object the peer passed to sendCustom (its own "data" field).
func (b *signalBus) onCustomData(f func(map[string]any)) { b.onCustom.Store(&f) }

func (b *signalBus) readLoop() {
	defer b.close()
	for {
		_, p, err := b.conn.ReadMessage()
		if err != nil {
			utils.Debugf("[vkteams] ws read: %v", err)
			return
		}
		if string(p) == "ping" { // application-level keepalive
			_ = b.writeRaw(websocket.TextMessage, []byte("pong"))
			continue
		}
		var msg struct {
			Type         string          `json:"type"`
			Notification string          `json:"notification"`
			Data         json.RawMessage `json:"data"`
		}
		if json.Unmarshal(p, &msg) != nil {
			continue
		}
		if msg.Type != "notification" || msg.Notification != "custom-data" {
			continue
		}
		var d map[string]any
		if json.Unmarshal(msg.Data, &d) != nil {
			continue
		}
		if fp := b.onCustom.Load(); fp != nil {
			(*fp)(d)
		}
	}
}

// sendCustom relays one custom-data payload to the other participants.
func (b *signalBus) sendCustom(payload map[string]any) error {
	raw, err := json.Marshal(map[string]any{
		"command":  "custom-data",
		"sequence": b.seq.Add(1) - 1, // monotonic from 0, like the web client
		"data":     payload,
	})
	if err != nil {
		return err
	}
	return b.writeRaw(websocket.TextMessage, raw)
}

func (b *signalBus) writeRaw(typ int, p []byte) error {
	b.wmu.Lock()
	defer b.wmu.Unlock()
	return b.conn.WriteMessage(typ, p)
}

// done closes when the bus connection drops.
func (b *signalBus) done() <-chan struct{} { return b.closed }

func (b *signalBus) close() {
	b.closeOnce.Do(func() {
		close(b.closed)
		_ = b.conn.Close()
	})
}
