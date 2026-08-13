package forward

import (
	"strings"
	"testing"
)

func TestEgressNetworkBlocksLiteralIPv6(t *testing.T) {
	t.Cleanup(func() { SetEgressPolicy(false, false) })
	SetEgressPolicy(false, true)
	_, err := egressNetwork("[2001:db8::1]:443")
	if err == nil || !strings.Contains(err.Error(), "IPv6") {
		t.Fatalf("want IPv6 disabled error, got %v", err)
	}
	netw, err := egressNetwork("1.2.3.4:443")
	if err != nil || netw != "tcp4" {
		t.Fatalf("want tcp4, got %s %v", netw, err)
	}
	netw, err = egressNetwork("example.com:443")
	if err != nil || netw != "tcp4" {
		t.Fatalf("hostname should force tcp4, got %s %v", netw, err)
	}
}

func TestEgressNetworkDefaultDual(t *testing.T) {
	t.Cleanup(func() { SetEgressPolicy(false, false) })
	SetEgressPolicy(false, false)
	netw, err := egressNetwork("example.com:443")
	if err != nil || netw != "tcp" {
		t.Fatalf("want tcp, got %s %v", netw, err)
	}
}
