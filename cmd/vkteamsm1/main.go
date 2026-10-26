// Command vkteamsm1 is a lab harness for milestone M1 of the vkteams carrier:
// prove that ONE media lane (genuine SRTP media through the call's TURN relay)
// clears the relay's RTP class rate (~260 KB/s per allocation), far above the
// DTLS/DataChannel class (~10 KB/s) the JS transport is stuck in.
//
// Run two instances against the SAME live call code (the part of a
// calls.myteam.mail.ru/join/<code> link after /join/):
//
//	vkteamsm1 -exit -code <code>      # on one host: answers, measures receive
//	vkteamsm1       -code <code>      # on the other: offers, blasts data
//
// Both may run on the same host (two guest participants) - with -relay (the
// default) the data still goes host -> VK TURN -> host, so it measures the
// relay's class policing, which is the point of M1. This is a measurement tool,
// not part of the shipped transport.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/p1neappleXpress/OpenFlux/transport/vkteams"
)

func main() {
	code := flag.String("code", "", "call code (part after /join/); required")
	isExit := flag.Bool("exit", false, "run as the exit end (answerer/receiver)")
	relay := flag.Bool("relay", true, "force ICE relay (exercise the TURN path)")
	mbps := flag.Float64("mbps", 4, "client: attempted send rate, MB/s")
	size := flag.Int("size", 1200, "client: payload bytes per RTP packet")
	flag.Parse()

	if *code == "" {
		fmt.Fprintln(os.Stderr, "need -code <call code>")
		os.Exit(2)
	}

	lane, err := vkteams.DialM1Lane(*code, *isExit, *relay)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dial: %v\n", err)
		os.Exit(1)
	}
	defer lane.Stop()

	var rx atomic.Uint64
	lane.Receive(func(b []byte) { rx.Add(uint64(len(b))) })

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	// Reporter: receive rate (both ends) and send rate (client).
	go func() {
		var lastRx uint64
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				r := rx.Load()
				st := lane.Stats()
				role := "client"
				if *isExit {
					role = "exit  "
				}
				fmt.Printf("[%s] conn=%v  rx=%6.1f KB/s (tot %d)  tx=%d pkt\n",
					role, lane.IsConnected(),
					float64(r-lastRx)/1000, r, st.PacketsSent)
				lastRx = r
			}
		}
	}()

	if !*isExit {
		go blast(lane, stop, *mbps, *size)
	}

	<-stop
	fmt.Println("stopping")
}

// blast sends fixed-size packets at the attempted rate once the lane connects.
// The TURN relay caps the RTP class, so the receiver's rate is what M1 measures.
func blast(lane *vkteams.M1Lane, stop <-chan os.Signal, mbps float64, size int) {
	for !lane.IsConnected() {
		select {
		case <-stop:
			return
		case <-time.After(200 * time.Millisecond):
		}
	}
	fmt.Println("[client] connected; blasting")

	payload := make([]byte, size)
	gap := time.Duration(float64(size) / (mbps * 1e6) * float64(time.Second))
	var seq uint64
	next := time.Now()
	for {
		select {
		case <-stop:
			return
		default:
		}
		// Stamp a sequence so a dropped/reordered packet is visible if needed.
		seq++
		payload[0] = byte(seq)
		payload[1] = byte(seq >> 8)
		if err := lane.Send(payload); err != nil {
			time.Sleep(20 * time.Millisecond) // not ready yet / srtp warming
			continue
		}
		next = next.Add(gap)
		if d := time.Until(next); d > 0 {
			time.Sleep(d)
		} else if d < -50*time.Millisecond {
			next = time.Now() // fell behind; don't accumulate debt
		}
	}
}
