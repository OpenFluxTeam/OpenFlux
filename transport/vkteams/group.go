package vkteams

import (
	"fmt"
	"sync"

	"github.com/p1neappleXpress/OpenFlux/transport"
	"github.com/p1neappleXpress/OpenFlux/utils"
)

// Group is a set of media lanes (PeerConnections) on a shared call: one join,
// one signaling bus, many lanes. It implements transport.LaneSource so the
// stripe can grow it, and multiplexes the single signaling bus across all lanes
// by the lane index each end puts in every signaling message.
type Group struct {
	cc         *callCtx
	forceRelay bool
	config     transport.TransportConfig

	mu    sync.Mutex
	lanes []*mediaLane
	grow  func()
}

// NewGroup joins the call and creates n initial media lanes. The lanes are not
// started: StripedTransport.Start does that.
func NewGroup(code string, isExit, forceRelay bool, n int, config transport.TransportConfig) (*Group, error) {
	cc, err := joinCall(code, isExit)
	if err != nil {
		return nil, err
	}
	g := &Group{cc: cc, forceRelay: forceRelay, config: config}
	cc.bus.onCustomData(g.demux)
	for i := 0; i < n; i++ {
		if _, err := g.newLaneLocked(); err != nil {
			cc.bus.close()
			return nil, fmt.Errorf("lane %d: %w", i, err)
		}
	}
	return g, nil
}

func (g *Group) newLaneLocked() (*mediaLane, error) {
	ml, err := newMediaLane(g.cc, g.forceRelay, g.config)
	if err != nil {
		return nil, err
	}
	ml.grouped = true
	ml.laneIdx = len(g.lanes)
	g.lanes = append(g.lanes, ml)
	return ml, nil
}

// Lanes returns the lanes created so far as transports.
func (g *Group) Lanes() []transport.Transport {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]transport.Transport, len(g.lanes))
	for i, l := range g.lanes {
		out[i] = l
	}
	return out
}

// NewLane implements transport.LaneSource.
func (g *Group) NewLane() transport.Transport {
	g.mu.Lock()
	ml, err := g.newLaneLocked()
	g.mu.Unlock()
	if err != nil {
		utils.Infof("[vkteams] NewLane: %v", err)
		return nil
	}
	return ml
}

// SetGrowHook implements transport.LaneSource.
func (g *Group) SetGrowHook(f func()) {
	g.mu.Lock()
	g.grow = f
	g.mu.Unlock()
}

// demux routes one inbound custom-data message to the right lane by its
// "laneIdx" field. Messages without a lane index are broadcast to all lanes
// (M1 compat; the M1 harness sends no index).
func (g *Group) demux(d map[string]any) {
	idx := -1
	if v, ok := d["laneIdx"].(float64); ok {
		idx = int(v)
	}

	g.mu.Lock()
	if idx >= 0 && idx < len(g.lanes) {
		l := g.lanes[idx]
		g.mu.Unlock()
		l.onCustom(d)
		return
	}
	if idx >= len(g.lanes) && g.cc.isExit {
		grow := g.grow
		n := len(g.lanes)
		g.mu.Unlock()
		if grow != nil {
			utils.Infof("[vkteams] peer has lane %d, we have %d: growing", idx, n)
			grow()
		}
		return
	}
	lanes := append([]*mediaLane(nil), g.lanes...)
	g.mu.Unlock()
	for _, l := range lanes {
		l.onCustom(d)
	}
}

// Stop tears down the signaling bus (the stripe stops each lane individually).
func (g *Group) Stop() {
	g.cc.bus.close()
}
