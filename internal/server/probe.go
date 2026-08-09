package server

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"nft/internal/db"
	"nft/internal/wsproto"
)

const probeTimeout = 5 * time.Second

type probeResult struct {
	OK      bool       `json:"ok"`
	Latency int        `json:"latency_ms"`
	Error   string     `json:"error,omitempty"`
	Hops    []hopProbe `json:"hops,omitempty"`
	// TLS dest fields (mode=tls)
	TLSVersion   string   `json:"tls_version,omitempty"`
	ALPN         string   `json:"alpn,omitempty"`
	Cipher       string   `json:"cipher,omitempty"`
	CertCN       string   `json:"cert_cn,omitempty"`
	CertDNS      []string `json:"cert_dns,omitempty"`
	CertNotAfter int64    `json:"cert_not_after,omitempty"`
	SNIMatch     bool     `json:"sni_match,omitempty"`
	TLS13        bool     `json:"tls13,omitempty"`
	H2           bool     `json:"h2,omitempty"`
	Score        string   `json:"score,omitempty"`
	Summary      string   `json:"summary,omitempty"`
	Mode         string   `json:"mode,omitempty"`
	Target       string   `json:"target,omitempty"`
	ServerName   string   `json:"server_name,omitempty"`
}

type hopProbe struct {
	Node    string `json:"node"`
	Target  string `json:"target"`
	Latency int    `json:"latency_ms"`
	Error   string `json:"error,omitempty"`
}

func (s *Server) probeEndpoint(w http.ResponseWriter, r *http.Request) {
	target := r.URL.Query().Get("target")
	if target == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(probeResult{Error: "missing target"})
		return
	}
	w.Header().Set("Content-Type", "application/json")

	mode := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("mode")))
	if mode == "" {
		mode = "tcp"
	}
	serverName := strings.TrimSpace(r.URL.Query().Get("server_name"))
	if serverName == "" {
		serverName = strings.TrimSpace(r.URL.Query().Get("sni"))
	}

	actor := userFromCtx(r.Context())

	nodeStr := r.URL.Query().Get("node")
	if nodeStr != "" {
		nodeID, err := strconv.ParseInt(nodeStr, 10, 64)
		if err != nil {
			json.NewEncoder(w).Encode(probeResult{Error: "invalid node id"})
			return
		}
		n, err := db.GetNode(s.DB, nodeID)
		if err != nil {
			json.NewEncoder(w).Encode(probeResult{Error: "node not found"})
			return
		}
		// A non-admin may probe only through nodes they've been granted, so the
		// node can't be used as a scanning proxy against arbitrary targets.
		if actor != nil && actor.Role != "admin" {
			if _, err := db.CheckNodeAccess(s.DB, actor.ID, nodeID); err != nil {
				w.WriteHeader(http.StatusForbidden)
				json.NewEncoder(w).Encode(probeResult{Error: "无权操作该节点"})
				return
			}
		}
		if n.NodeType == "composite" {
			// TLS dest probe on composite: only exit hop (same as TCP).
			if mode == "tls" {
				s.probeCompositeTLS(w, nodeID, target, serverName)
				return
			}
			s.probeCompositeToTarget(w, nodeID, target)
			return
		}
		req := wsproto.Probe{Target: target, Mode: mode, ServerName: serverName}
		ack, err := s.Hub.SendProbeEx(nodeID, req)
		if err != nil {
			json.NewEncoder(w).Encode(probeResult{Error: err.Error(), Mode: mode, Target: target, ServerName: serverName})
			return
		}
		json.NewEncoder(w).Encode(probeAckToResult(ack, mode, target, serverName))
		return
	}

	// The node-less branch makes the panel process itself dial the target — an
	// SSRF primitive into the panel's own network. The UI never uses it (it
	// always passes a node), so restrict it to admins.
	if actor == nil || actor.Role != "admin" {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(probeResult{Error: "无权操作"})
		return
	}

	if mode == "tls" {
		// Local panel TLS probe (admin only) — same checks without a node.
		ack := localTLSProbe(target, serverName)
		json.NewEncoder(w).Encode(probeAckToResult(ack, mode, target, serverName))
		return
	}

	start := time.Now()
	conn, err := net.DialTimeout("tcp", target, probeTimeout)
	elapsed := time.Since(start)
	if err != nil {
		json.NewEncoder(w).Encode(probeResult{Error: err.Error(), Mode: mode, Target: target})
		return
	}
	conn.Close()
	json.NewEncoder(w).Encode(probeResult{OK: true, Latency: int(elapsed.Milliseconds()), Mode: mode, Target: target, Score: "ok", Summary: "TCP 可达"})
}

func probeAckToResult(ack wsproto.ProbeAck, mode, target, serverName string) probeResult {
	return probeResult{
		OK:           ack.OK,
		Latency:      ack.Latency,
		Error:        ack.Error,
		TLSVersion:   ack.TLSVersion,
		ALPN:         ack.ALPN,
		Cipher:       ack.Cipher,
		CertCN:       ack.CertCN,
		CertDNS:      ack.CertDNS,
		CertNotAfter: ack.CertNotAfter,
		SNIMatch:     ack.SNIMatch,
		TLS13:        ack.TLS13,
		H2:           ack.H2,
		Score:        ack.Score,
		Summary:      ack.Summary,
		Mode:         mode,
		Target:       target,
		ServerName:   serverName,
	}
}

// probeCompositeTLS probes only the last hop with TLS mode.
func (s *Server) probeCompositeTLS(w http.ResponseWriter, compositeID int64, target, serverName string) {
	hops, err := db.ListNodeHops(s.DB, compositeID)
	if err != nil || len(hops) == 0 {
		json.NewEncoder(w).Encode(probeResult{Error: "no hops", Mode: "tls", Target: target})
		return
	}
	last := hops[len(hops)-1]
	ack, err := s.Hub.SendProbeEx(last.HopNodeID, wsproto.Probe{Target: target, Mode: "tls", ServerName: serverName})
	if err != nil {
		json.NewEncoder(w).Encode(probeResult{Error: err.Error(), Mode: "tls", Target: target, ServerName: serverName})
		return
	}
	json.NewEncoder(w).Encode(probeAckToResult(ack, "tls", target, serverName))
}

func (s *Server) probeCompositeToTarget(w http.ResponseWriter, compositeID int64, target string) {
	hops, err := db.ListNodeHops(s.DB, compositeID)
	if err != nil || len(hops) == 0 {
		json.NewEncoder(w).Encode(probeResult{Error: "no hops"})
		return
	}
	// Only the last child dials the real target, so it's the only leg with a
	// concrete data-plane endpoint to probe outside a rule — inter-child legs
	// have no listen port until the composite is instantiated in a rule (use the
	// rule-level chain probe for full per-hop latency). Still, report every
	// child: a dead middle child means the composite can't work, so its liveness
	// must fold into the overall result rather than being ignored while the exit
	// leg alone reports OK.
	results := make([]hopProbe, len(hops))
	allOK := true
	total := 0
	for i, h := range hops {
		n, gerr := db.GetNode(s.DB, h.HopNodeID)
		name := fmt.Sprintf("#%d", h.HopNodeID)
		if gerr == nil {
			name = n.Name
		}
		hp := hopProbe{Node: name}
		if i == len(hops)-1 {
			hp.Target = target
			ack, perr := s.Hub.SendProbe(h.HopNodeID, target)
			switch {
			case perr != nil:
				hp.Error = perr.Error()
			case !ack.OK:
				if hp.Error = ack.Error; hp.Error == "" {
					hp.Error = "不通"
				}
			default:
				hp.Latency = ack.Latency
			}
		} else if gerr != nil || n.Online != 1 || n.Disabled {
			hp.Error = "节点离线"
		}
		if hp.Error != "" {
			allOK = false
		} else {
			total += hp.Latency
		}
		results[i] = hp
	}
	json.NewEncoder(w).Encode(probeResult{OK: allOK, Latency: total, Hops: results})
}

func (s *Server) probeChainEndpoint(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// rule_id is the canonical param; rule/chain are older aliases kept working.
	ruleIDStr := r.URL.Query().Get("rule_id")
	if ruleIDStr == "" {
		ruleIDStr = r.URL.Query().Get("rule")
	}
	if ruleIDStr == "" {
		ruleIDStr = r.URL.Query().Get("chain")
	}
	ruleID, err := strconv.ParseInt(ruleIDStr, 10, 64)
	if err != nil {
		json.NewEncoder(w).Encode(probeResult{Error: "invalid rule id"})
		return
	}
	rule, err := db.GetRule(s.DB, ruleID)
	if err != nil {
		json.NewEncoder(w).Encode(probeResult{Error: "rule not found"})
		return
	}
	// A non-admin may probe only their own rule, so the per-hop node names and
	// targets of other users' rules don't leak through the chain probe.
	if u := userFromCtx(r.Context()); u != nil && u.Role != "admin" {
		if !rule.OwnerID.Valid || rule.OwnerID.Int64 != u.ID {
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(probeResult{Error: "无权操作该规则"})
			return
		}
	}
	hops, err := db.ListRuleHops(s.DB, ruleID)
	if err != nil || len(hops) == 0 {
		json.NewEncoder(w).Encode(probeResult{Error: "no hops"})
		return
	}

	type probeTask struct {
		idx    int
		nodeID int64
		name   string
		target string
	}
	var tasks []probeTask
	for i, h := range hops {
		target := net.JoinHostPort(h.TargetHost, strconv.Itoa(h.TargetPort))
		nodeName := fmt.Sprintf("#%d", h.NodeID)
		if n, err := db.GetNode(s.DB, h.NodeID); err == nil {
			nodeName = n.Name
		}
		tasks = append(tasks, probeTask{idx: i, nodeID: h.NodeID, name: nodeName, target: target})
	}

	results := make([]hopProbe, len(tasks))
	var wg sync.WaitGroup
	for i, t := range tasks {
		wg.Add(1)
		go func(i int, t probeTask) {
			defer wg.Done()
			hp := hopProbe{Node: t.name, Target: t.target}
			ack, err := s.Hub.SendProbe(t.nodeID, t.target)
			if err != nil {
				hp.Error = err.Error()
			} else if !ack.OK {
				hp.Error = ack.Error
			} else {
				hp.Latency = ack.Latency
			}
			results[i] = hp
		}(i, t)
	}
	wg.Wait()

	total := 0
	allOK := true
	for i := range results {
		if results[i].Error != "" {
			allOK = false
		} else {
			total += results[i].Latency
		}
	}
	json.NewEncoder(w).Encode(probeResult{OK: allOK, Latency: total, Hops: results})
}

// localTLSProbe runs the same TLS dest checks from the panel process (admin-only path).
func localTLSProbe(target, serverName string) wsproto.ProbeAck {
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		host = target
		port = "443"
		target = net.JoinHostPort(host, port)
	}
	_ = port
	sni := serverName
	if sni == "" {
		sni = host
	}
	dialer := &net.Dialer{Timeout: probeTimeout}
	start := time.Now()
	raw, err := dialer.Dial("tcp", target)
	if err != nil {
		return wsproto.ProbeAck{Error: err.Error(), Score: "fail", Summary: "TCP 不通: " + err.Error()}
	}
	cfg := &tls.Config{
		ServerName:         sni,
		InsecureSkipVerify: true,
		NextProtos:         []string{"h2", "http/1.1"},
		MinVersion:         tls.VersionTLS12,
	}
	tlsConn := tls.Client(raw, cfg)
	_ = tlsConn.SetDeadline(time.Now().Add(probeTimeout))
	err = tlsConn.Handshake()
	elapsed := time.Since(start)
	if err != nil {
		_ = raw.Close()
		return wsproto.ProbeAck{Error: err.Error(), Latency: int(elapsed.Milliseconds()), Score: "fail", Summary: "TLS 握手失败: " + err.Error()}
	}
	st := tlsConn.ConnectionState()
	_ = tlsConn.Close()
	ver := "TLS?"
	switch st.Version {
	case tls.VersionTLS13:
		ver = "TLS1.3"
	case tls.VersionTLS12:
		ver = "TLS1.2"
	case tls.VersionTLS11:
		ver = "TLS1.1"
	case tls.VersionTLS10:
		ver = "TLS1.0"
	}
	alpn := st.NegotiatedProtocol
	var certCN string
	var certDNS []string
	var notAfter int64
	sniMatch := false
	if len(st.PeerCertificates) > 0 {
		leaf := st.PeerCertificates[0]
		certCN = leaf.Subject.CommonName
		certDNS = append([]string{}, leaf.DNSNames...)
		notAfter = leaf.NotAfter.Unix()
		if sni != "" {
			if err := leaf.VerifyHostname(sni); err == nil {
				sniMatch = true
			}
		}
	}
	tls13 := st.Version == tls.VersionTLS13
	h2 := alpn == "h2"
	score, summary := "poor", ver
	if tls13 && h2 {
		score, summary = "good", "TLS1.3 + h2 · 适合 REALITY dest"
	} else if tls13 {
		score, summary = "ok", "TLS1.3 · 无 h2（可用但次优）"
	} else if st.Version >= tls.VersionTLS12 {
		score, summary = "poor", ver+" · 建议换支持 TLS1.3 的 dest"
	} else {
		score, summary = "fail", ver+" · 过旧"
	}
	return wsproto.ProbeAck{
		OK: true, Latency: int(elapsed.Milliseconds()),
		TLSVersion: ver, ALPN: alpn, Cipher: tls.CipherSuiteName(st.CipherSuite),
		CertCN: certCN, CertDNS: certDNS, CertNotAfter: notAfter,
		SNIMatch: sniMatch, TLS13: tls13, H2: h2, Score: score, Summary: summary,
	}
}
