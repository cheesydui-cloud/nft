package forward

import (
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"
)

// startMockSOCKS5 runs a minimal SOCKS5 server that accepts no-auth, CONNECTs
// to the requested target by dialing it, and relays bytes both ways.
func startMockSOCKS5(t *testing.T, requireUser, requirePass string) (proxyAddr string, cleanup func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go handleMockSOCKS(c, requireUser, requirePass)
		}
	}()
	return ln.Addr().String(), func() { ln.Close(); <-done }
}

func handleMockSOCKS(c net.Conn, user, pass string) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	var hdr [2]byte
	if _, err := io.ReadFull(c, hdr[:]); err != nil {
		return
	}
	if hdr[0] != 0x05 {
		return
	}
	methods := make([]byte, int(hdr[1]))
	if _, err := io.ReadFull(c, methods); err != nil {
		return
	}
	wantAuth := user != ""
	selected := byte(0xff)
	for _, m := range methods {
		if wantAuth && m == 0x02 {
			selected = 0x02
			break
		}
		if !wantAuth && m == 0x00 {
			selected = 0x00
			break
		}
	}
	if _, err := c.Write([]byte{0x05, selected}); err != nil || selected == 0xff {
		return
	}
	if selected == 0x02 {
		var ver [1]byte
		if _, err := io.ReadFull(c, ver[:]); err != nil {
			return
		}
		var ulen [1]byte
		if _, err := io.ReadFull(c, ulen[:]); err != nil {
			return
		}
		ubuf := make([]byte, int(ulen[0]))
		if _, err := io.ReadFull(c, ubuf); err != nil {
			return
		}
		var plen [1]byte
		if _, err := io.ReadFull(c, plen[:]); err != nil {
			return
		}
		pbuf := make([]byte, int(plen[0]))
		if _, err := io.ReadFull(c, pbuf); err != nil {
			return
		}
		ok := string(ubuf) == user && string(pbuf) == pass
		status := byte(0x00)
		if !ok {
			status = 0x01
		}
		_, _ = c.Write([]byte{0x01, status})
		if !ok {
			return
		}
	}
	// CONNECT request
	var req [4]byte
	if _, err := io.ReadFull(c, req[:]); err != nil {
		return
	}
	if req[0] != 0x05 || req[1] != 0x01 {
		return
	}
	var host string
	switch req[3] {
	case 0x01:
		var ip [4]byte
		if _, err := io.ReadFull(c, ip[:]); err != nil {
			return
		}
		host = net.IP(ip[:]).String()
	case 0x03:
		var ln [1]byte
		if _, err := io.ReadFull(c, ln[:]); err != nil {
			return
		}
		b := make([]byte, int(ln[0]))
		if _, err := io.ReadFull(c, b); err != nil {
			return
		}
		host = string(b)
	case 0x04:
		var ip [16]byte
		if _, err := io.ReadFull(c, ip[:]); err != nil {
			return
		}
		host = net.IP(ip[:]).String()
	default:
		return
	}
	var pb [2]byte
	if _, err := io.ReadFull(c, pb[:]); err != nil {
		return
	}
	port := binary.BigEndian.Uint16(pb[:])
	target := net.JoinHostPort(host, itoa(int(port)))
	up, err := net.DialTimeout("tcp", target, 2*time.Second)
	if err != nil {
		_, _ = c.Write([]byte{0x05, 0x05, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}
	defer up.Close()
	// Success reply with zero bind addr.
	_, _ = c.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
	_ = c.SetDeadline(time.Time{})
	done := make(chan struct{}, 2)
	go func() { io.Copy(up, c); done <- struct{}{} }()
	go func() { io.Copy(c, up); done <- struct{}{} }()
	<-done
}

func itoa(n int) string {
	return strconvItoa(n)
}

// local strconv to avoid extra import noise in helper — keep test self-contained.
func strconvItoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func TestDialUpstreamPlain(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		buf := make([]byte, 4)
		io.ReadFull(c, buf)
		c.Write([]byte("pong"))
	}()
	c, err := dialUpstream(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(c, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "pong" {
		t.Fatalf("got %q", buf)
	}
}

func TestDialUpstreamSOCKS5NoAuth(t *testing.T) {
	// Echo target.
	tgtLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tgtLn.Close()
	go func() {
		c, _ := tgtLn.Accept()
		if c == nil {
			return
		}
		defer c.Close()
		io.Copy(c, c)
	}()

	proxyAddr, cleanup := startMockSOCKS5(t, "", "")
	defer cleanup()

	uri := "socks5://" + proxyAddr
	c, err := dialUpstream(tgtLn.Addr().String(), uri)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Write([]byte("hi")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 2)
	if _, err := io.ReadFull(c, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "hi" {
		t.Fatalf("got %q", buf)
	}
}

func TestDialUpstreamSOCKS5Auth(t *testing.T) {
	tgtLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tgtLn.Close()
	go func() {
		c, _ := tgtLn.Accept()
		if c == nil {
			return
		}
		defer c.Close()
		io.Copy(c, c)
	}()

	proxyAddr, cleanup := startMockSOCKS5(t, "u1", "p1")
	defer cleanup()

	uri := "socks5://u1:p1@" + proxyAddr
	c, err := dialUpstream(tgtLn.Addr().String(), uri)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Write([]byte("ok")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 2)
	if _, err := io.ReadFull(c, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "ok" {
		t.Fatalf("got %q", buf)
	}
}

func TestDialUpstreamSOCKS5BadAuth(t *testing.T) {
	proxyAddr, cleanup := startMockSOCKS5(t, "u1", "p1")
	defer cleanup()
	_, err := dialUpstream("127.0.0.1:9", "socks5://u1:wrong@"+proxyAddr)
	if err == nil {
		t.Fatal("expected auth failure")
	}
}
