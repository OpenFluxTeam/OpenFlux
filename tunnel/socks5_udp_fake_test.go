package tunnel

import (
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"

	"golang.org/x/net/proxy"
)

// fakeSOCKS5UDP runs a minimal SOCKS5 server with UDP ASSOCIATE: it echoes
// every relayed datagram back, stripping nothing, so a round trip proves the
// header is written and parsed the way RFC 1928 says.
func fakeSOCKS5UDP(t *testing.T, want *proxy.Auth) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				greet := make([]byte, 2)
				if _, err := io.ReadFull(c, greet); err != nil || greet[0] != 0x05 {
					return
				}
				methods := make([]byte, greet[1])
				if _, err := io.ReadFull(c, methods); err != nil {
					return
				}
				if want != nil {
					_, _ = c.Write([]byte{0x05, 0x02})
					ver := make([]byte, 2)
					if _, err := io.ReadFull(c, ver); err != nil || ver[0] != 0x01 {
						return
					}
					u := make([]byte, int(ver[1]))
					if _, err := io.ReadFull(c, u); err != nil {
						return
					}
					pl := make([]byte, 1)
					if _, err := io.ReadFull(c, pl); err != nil {
						return
					}
					pw := make([]byte, int(pl[0]))
					if _, err := io.ReadFull(c, pw); err != nil {
						return
					}
					if string(u) != want.User || string(pw) != want.Password {
						_, _ = c.Write([]byte{0x01, 0x01})
						return
					}
					_, _ = c.Write([]byte{0x01, 0x00})
				} else {
					_, _ = c.Write([]byte{0x05, 0x00})
				}
				req := make([]byte, 10)
				if _, err := io.ReadFull(c, req); err != nil || req[1] != 0x03 {
					return
				}
				u, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
				if err != nil {
					return
				}
				defer u.Close()
				addr := u.LocalAddr().(*net.UDPAddr)
				reply := []byte{0x05, 0x00, 0x00, 0x01}
				reply = append(reply, addr.IP.To4()...)
				reply = binary.BigEndian.AppendUint16(reply, uint16(addr.Port))
				if _, err := c.Write(reply); err != nil {
					return
				}
				buf := make([]byte, 2048)
				for {
					n, from, err := u.ReadFromUDP(buf)
					if err != nil {
						return
					}
					if _, err := u.WriteToUDP(buf[:n], from); err != nil {
						return
					}
				}
			}(c)
		}
	}()
	return ln.Addr().String()
}

func roundTrip(t *testing.T, proxyAddr string, auth *proxy.Auth) {
	t.Helper()
	conn, err := dialSOCKS5UDP(proxyAddr, auth, "1.1.1.1:53")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	msg := []byte("openflux-udp")
	if _, err := conn.Write(msg); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := make([]byte, 256)
	n, err := conn.Read(got)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got[:n]) != string(msg) {
		t.Fatalf("echo mismatch: %q", got[:n])
	}
}

func TestSOCKS5UDPNoAuth(t *testing.T) { roundTrip(t, fakeSOCKS5UDP(t, nil), nil) }

func TestSOCKS5UDPWithAuth(t *testing.T) {
	auth := &proxy.Auth{User: "u", Password: "p"}
	roundTrip(t, fakeSOCKS5UDP(t, auth), auth)
}

func TestSOCKS5UDPWrongAuthFails(t *testing.T) {
	addr := fakeSOCKS5UDP(t, &proxy.Auth{User: "u", Password: "p"})
	if _, err := dialSOCKS5UDP(addr, &proxy.Auth{User: "u", Password: "x"}, "1.1.1.1:53"); err == nil {
		t.Fatal("a wrong password was accepted")
	}
}
