package forward

import (
	"fmt"
	"net"
	"sync/atomic"
)

var (
	blockEgressV4 atomic.Bool
	blockEgressV6 atomic.Bool
)

// SetEgressPolicy tells userspace dials which address family to refuse.
// blockV6=true → tcp4 only (no AAAA / no literal IPv6).
func SetEgressPolicy(blockV4, blockV6 bool) {
	blockEgressV4.Store(blockV4)
	blockEgressV6.Store(blockV6)
}

func egressNetwork(addr string) (network string, err error) {
	host, _, splitErr := net.SplitHostPort(addr)
	if splitErr == nil {
		if ip := net.ParseIP(host); ip != nil {
			if ip.To4() != nil {
				if blockEgressV4.Load() {
					return "", fmt.Errorf("egress IPv4 disabled: %s", addr)
				}
				return "tcp4", nil
			}
			if blockEgressV6.Load() {
				return "", fmt.Errorf("egress IPv6 disabled: %s", addr)
			}
			return "tcp6", nil
		}
	}
	if blockEgressV6.Load() && !blockEgressV4.Load() {
		return "tcp4", nil
	}
	if blockEgressV4.Load() && !blockEgressV6.Load() {
		return "tcp6", nil
	}
	return "tcp", nil
}
