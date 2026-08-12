package server

import (
	"database/sql"
	"fmt"
	"log"
	"net"
	"net/url"
	"strconv"
	"strings"

	"nft/internal/db"
	"nft/internal/landing"
	"nft/internal/proxysvc"
	"nft/internal/resolver"
)

// safeGo runs fn in a goroutine with panic recovery, so an unexpected panic
// in a background task (redispatch, hub, backup, etc.) logs instead of
// crashing the whole process.
func safeGo(fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("goroutine panic: %v", r)
			}
		}()
		fn()
	}()
}

// ruleView is the per-rule row the list/detail API renders.
type ruleView struct {
	Rule        *db.Rule
	Entry       string
	EntryV6     string
	Exit        string
	EntryNodeID int64
	EntryMode   string
	ExitMode    string
	OwnerName   string
}

func (s *Server) buildRuleView(r *db.Rule) ruleView {
	hops, _ := db.ListRuleHops(s.DB, r.ID)
	exit := net.JoinHostPort(r.ExitHost, strconv.Itoa(r.ExitPort))
	// Never leak SOCKS credentials in list/detail JSON — redact userinfo.
	if r.ExitURI != "" {
		cp := *r
		cp.ExitURI = redactedExitURI(r.ExitURI)
		r = &cp
	}
	if r.ExitType == "" {
		cp := *r
		cp.ExitType = "direct"
		r = &cp
	}
	entry, entryV6 := "—", ""
	var entryNodeID int64
	entryMode, exitMode := "", ""
	if len(hops) > 0 {
		entryMode = hops[0].Mode
		exitMode = hops[len(hops)-1].Mode
	}
	if len(hops) > 0 && r.EntryListenPort > 0 {
		entryNodeID = hops[0].NodeID
		if n, err := db.GetNode(s.DB, hops[0].NodeID); err == nil && n.RelayHost != "" {
			// EntryAddresses returns "" for a family whose relay address the
			// node no longer carries; keep the "—" placeholder instead of
			// rendering an empty host.
			if e, e6 := db.EntryAddresses(r.EntryFamily, n.RelayHost, n.RelayHostV6, r.EntryListenPort); e != "" {
				entry, entryV6 = e, e6
			}
		}
	}
	return ruleView{Rule: r, Entry: entry, EntryV6: entryV6, Exit: exit, EntryNodeID: entryNodeID, EntryMode: entryMode, ExitMode: exitMode}
}

// ruleListItem is the JSON shape for rule-list endpoints. The embedded *db.Rule
// promotes the rule's own fields (id, node_id, name, proto, ...) to the top
// level so React list rows can read them flat alongside the computed view
// fields. A wrapped {"rule":{...}} shape would leave r.id undefined in the UI.
type ruleListItem struct {
	*db.Rule
	OwnerName string `json:"owner_name"`
	Entry     string `json:"entry"`
	// EntryV6 is the rule's secondary entry address, populated only when
	// entry_family is "both"; computed from the entry node's current relay
	// addresses in buildRuleView.
	EntryV6     string `json:"entry_v6,omitempty"`
	Exit        string `json:"exit"`
	EntryNodeID int64  `json:"entry_node_id"`
	// EntryMode is the first hop's forwarding mode. ExitMode is the last
	// hop's — the exit segment the rule owns — and prefills the edit form's
	// kernel/userspace picker; on single-node rules the two coincide.
	EntryMode string `json:"entry_mode"`
	ExitMode  string `json:"exit_mode"`
	// ExitKind is "landing" when the exit host:port matches one of the owner's
	// admin-assigned landing nodes, else "custom". LandingURI is the original
	// (direct) proxy URI; RelayURI is that URI with its host:port rewritten to
	// the rule's entry endpoint, so a client dials the relay instead of the
	// landing directly. RelayURI is populated only where the copy action is
	// offered (detail and the user's own list). Matches against the user's own
	// browser-local URIs happen client-side, not here.
		ExitKind        string  `json:"exit_kind"`
		LandingName     string  `json:"landing_name,omitempty"`
		LandingProtocol string  `json:"landing_protocol,omitempty"`
		LandingURI      string  `json:"landing_uri,omitempty"`
		// LandingExpiresAt is the owner's assigned landing-exit expiry
		// (user_landing_exits.expires_at) for this rule's exit host:port.
		// Used by clipboard rename (`用户名-8月5日`); not node_repo warehouse expiry.
		LandingExpiresAt int64   `json:"landing_expires_at,omitempty"`
		RelayURI         string  `json:"relay_uri,omitempty"`
		RateMultiplier   float64 `json:"rate_multiplier"`
		BillingRate      float64 `json:"billing_rate"`
	// Chain is the flattened physical path (entry → the hop that dials the
	// target, target excluded), with composite segments already expanded into
	// their member nodes. Sourced from rule_hops so it reflects what is actually
	// deployed, not a composite's current definition. Empty until the rule's
	// first regeneration.
	Chain []chainNode `json:"chain,omitempty"`
}

// chainNode is one physical hop in a rule's flattened chain, resolved to its
// node name and type for display.
type chainNode struct {
	NodeID   int64  `json:"node_id"`
	Name     string `json:"name"`
	NodeType string `json:"node_type"`
}

// nodeHopView adds the resolved child node name to a composite node's hop so
// the UI shows names instead of bare ids. The embedded *db.NodeHop promotes its
// own fields (node_id, position, hop_node_id, mode) to the top level.
type nodeHopView struct {
	*db.NodeHop
	NodeName string `json:"node_name"`
}

func (s *Server) buildRuleListItem(r *db.Rule, ownerName string) ruleListItem {
	v := s.buildRuleView(r)
	return ruleListItem{Rule: r, OwnerName: ownerName, Entry: v.Entry, EntryV6: v.EntryV6, Exit: v.Exit, EntryNodeID: v.EntryNodeID, EntryMode: v.EntryMode, ExitMode: v.ExitMode}
}

// fillRuleChains attaches the flattened physical chain to each item, resolving
// every hop's node id to its display name/type via nodesByID. A hop whose node
// can't be resolved is dropped rather than shown as a bare id. nodesByID should
// span all nodes (composite children included), not just the caller's granted
// subset, so a composite's members still resolve.
func (s *Server) fillRuleChains(items []ruleListItem, nodesByID map[int64]*db.Node) {
	if len(items) == 0 {
		return
	}
	ids := make([]int64, len(items))
	for i := range items {
		ids[i] = items[i].ID
	}
	chains, err := db.RuleChainNodeIDs(s.DB, ids)
	if err != nil {
		return
	}
	for i := range items {
		hopIDs := chains[items[i].ID]
		chain := make([]chainNode, 0, len(hopIDs))
		for _, nid := range hopIDs {
			if n := nodesByID[nid]; n != nil {
				chain = append(chain, chainNode{NodeID: nid, Name: n.Name, NodeType: n.NodeType})
			}
		}
		items[i].Chain = chain
	}
}

// classifyExit fills the exit-kind / proxy-URI fields. idx maps "host:port" to
// the owner's landing nodes; withURI controls whether the copyable relay URI is
// computed (skipped for the admin list, which only shows the kind badge).
//
// Client-facing link (copy/QR) must match the data plane:
//
//  1. Protocol-entry (proxy_service_id + supported entry protocol, single-hop)
//     → entry port is a real inbound of that protocol (3x-ui style);
//     relay_uri is the proxy-service share rewritten to entry host:port.
//     Egress: exit_type=socks5 → open SOCKS via exit_uri; direct → freedom
//     redirect to exit host:port. Wins over landing-index match on exit.
//  2. Landing exit match (direct L4 tunnel to a landing node) → rewrite that
//     landing's share URI to the rule entry host:port.
//  3. Plain exit_type=socks5 without protocol entry → L4 + last-hop ExitProxy;
//     rewrite exit_uri authority to entry (socks5 client link).
//  4. proxy_service_id also supplies display name for 线路 labels.
//
// ExitURI is always redacted in the JSON response after RelayURI is built from
// the unredacted secret (buildRuleListItem embeds the raw db.Rule).
func (it *ruleListItem) classifyExit(idx map[string]landing.Node, withURI bool) {
	it.classifyExitWithShare(idx, withURI, "", "", "")
}

// classifyExitWithShare is classifyExit plus optional entry proxy-service
// metadata. Protocol-entry uses the entry share on the entry port.
func (it *ruleListItem) classifyExitWithShare(idx map[string]landing.Node, withURI bool, shareProto, shareURI, shareName string) {
	it.ExitKind = "custom"
	relayHost, relayPort, entryOK := splitEntry(it.Entry)
	sp := strings.ToLower(strings.TrimSpace(shareProto))
	// Normalize ss alias for tags.
	if sp == "ss" {
		sp = "shadowsocks"
	}
	if sp == "socks" {
		sp = "socks5"
	}
	if sp == "naiveproxy" {
		sp = "naive"
	}
	protocolEntry := it.Rule != nil && ruleUsesProtocolEntry(it.Rule) &&
		protocolEntrySupported(sp) &&
		strings.TrimSpace(shareURI) != ""

	// 1) Protocol entry first — do not let landing-index on CONNECT/SK5 host
	// steal the client-facing protocol tag/URI.
	if protocolEntry {
		it.LandingProtocol = sp
		if shareName != "" {
			it.LandingName = shareName
		}
		if withURI && entryOK {
			if u, err := landing.RewriteEndpoint(shareURI, relayHost, relayPort); err == nil {
				it.RelayURI = u
			}
		}
	} else if node, ok := idx[it.Exit]; ok {
		it.ExitKind = "landing"
		it.LandingName = node.Name
		it.LandingProtocol = node.Protocol
		it.LandingURI = node.URI
		it.LandingExpiresAt = node.ExpiresAt
		if withURI && entryOK {
			if u, err := landing.RewriteEndpoint(node.URI, relayHost, relayPort); err == nil {
				it.RelayURI = u
			}
		}
	} else if it.Rule != nil && it.ExitType == "socks5" {
		// L4 entry + last-hop ExitProxy CONNECT.
		it.LandingProtocol = "socks5"
		if shareName != "" && it.LandingName == "" {
			it.LandingName = shareName
		}
		src := strings.TrimSpace(it.ExitURI)
		if withURI && entryOK && src != "" && !isRedactedExitURI(src) {
			if u, err := landing.RewriteEndpoint(src, relayHost, relayPort); err == nil {
				it.RelayURI = u
			}
		}
	} else {
		// Direct custom exit: optional display hint from 代理 tab pick.
		if p := strings.TrimSpace(sp); p != "" {
			it.LandingProtocol = p
		}
		if shareName != "" && it.LandingName == "" {
			it.LandingName = shareName
		}
	}
	// Never leak SOCKS credentials in list/detail JSON.
	if it.Rule != nil && it.ExitURI != "" {
		cp := *it.Rule
		cp.ExitURI = redactedExitURI(it.ExitURI)
		it.Rule = &cp
	}
}

// classifyRuleExit runs classifyExit and attaches proxy-service share metadata
// when the rule remembers a proxy_service_id (display + protocol-entry relay).
func (s *Server) classifyRuleExit(it *ruleListItem, idx map[string]landing.Node, withURI bool) {
	if it == nil {
		return
	}
	var shareProto, shareURI, shareName string
	if it.Rule != nil && it.ProxyServiceID > 0 && it.NodeID > 0 {
		if p, u, n, ok := db.LookupProxyServiceShare(s.DB, it.ProxyServiceID, it.NodeID); ok || p != "" || n != "" {
			shareProto, shareURI, shareName = p, u, n
			_ = ok
		}
		// Prefer building share from service config when instance URI missing
		// (protocol-entry rules do not require a published instance on the node).
		if shareURI == "" || shareProto == "" {
			if svc, err := db.GetProxyService(s.DB, it.ProxyServiceID); err == nil && svc != nil {
				if shareProto == "" {
					shareProto = svc.Protocol
				}
				if shareName == "" {
					shareName = svc.Name
				}
				if shareURI == "" && withURI {
					host, port, eok := splitEntry(it.Entry)
					if !eok {
						if n, err := db.GetNode(s.DB, it.NodeID); err == nil && n != nil {
							host = firstNonEmpty(n.RelayHost, n.Address)
							if h, _, e := splitHostPortLoose(host); e == nil && h != "" {
								host = h
							}
							port = it.EntryListenPort
							eok = host != "" && port > 0
						}
					}
					if eok {
						cfg := svc.ConfigJSON
						if fixed, err := proxysvc.EnsureSecrets(svc.Protocol, cfg); err == nil {
							cfg = fixed
						}
						if u, err := proxysvc.BuildShareURI(svc.Protocol, svc.Name, host, port, cfg); err == nil {
							shareURI = u
						}
					}
				}
			}
		}
	}
	it.classifyExitWithShare(idx, withURI, shareProto, shareURI, shareName)
}

// splitEntry parses a "host:port" entry string; entry is "—" before the rule's
// first regeneration, which fails the split and reports ok=false.
func splitEntry(entry string) (host string, port int, ok bool) {
	h, p, err := net.SplitHostPort(entry)
	if err != nil {
		return "", 0, false
	}
	pp, err := strconv.Atoi(p)
	if err != nil {
		return "", 0, false
	}
	return h, pp, true
}

// validRuleProto reports whether proto is an accepted forward protocol. tcp+udp
// is accepted: the data plane splits it into a udp kernel DNAT plus a tcp
// userspace relay when the hop runs in userspace mode (see forward.Partition).
func validRuleProto(proto string) bool {
	switch proto {
	case "tcp", "udp", "tcp+udp":
		return true
	default:
		return false
	}
}

// normalizeEntryFamily validates a client-supplied entry_family. Empty passes
// through as empty so callers can tell "not sent" apart from an explicit
// value: create defaults it to v4, while edit keeps the rule's stored family —
// a client predating the field must not silently downgrade a v6/both rule.
func normalizeEntryFamily(v string) (string, error) {
	v = strings.ToLower(strings.TrimSpace(v))
	switch v {
	case "", "v4", "v6", "both":
		return v, nil
	default:
		return "", fmt.Errorf("entry_family 须为 v4、v6 或 both")
	}
}

// parsedExit is the resolved exit of a rule: direct L4 host:port, or SOCKS5
// proxy URI plus CONNECT target host:port.
type parsedExit struct {
	Type string // "direct" | "socks5"
	Host string // CONNECT target (and direct dial host)
	Port int
	URI  string // socks5://... when Type==socks5; empty otherwise
}

func parseExit(raw string) (string, int, error) {
	pe, err := parseExitFull(raw, "", "")
	if err != nil {
		return "", 0, err
	}
	return pe.Host, pe.Port, nil
}

// parseExitFull resolves exit fields from the create/update body.
//
// Direct mode (default):
//   - exit = host:port (or [ipv6]:port)
//
// SOCKS5 mode (chain rules):
//   - exit_type=socks5 + exit_uri=socks5://user:pass@proxy:port + exit=target host:port
//   - or a single socks5:// URI in exit when exit is the proxy and target is
//     supplied via exit_target (less common; prefer explicit fields)
//
// exitType/exitURI come from the JSON body; empty type defaults to direct.
func parseExitFull(exit, exitType, exitURI string) (parsedExit, error) {
	exit = strings.TrimSpace(exit)
	exitType = strings.ToLower(strings.TrimSpace(exitType))
	exitURI = strings.TrimSpace(exitURI)

	// Infer socks5 when the exit string itself is a socks URI and no type was set.
	if exitType == "" && (strings.HasPrefix(strings.ToLower(exit), "socks5://") || strings.HasPrefix(strings.ToLower(exit), "socks://")) {
		return parsedExit{}, fmt.Errorf("SOCKS5 出口请分别填写：exit 为 CONNECT 目标 host:port，exit_uri 为 socks5://user:pass@代理:端口，exit_type=socks5")
	}
	if exitType == "" {
		exitType = "direct"
	}
	switch exitType {
	case "direct":
		host, port, err := parseHostPort(exit)
		if err != nil {
			return parsedExit{}, err
		}
		// Optional landing share (ss:// / vless:// …) for protocol-entry egress.
		// Bare host:port alone cannot speak SS/VLESS — credentials live in exit_uri.
		pe := parsedExit{Type: "direct", Host: host, Port: port}
		if exitURI != "" && proxysvc.IsProxyShareURI(exitURI) {
			pe.URI = strings.TrimSpace(exitURI)
		}
		return pe, nil
	case "socks5", "socks":
		uri := exitURI
		if uri == "" {
			return parsedExit{}, fmt.Errorf("SOCKS5 出口需提供 exit_uri（socks5://user:pass@host:port）")
		}
		if err := validateSocks5URI(uri); err != nil {
			return parsedExit{}, err
		}
		host, port, err := parseHostPort(exit)
		if err != nil {
			return parsedExit{}, fmt.Errorf("SOCKS5 CONNECT 目标：%w", err)
		}
		return parsedExit{Type: "socks5", Host: host, Port: port, URI: normalizeSocks5URI(uri)}, nil
	default:
		return parsedExit{}, fmt.Errorf("exit_type 须为 direct 或 socks5")
	}
}

func parseHostPort(raw string) (string, int, error) {
	raw = strings.TrimSpace(raw)
	host, portStr, err := net.SplitHostPort(raw)
	if err != nil {
		if looksLikeBareIPv6(raw) {
			return "", 0, fmt.Errorf("IPv6 地址需要用方括号包裹，例如 [::1]:1080")
		}
		return "", 0, fmt.Errorf("出口需为 host:port 形式")
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("出口端口非法")
	}
	if host == "" {
		return "", 0, fmt.Errorf("出口地址不能为空")
	}
	if net.ParseIP(host) == nil && !resolver.PlausibleHostname(host) {
		return "", 0, fmt.Errorf("出口地址非法：%q 不是合法 IP 或域名", host)
	}
	return host, port, nil
}

// validateSocks5URI accepts socks5:// and socks:// with host:port authority.
func validateSocks5URI(raw string) error {
	raw = strings.TrimSpace(raw)
	lower := strings.ToLower(raw)
	if !strings.HasPrefix(lower, "socks5://") && !strings.HasPrefix(lower, "socks://") {
		return fmt.Errorf("exit_uri 须为 socks5:// 或 socks:// 形式")
	}
	// net/url handles userinfo; SplitHostPort needs host:port after authority.
	u, err := parseSocksURL(raw)
	if err != nil {
		return err
	}
	host := u.Hostname()
	portStr := u.Port()
	if host == "" || portStr == "" {
		return fmt.Errorf("exit_uri 须包含 host:port")
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("exit_uri 端口非法")
	}
	if net.ParseIP(host) == nil && !resolver.PlausibleHostname(host) {
		return fmt.Errorf("exit_uri 地址非法：%q", host)
	}
	return nil
}

func normalizeSocks5URI(raw string) string {
	raw = strings.TrimSpace(raw)
	// Prefer socks5:// scheme on the wire for the agent dialer.
	if strings.HasPrefix(strings.ToLower(raw), "socks://") {
		return "socks5://" + raw[len("socks://"):]
	}
	return raw
}

// redactedExitURI masks secrets in exit_uri for API responses.
// socks5:// → mask password; ss:// / vless:// → keep host:port shape, drop credentials.
func redactedExitURI(uri string) string {
	uri = strings.TrimSpace(uri)
	if uri == "" {
		return ""
	}
	lower := strings.ToLower(uri)
	if strings.HasPrefix(lower, "socks5://") || strings.HasPrefix(lower, "socks://") {
		u, err := parseSocksURL(uri)
		if err != nil {
			return "socks5://***"
		}
		hostport := u.Host
		if u.User != nil {
			user := u.User.Username()
			if user == "" {
				user = "***"
			}
			return "socks5://" + user + ":***@" + hostport
		}
		return "socks5://" + hostport
	}
	// Share links: do not leak method:password / uuid in list JSON.
	if proxysvc.IsProxyShareURI(uri) {
		// Keep scheme + host:port only when parseable.
		if i := strings.Index(uri, "://"); i > 0 {
			scheme := uri[:i]
			rest := uri[i+3:]
			if h := strings.Index(rest, "#"); h >= 0 {
				rest = rest[:h]
			}
			if q := strings.Index(rest, "?"); q >= 0 {
				// keep query for vless (non-secret params) but strip userinfo
				// simpler: authority only
			}
			if at := strings.LastIndex(rest, "@"); at >= 0 {
				rest = rest[at+1:]
			}
			// rest may still have ?query for vless without user in authority after strip
			if q := strings.Index(rest, "?"); q >= 0 {
				// drop query secrets-ish; show host:port only
				rest = rest[:q]
			}
			return scheme + "://***@" + rest
		}
		return "***"
	}
	return "***"
}

// isRedactedExitURI reports whether the client re-submitted a list/detail
// redacted socks URI (password replaced with ***). Such values must not be
// persisted; the stored secret should be kept instead.
func isRedactedExitURI(uri string) bool {
	uri = strings.TrimSpace(uri)
	if uri == "" {
		return false
	}
	if strings.Contains(uri, ":***@") || strings.Contains(uri, "://***@") || strings.Contains(uri, "://***") {
		return true
	}
	u, err := parseSocksURL(uri)
	if err != nil {
		return false
	}
	if u.User == nil {
		return false
	}
	pass, ok := u.User.Password()
	return ok && pass == "***"
}

// applyExitConstraints enforces SK5 product rules: TCP-only + userspace exit hop.
// Returns adjusted exitMode (forced userspace for socks5) or an error.
func applyExitConstraints(exitType, proto, exitMode string) (string, error) {
	if exitType != "socks5" {
		return exitMode, nil
	}
	if proto != "tcp" {
		return "", fmt.Errorf("SOCKS5 出口仅支持 TCP 协议")
	}
	return "userspace", nil
}

func parseSocksURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("exit_uri 格式错误：%v", err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "socks5" && scheme != "socks" {
		return nil, fmt.Errorf("exit_uri 须为 socks5:// 或 socks:// 形式")
	}
	return u, nil
}

// looksLikeBareIPv6 reports whether raw is very likely an IPv6 literal
// missing the brackets host:port syntax requires: multiple colons with no
// leading '[' isn't ambiguous with any valid IPv4/hostname:port form.
func looksLikeBareIPv6(raw string) bool {
	return !strings.HasPrefix(raw, "[") && strings.Count(raw, ":") >= 2
}

func validateCIDRList(s string) error {
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if _, _, err := net.ParseCIDR(part); err != nil {
			return fmt.Errorf("%q: %v", part, err)
		}
	}
	return nil
}

func cidrAllowsAll(list string) bool {
	list = strings.TrimSpace(list)
	if list == "" {
		return true
	}
	for _, part := range strings.Split(list, ",") {
		if strings.TrimSpace(part) == "0.0.0.0/0" {
			return true
		}
	}
	return false
}

func targetIPInCIDR(ip net.IP, list string) bool {
	if cidrAllowsAll(list) {
		return true
	}
	for _, part := range strings.Split(list, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		_, ipnet, err := net.ParseCIDR(part)
		if err != nil {
			continue
		}
		if ipnet.Contains(ip) {
			return true
		}
	}
	return false
}

func nullInt64(v int64) sql.NullInt64 { return sql.NullInt64{Int64: v, Valid: true} }

// viasOf dereferences the optional middle-layer path from a request body: a
// nil pointer (field absent) yields a nil slice so callers keep the stored
// path, while a non-nil pointer — including an explicit empty array — yields
// its value so the client can deliberately clear the layers.
func viasOf(p *[]int64) []int64 {
	if p == nil {
		return nil
	}
	return *p
}

// checkUserRuleQuota verifies a user hasn't exceeded their global max_forwards
// limit or per-node grant limits.
func (s *Server) checkUserRuleQuota(u *db.User, hopCount int, existingRuleHops int) error {
	total, _ := db.CountRulesForUser(s.DB, u.ID)
	if (total-existingRuleHops)+hopCount > u.MaxForwards {
		return fmt.Errorf("超出用户最大转发数（%d）", u.MaxForwards)
	}
	return nil
}

// regenerateRuleByID loads a rule and its hops, then calls RegenerateRule
// inside a transaction. Returns the set of affected node IDs.
func (s *Server) regenerateRuleByID(ruleID int64) ([]int64, error) {
	r, err := db.GetRule(s.DB, ruleID)
	if err != nil {
		return nil, err
	}
	hops, err := db.ListRuleHops(s.DB, ruleID)
	if err != nil {
		return nil, err
	}
	if len(hops) == 0 {
		return nil, nil
	}
	inputs := make([]db.HopInput, len(hops))
	for i, h := range hops {
		inputs[i] = db.HopInput{NodeID: h.NodeID, Mode: h.Mode, ViaNodeID: h.ViaNodeID}
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	_, _, affected, err := db.RegenerateRule(tx, r, inputs, nil)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return affected, nil
}

func (s *Server) listUsersBrief() []map[string]any {
	all, err := db.ListUsers(s.DB)
	if err != nil {
		return nil
	}
	out := make([]map[string]any, 0, len(all))
	for _, u := range all {
		out = append(out, map[string]any{"id": u.ID, "username": u.Username})
	}
	return out
}
