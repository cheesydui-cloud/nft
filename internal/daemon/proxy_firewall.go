package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"nft/internal/nft/shim"
	"nft/internal/proxysvc"
)

// extraListenApplier is implemented by *forward.Dataplane. Tests' fake
// dataplanes omit it; punch is then a no-op.
type extraListenApplier interface {
	SetExtraListenPorts([]shim.ListenPort)
}

var (
	proxyFWMu  sync.Mutex
	proxyFWSet extraListenApplier
)

func (d *Daemon) wireProxyFirewall() {
	if d == nil {
		return
	}
	if ap, ok := d.dp.(extraListenApplier); ok {
		proxyFWMu.Lock()
		proxyFWSet = ap
		proxyFWMu.Unlock()
		syncProxyFirewallPorts()
	}
}

func syncProxyFirewallPorts() {
	proxyFWMu.Lock()
	ap := proxyFWSet
	proxyFWMu.Unlock()
	if ap == nil {
		return
	}
	ap.SetExtraListenPorts(collectProxyListenPorts())
}

func collectProxyListenPorts() []shim.ListenPort {
	base := coreStateDir()
	seen := map[string]bool{}
	var out []shim.ListenPort
	add := func(proto string, port int) {
		if port < 1 || port > 65535 {
			return
		}
		proto = strings.ToLower(strings.TrimSpace(proto))
		if proto == "" {
			proto = "tcp"
		}
		key := proto + "/" + strconv.Itoa(port)
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, shim.ListenPort{Proto: proto, Port: port})
	}

	// xray / sing-box: instance JSON listen_port + TCP (and UDP when present).
	for _, sub := range []string{"xray", "sing-box"} {
		matches, _ := filepath.Glob(filepath.Join(base, sub, "instance-*.json"))
		for _, p := range matches {
			raw, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			port := listenPortFromCoreJSON(raw)
			if port <= 0 {
				port = proxysvc.ListenPortFromConfig(raw)
			}
			if port > 0 {
				add("tcp", port)
			}
		}
	}

	// mieru: instance fragments carry portBindings with TCP and/or UDP.
	matches, _ := filepath.Glob(filepath.Join(base, "mieru", "instance-*.json"))
	for _, p := range matches {
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		for _, b := range mitaBindingsFromJSON(raw) {
			add(strings.ToLower(b.proto), b.port)
		}
	}
	return out
}

func listenPortFromCoreJSON(raw []byte) int {
	var peek struct {
		ListenPort int `json:"listen_port"`
		Inbounds   []struct {
			ListenPort int `json:"listen_port"`
			Port       int `json:"port"`
		} `json:"inbounds"`
	}
	if err := json.Unmarshal(raw, &peek); err != nil {
		return 0
	}
	if peek.ListenPort > 0 {
		return peek.ListenPort
	}
	for _, in := range peek.Inbounds {
		if in.ListenPort > 0 {
			return in.ListenPort
		}
		if in.Port > 0 {
			return in.Port
		}
	}
	return 0
}

type mitaBind struct {
	port  int
	proto string
}

func mitaBindingsFromJSON(raw []byte) []mitaBind {
	var peek struct {
		PortBindings []struct {
			Port     any    `json:"port"`
			Protocol string `json:"protocol"`
		} `json:"portBindings"`
	}
	if err := json.Unmarshal(raw, &peek); err != nil {
		return nil
	}
	var out []mitaBind
	for _, b := range peek.PortBindings {
		port := 0
		switch v := b.Port.(type) {
		case float64:
			port = int(v)
		case json.Number:
			n, _ := v.Int64()
			port = int(n)
		case string:
			n, _ := strconv.Atoi(strings.TrimSpace(v))
			port = n
		}
		proto := strings.ToUpper(strings.TrimSpace(b.Protocol))
		if proto == "" {
			proto = "TCP"
		}
		if port > 0 {
			out = append(out, mitaBind{port: port, proto: proto})
		}
	}
	return out
}
