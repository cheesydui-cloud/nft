package forward

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// dialUpstream opens a TCP connection to addr, optionally through a SOCKS5
// proxy. When exitProxy is empty this is a plain dial (same as historical
// behavior). When set to socks5://[user:pass@]host:port, the dialer performs
// a SOCKS5 handshake and CONNECT to addr (the business target).
func dialUpstream(addr string, exitProxy ...string) (net.Conn, error) {
	proxy := ""
	if len(exitProxy) > 0 {
		proxy = strings.TrimSpace(exitProxy[0])
	}
		if proxy == "" {
			network, err := egressNetwork(addr)
			if err != nil {
				return nil, err
			}
			c, err := net.DialTimeout(network, addr, dialTimeout)
			if err != nil {
				return nil, err
			}
			setKeepAlive(c)
			return c, nil
		}
		return dialViaSOCKS5(proxy, addr)
	}

func dialViaSOCKS5(proxyURI, target string) (net.Conn, error) {
	u, err := url.Parse(proxyURI)
	if err != nil {
		return nil, fmt.Errorf("socks5 uri: %w", err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "socks5" && scheme != "socks" {
		return nil, fmt.Errorf("unsupported proxy scheme %q", u.Scheme)
	}
	proxyHost := u.Hostname()
	proxyPort := u.Port()
	if proxyHost == "" || proxyPort == "" {
		return nil, fmt.Errorf("socks5 uri missing host:port")
	}
		proxyAddr := net.JoinHostPort(proxyHost, proxyPort)

		network, nerr := egressNetwork(proxyAddr)
		if nerr != nil {
			return nil, nerr
		}
		c, err := net.DialTimeout(network, proxyAddr, dialTimeout)
	if err != nil {
		return nil, fmt.Errorf("socks5 dial proxy: %w", err)
	}
	setKeepAlive(c)
	_ = c.SetDeadline(time.Now().Add(dialTimeout))
	defer func() { _ = c.SetDeadline(time.Time{}) }()

	user := ""
	pass := ""
	if u.User != nil {
		user = u.User.Username()
		pass, _ = u.User.Password()
	}

	// Greeting: VER=5, methods.
	if user != "" {
		// Offer no-auth + username/password.
		if _, err := c.Write([]byte{0x05, 0x02, 0x00, 0x02}); err != nil {
			c.Close()
			return nil, err
		}
	} else {
		if _, err := c.Write([]byte{0x05, 0x01, 0x00}); err != nil {
			c.Close()
			return nil, err
		}
	}
	var greets [2]byte
	if _, err := io.ReadFull(c, greets[:]); err != nil {
		c.Close()
		return nil, fmt.Errorf("socks5 greeting: %w", err)
	}
	if greets[0] != 0x05 {
		c.Close()
		return nil, fmt.Errorf("socks5: bad version %d", greets[0])
	}
	switch greets[1] {
	case 0x00:
		// no auth
	case 0x02:
		if user == "" {
			c.Close()
			return nil, fmt.Errorf("socks5: proxy requires auth")
		}
		// Username/password subnegotiation (RFC 1929).
		if len(user) > 255 || len(pass) > 255 {
			c.Close()
			return nil, fmt.Errorf("socks5: credentials too long")
		}
		auth := make([]byte, 0, 3+len(user)+len(pass))
		auth = append(auth, 0x01, byte(len(user)))
		auth = append(auth, user...)
		auth = append(auth, byte(len(pass)))
		auth = append(auth, pass...)
		if _, err := c.Write(auth); err != nil {
			c.Close()
			return nil, err
		}
		var aresp [2]byte
		if _, err := io.ReadFull(c, aresp[:]); err != nil {
			c.Close()
			return nil, fmt.Errorf("socks5 auth: %w", err)
		}
		if aresp[1] != 0x00 {
			c.Close()
			return nil, fmt.Errorf("socks5: authentication failed")
		}
	case 0xff:
		c.Close()
		return nil, fmt.Errorf("socks5: no acceptable auth method")
	default:
		c.Close()
		return nil, fmt.Errorf("socks5: unsupported auth method %d", greets[1])
	}

	host, portStr, err := net.SplitHostPort(target)
	if err != nil {
		c.Close()
		return nil, fmt.Errorf("socks5 target: %w", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		c.Close()
		return nil, fmt.Errorf("socks5 target port invalid")
	}

	// CONNECT request.
	req := []byte{0x05, 0x01, 0x00} // VER CMD RSV
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			req = append(req, 0x01)
			req = append(req, v4...)
		} else {
			v6 := ip.To16()
			req = append(req, 0x04)
			req = append(req, v6...)
		}
	} else {
		if len(host) > 255 {
			c.Close()
			return nil, fmt.Errorf("socks5: hostname too long")
		}
		req = append(req, 0x03, byte(len(host)))
		req = append(req, host...)
	}
	var pb [2]byte
	binary.BigEndian.PutUint16(pb[:], uint16(port))
	req = append(req, pb[:]...)
	if _, err := c.Write(req); err != nil {
		c.Close()
		return nil, err
	}

	// Reply: VER REP RSV ATYP ...
	var hdr [4]byte
	if _, err := io.ReadFull(c, hdr[:]); err != nil {
		c.Close()
		return nil, fmt.Errorf("socks5 reply: %w", err)
	}
	if hdr[0] != 0x05 {
		c.Close()
		return nil, fmt.Errorf("socks5: bad reply version")
	}
	if hdr[1] != 0x00 {
		c.Close()
		return nil, fmt.Errorf("socks5: connect failed (rep=%d)", hdr[1])
	}
	// Consume bind address.
	switch hdr[3] {
	case 0x01: // IPv4
		var skip [4 + 2]byte
		if _, err := io.ReadFull(c, skip[:]); err != nil {
			c.Close()
			return nil, err
		}
	case 0x04: // IPv6
		var skip [16 + 2]byte
		if _, err := io.ReadFull(c, skip[:]); err != nil {
			c.Close()
			return nil, err
		}
	case 0x03: // domain
		var ln [1]byte
		if _, err := io.ReadFull(c, ln[:]); err != nil {
			c.Close()
			return nil, err
		}
		skip := make([]byte, int(ln[0])+2)
		if _, err := io.ReadFull(c, skip); err != nil {
			c.Close()
			return nil, err
		}
	default:
		c.Close()
		return nil, fmt.Errorf("socks5: unknown atyp %d", hdr[3])
	}
	return c, nil
}
