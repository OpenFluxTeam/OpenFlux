package tunnel

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"

	"golang.org/x/net/proxy"
)

// socks5UDPConn is one SOCKS5 UDP association: the TCP control connection that
// keeps the relay alive, and the UDP socket that carries datagrams with the
// RFC 1928 header of one fixed destination.
type socks5UDPConn struct {
	tcpControl net.Conn
	udpConn    *net.UDPConn
	header     []byte
}

// socksAddr encodes host:port as the ATYP, address and port fields of a SOCKS5
// request: IPv4, IPv6 or a domain name (the relay resolves the last one).
func socksAddr(host string, port int) ([]byte, error) {
	if ip := net.ParseIP(host); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			out := make([]byte, 1+4+2)
			out[0] = 0x01
			copy(out[1:5], ip4)
			binary.BigEndian.PutUint16(out[5:7], uint16(port))
			return out, nil
		}
		out := make([]byte, 1+16+2)
		out[0] = 0x04
		copy(out[1:17], ip.To16())
		binary.BigEndian.PutUint16(out[17:19], uint16(port))
		return out, nil
	}
	if len(host) == 0 || len(host) > 255 {
		return nil, fmt.Errorf("socks5 udp: bad destination host %q", host)
	}
	out := make([]byte, 0, 1+1+len(host)+2)
	out = append(out, 0x03, byte(len(host)))
	out = append(out, host...)
	out = binary.BigEndian.AppendUint16(out, uint16(port))
	return out, nil
}

func dialSOCKS5UDP(proxyAddr string, auth *proxy.Auth, dest string) (net.Conn, error) {
	dHost, dPortStr, err := net.SplitHostPort(dest)
	if err != nil {
		return nil, err
	}
	dPort, err := strconv.Atoi(dPortStr)
	if err != nil {
		return nil, err
	}
	dst, err := socksAddr(dHost, dPort)
	if err != nil {
		return nil, err
	}

	tcpConn, err := net.DialTimeout("tcp", proxyAddr, 10*time.Second)
	if err != nil {
		return nil, err
	}
	closeTCP := func() { _ = tcpConn.Close() }
	// The handshake must not hang a UDP flow if the proxy stalls.
	if err := tcpConn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		closeTCP()
		return nil, err
	}

	// Greeting: no-auth, plus user/password when the proxy was configured with them.
	methods := []byte{0x00}
	if auth != nil {
		methods = []byte{0x00, 0x02}
	}
	if _, err := tcpConn.Write(append([]byte{0x05, byte(len(methods))}, methods...)); err != nil {
		closeTCP()
		return nil, err
	}
	sel := make([]byte, 2)
	if _, err := io.ReadFull(tcpConn, sel); err != nil || sel[0] != 0x05 {
		closeTCP()
		return nil, fmt.Errorf("socks5 auth: %v", err)
	}
	switch sel[1] {
	case 0x00:
	case 0x02:
		if auth == nil {
			closeTCP()
			return nil, fmt.Errorf("socks5: proxy wants user/password, none configured")
		}
		user, pass := []byte(auth.User), []byte(auth.Password)
		if len(user) > 255 || len(pass) > 255 {
			closeTCP()
			return nil, fmt.Errorf("socks5: credentials too long")
		}
		req := append([]byte{0x01, byte(len(user))}, user...)
		req = append(req, byte(len(pass)))
		req = append(req, pass...)
		if _, err := tcpConn.Write(req); err != nil {
			closeTCP()
			return nil, err
		}
		rr := make([]byte, 2)
		if _, err := io.ReadFull(tcpConn, rr); err != nil || rr[1] != 0x00 {
			closeTCP()
			return nil, fmt.Errorf("socks5: user/password rejected (%v)", rr)
		}
	default:
		closeTCP()
		return nil, fmt.Errorf("socks5: no acceptable auth method (server chose %#x)", sel[1])
	}

	// UDP ASSOCIATE: the relay address comes back in the reply, whatever its ATYP.
	if _, err := tcpConn.Write([]byte{0x05, 0x03, 0x00, 0x01, 0, 0, 0, 0, 0, 0}); err != nil {
		closeTCP()
		return nil, err
	}
	hdr := make([]byte, 4)
	if _, err := io.ReadFull(tcpConn, hdr); err != nil {
		closeTCP()
		return nil, err
	}
	if hdr[0] != 0x05 || hdr[1] != 0x00 {
		closeTCP()
		return nil, fmt.Errorf("socks5: udp associate failed (rep=%#x)", hdr[1])
	}
	relayIP, relayPort, err := readSocksAddr(tcpConn, hdr[3])
	if err != nil {
		closeTCP()
		return nil, err
	}
	if relayIP == nil || relayIP.IsUnspecified() {
		sHost, _, _ := net.SplitHostPort(proxyAddr)
		relayIP = net.ParseIP(sHost)
		if relayIP == nil {
			closeTCP()
			return nil, fmt.Errorf("socks5: relay address unusable")
		}
	}
	_ = tcpConn.SetDeadline(time.Time{})

	uConn, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: relayIP, Port: relayPort})
	if err != nil {
		closeTCP()
		return nil, err
	}

	header := make([]byte, 0, 3+len(dst))
	header = append(header, 0, 0, 0) // RSV, RSV, FRAG
	header = append(header, dst...)
	return &socks5UDPConn{tcpControl: tcpConn, udpConn: uConn, header: header}, nil
}

// readSocksAddr reads the address that follows a 4-byte SOCKS5 reply header.
func readSocksAddr(r io.Reader, atyp byte) (net.IP, int, error) {
	switch atyp {
	case 0x01:
		b := make([]byte, 4+2)
		if _, err := io.ReadFull(r, b); err != nil {
			return nil, 0, err
		}
		return net.IP(b[:4]), int(binary.BigEndian.Uint16(b[4:6])), nil
	case 0x04:
		b := make([]byte, 16+2)
		if _, err := io.ReadFull(r, b); err != nil {
			return nil, 0, err
		}
		return net.IP(b[:16]), int(binary.BigEndian.Uint16(b[16:18])), nil
	case 0x03:
		l := make([]byte, 1)
		if _, err := io.ReadFull(r, l); err != nil {
			return nil, 0, err
		}
		b := make([]byte, int(l[0])+2)
		if _, err := io.ReadFull(r, b); err != nil {
			return nil, 0, err
		}
		host := string(b[:len(b)-2])
		ips, err := net.LookupIP(host)
		if err != nil || len(ips) == 0 {
			return nil, 0, fmt.Errorf("socks5: relay host %q: %v", host, err)
		}
		return ips[0], int(binary.BigEndian.Uint16(b[len(b)-2:])), nil
	}
	return nil, 0, fmt.Errorf("socks5: unsupported reply ATYP %#x", atyp)
}

func (c *socks5UDPConn) Read(b []byte) (int, error) {
	buf := make([]byte, 65535)
	n, err := c.udpConn.Read(buf)
	if err != nil {
		return 0, err
	}
	skip, err := socksHeaderLen(buf[:n])
	if err != nil {
		return 0, err
	}
	return copy(b, buf[skip:n]), nil
}

// socksHeaderLen returns how many bytes of a relayed datagram are header.
func socksHeaderLen(b []byte) (int, error) {
	if len(b) < 4 {
		return 0, fmt.Errorf("socks5 udp: packet too short")
	}
	switch b[3] {
	case 0x01:
		if len(b) < 10 {
			return 0, fmt.Errorf("socks5 udp: short ipv4 header")
		}
		return 10, nil
	case 0x04:
		if len(b) < 22 {
			return 0, fmt.Errorf("socks5 udp: short ipv6 header")
		}
		return 22, nil
	case 0x03:
		if len(b) < 5 || len(b) < 5+int(b[4])+2 {
			return 0, fmt.Errorf("socks5 udp: short domain header")
		}
		return 5 + int(b[4]) + 2, nil
	}
	return 0, fmt.Errorf("socks5 udp: unsupported ATYP %#x", b[3])
}

func (c *socks5UDPConn) Write(b []byte) (int, error) {
	pkt := make([]byte, len(c.header)+len(b))
	copy(pkt, c.header)
	copy(pkt[len(c.header):], b)
	if _, err := c.udpConn.Write(pkt); err != nil {
		return 0, err
	}
	return len(b), nil
}

func (c *socks5UDPConn) Close() error {
	_ = c.tcpControl.Close()
	return c.udpConn.Close()
}

func (c *socks5UDPConn) LocalAddr() net.Addr                { return c.udpConn.LocalAddr() }
func (c *socks5UDPConn) RemoteAddr() net.Addr               { return c.udpConn.RemoteAddr() }
func (c *socks5UDPConn) SetDeadline(t time.Time) error      { return c.udpConn.SetDeadline(t) }
func (c *socks5UDPConn) SetReadDeadline(t time.Time) error  { return c.udpConn.SetReadDeadline(t) }
func (c *socks5UDPConn) SetWriteDeadline(t time.Time) error { return c.udpConn.SetWriteDeadline(t) }
