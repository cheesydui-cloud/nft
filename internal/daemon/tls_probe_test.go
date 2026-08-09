package daemon

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"testing"
	"time"

	"nft/internal/wsproto"
)

func TestDoProbeExTLSGood(t *testing.T) {
	ln, conf := mustPlainTLSServer(t, []string{"h2", "http/1.1"}, "www.example.com")
	defer ln.Close()
	go serveTLS(ln, conf)
	addr := ln.Addr().String()
	ack := doProbeEx(wsproto.Probe{Target: addr, Mode: "tls", ServerName: "www.example.com"})
	if !ack.OK {
		t.Fatalf("probe failed: %s", ack.Error)
	}
	if !ack.TLS13 {
		t.Fatalf("want TLS1.3 got %s", ack.TLSVersion)
	}
	if !ack.H2 {
		t.Fatalf("want h2 alpn got %q", ack.ALPN)
	}
	if ack.Score != "good" {
		t.Fatalf("score=%s summary=%s", ack.Score, ack.Summary)
	}
}

func TestDoProbeExTCP(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, _ := ln.Accept()
		if c != nil {
			c.Close()
		}
	}()
	ack := doProbeEx(wsproto.Probe{Target: ln.Addr().String(), Mode: "tcp"})
	if !ack.OK {
		t.Fatalf("tcp probe: %s", ack.Error)
	}
}

func serveTLS(ln net.Listener, conf *tls.Config) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go func(c net.Conn) {
			defer c.Close()
			tc := tls.Server(c, conf)
			_ = tc.Handshake()
			_ = tc.Close()
		}(c)
	}
}

func mustPlainTLSServer(t *testing.T, alpn []string, dnsName string) (net.Listener, *tls.Config) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: dnsName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		DNSNames:     []string{dnsName},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	conf := &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   alpn,
		MinVersion:   tls.VersionTLS13,
	}
	pln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return pln, conf
}
