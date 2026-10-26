package transport

import (
	"sync/atomic"
	"testing"
)

// A pinned carrier carries the traffic against the priorities, and nothing
// else does; unpinning gives the choice back, and a name the Session does not have is refused.
func TestSessionPinnedCarrierOverridesPriorities(t *testing.T) {
	client, exit, _, _ := linkedSessionsWith(t, func(i int) int {
		if i == 0 {
			return 100 // "alpha" is the highest-priority carrier
		}
		return 50
	}, "alpha", "beta")
	fastKeepalive(client, exit)
	var got atomic.Int32
	exit.Receive(func([]byte) { got.Add(1) })
	startPair(t, client, exit)

	if a := client.ActiveTransport(); a != "alpha" {
		t.Fatalf("before pinning: active = %q, want the higher-priority alpha", a)
	}
	if client.SetPinnedCarrier("nope") {
		t.Fatal("pinning a carrier the Session does not have succeeded")
	}
	if !client.SetPinnedCarrier("beta") {
		t.Fatal("pinning beta was refused")
	}
	if p := client.PinnedCarrier(); p != "beta" {
		t.Fatalf("PinnedCarrier = %q, want beta", p)
	}
	if a := client.ActiveTransports(); len(a) != 1 || a[0] != "beta" {
		t.Fatalf("pinned to beta: active = %v, want [beta]", a)
	}
	before := got.Load()
	if err := client.Send(testIPv4(40, 6)); err != nil {
		t.Fatalf("send over the pinned carrier: %v", err)
	}
	eventually(t, "the packet to arrive over the pinned carrier", func() bool { return got.Load() > before })

	if !client.SetPinnedCarrier("") {
		t.Fatal("unpinning was refused")
	}
	if a := client.ActiveTransport(); a != "alpha" {
		t.Fatalf("after unpinning: active = %q, want the higher-priority alpha again", a)
	}
	if names := client.CarrierNames(); len(names) != 2 {
		t.Fatalf("CarrierNames = %v, want both carriers", names)
	}
}
