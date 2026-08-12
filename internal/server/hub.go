package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"nft/internal/db"
	"nft/internal/nft"
	"nft/internal/wsproto"
)

const (
	hubWriteTimeout = 10 * time.Second
	hubReadTimeout  = 30 * time.Second
	applyAckTimeout = 60 * time.Second
	// hubMaxReadBytes bounds a single WebSocket frame. Counters samples and
	// rule command payloads are small; anything larger is malformed or malicious.
	hubMaxReadBytes = 4 << 20
	// hubWriteTimeoutMax caps size-based write deadlines (see writeTimeoutFor).
	hubWriteTimeoutMax = 5 * time.Minute
)

// writeTimeoutFor scales the per-frame write deadline with payload size so a
// rare large frame (legacy inline upgrade, big apply payload) is not cut off
// by the default 10s budget on slow reverse links.
func writeTimeoutFor(size int) time.Duration {
	const perMB = 3 * time.Second
	d := hubWriteTimeout + time.Duration(size/(1<<20))*perMB
	if d > hubWriteTimeoutMax {
		return hubWriteTimeoutMax
	}
	return d
}

type Hub struct {
	DB *sql.DB

	// OnTrafficUpdate, when set, is invoked once per (user, node) pair whose
	// usage was advanced by a counters batch. The Hub stays a pure transport:
	// it knows how to accumulate bytes but delegates quota policy (and the
	// re-dispatch it may trigger) to the owner that wires this callback.
	OnTrafficUpdate func(userID int64, nodeID int64)

	// Redispatch re-pushes kernel state to a set of nodes after the hub
	// mutates rule state on their behalf. Keeps the hub transport-only.
	Redispatch func(nodeIDs []int64)

	mu         sync.RWMutex
	conns      map[int64]*agentConn
	speedCache *speedCache

	// redirectAck tracks last panel_redirect outcome per node (migrate UI).
	redirectMu  sync.Mutex
	redirectAck map[int64]redirectAckEntry
}

type redirectAckEntry struct {
	OK        bool
	Error     string
	At        int64
	PanelURL  string
	Attempted bool
}

func NewHub(d *sql.DB) *Hub {
	return &Hub{
		DB: d, conns: make(map[int64]*agentConn), speedCache: newSpeedCache(),
		redirectAck: make(map[int64]redirectAckEntry),
	}
}

// OnlineNodeIDs returns currently connected agent node IDs.
func (h *Hub) OnlineNodeIDs() []int64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]int64, 0, len(h.conns))
	for id := range h.conns {
		out = append(out, id)
	}
	return out
}

func (h *Hub) noteRedirectAck(nodeID int64, url string, ok bool, errMsg string) {
	h.redirectMu.Lock()
	defer h.redirectMu.Unlock()
	if h.redirectAck == nil {
		h.redirectAck = make(map[int64]redirectAckEntry)
	}
	h.redirectAck[nodeID] = redirectAckEntry{
		OK: ok, Error: errMsg, At: time.Now().Unix(), PanelURL: url, Attempted: true,
	}
}

// RedirectAcks returns a copy of redirect ack state for the migrate status API.
func (h *Hub) RedirectAcks() map[int64]redirectAckEntry {
	h.redirectMu.Lock()
	defer h.redirectMu.Unlock()
	out := make(map[int64]redirectAckEntry, len(h.redirectAck))
	for k, v := range h.redirectAck {
		out[k] = v
	}
	return out
}

type agentConn struct {
	nodeID  int64
	ws      *websocket.Conn
	writeCh chan []byte
	closed  chan struct{}

	// closeOnce guards closed so the multiple close paths (a displaced
	// conn in registerConn, unregisterConn on disconnect, and Hub.Close
	// on shutdown) can race without double-closing the channel.
	closeOnce sync.Once

	pendMu  sync.Mutex
	pending map[string]chan json.RawMessage

	idSeq atomic.Uint64
}

func (a *agentConn) nextID() string {
	return strconv.FormatUint(a.idSeq.Add(1), 36)
}

// signalClose closes ac.closed exactly once, signalling the reader and
// writer loops (and any pending SendApplyRuleset) to stop.
func (a *agentConn) signalClose() {
	a.closeOnce.Do(func() { close(a.closed) })
}

func (h *Hub) IsOnline(nodeID int64) bool {
	h.mu.RLock()
	_, ok := h.conns[nodeID]
	h.mu.RUnlock()
	return ok
}

// ServeWS handles the /v1/agents WS endpoint. Upgrades the request,
// reads the mandatory hello frame, validates the bearer token against
// nodes.secret, registers the conn, and loops on reads dispatching by
// message type.
func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request) {
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true, // we authenticate via bearer in hello
	})
	if err != nil {
		log.Printf("hub: accept: %v", err)
		return
	}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	helloEnv, err := readEnvelope(ctx, ws, hubReadTimeout)
	if err != nil || helloEnv.Type != wsproto.TypeHello {
		writeError(ctx, ws, "protocol", "expected hello as first frame")
		ws.Close(websocket.StatusPolicyViolation, "no hello")
		return
	}
	var hello wsproto.Hello
	if err := json.Unmarshal(helloEnv.Payload, &hello); err != nil {
		writeError(ctx, ws, "protocol", "malformed hello payload")
		ws.Close(websocket.StatusPolicyViolation, "bad hello")
		return
	}

	node, err := lookupNodeBySecret(h.DB, hello.NodeToken)
	if err != nil || node == nil {
		ack, _ := json.Marshal(wsproto.HelloAck{Error: "unknown or revoked token"})
		writeEnvelope(ctx, ws, wsproto.Envelope{Type: wsproto.TypeHelloAck, ID: helloEnv.ID, Payload: ack})
		ws.Close(websocket.StatusPolicyViolation, "bad token")
		return
	}

	// Register before hello_ack so both sides can rely on the invariant
	// "hello_ack visible => conn is in the hub map".
	ac := &agentConn{
		nodeID:  node.ID,
		ws:      ws,
		writeCh: make(chan []byte, 16),
		closed:  make(chan struct{}),
		pending: make(map[string]chan json.RawMessage),
	}
	h.registerConn(ac)
	defer h.unregisterConn(ac)

	poolSize := 4
	if psStr, err := db.GetSetting(h.DB, "pool_size"); err == nil {
		if n, err := strconv.Atoi(psStr); err == nil && n >= 0 {
			poolSize = n
		}
	}
	ackPayload, _ := json.Marshal(wsproto.HelloAck{NodeID: node.ID, Name: node.Name, PoolSize: poolSize})
	if err := writeEnvelope(ctx, ws, wsproto.Envelope{Type: wsproto.TypeHelloAck, ID: helloEnv.ID, Payload: ackPayload}); err != nil {
		ws.Close(websocket.StatusInternalError, "ack write failed")
		return
	}

	observedIP := extractIP(r)
	connectIP := resolveNodeConnectIP(h.DB, observedIP, hello.ProbedV4, hello.ProbedV6)
	if err := db.MarkNodeOnline(h.DB, node.ID, hello.AgentVersion, hello.AgentSHA, connectIP, hello.Arch); err != nil {
		log.Printf("hub: MarkNodeOnline: %v", err)
	}
	applyDeclaredRelayHosts(h.DB, node, hello.DeclaredRelayHost, hello.DeclaredRelayHostV6)
	// Use connectIP (already de-noised) for seeding; still pass probes so the
	// other address family can be filled when this WS only covers one family.
	fillNodeRelayHosts(h.DB, node, connectIP, observedIP, hello.ProbedV4, hello.ProbedV6, hello.DeclaredRelayHost, hello.DeclaredRelayHostV6)
	// Port range is panel-owned: admin edits via /nodes/{id}/port-range must stick.
	// Agent still reports hello.PortRange for diagnostics, but we must not overwrite
	// the DB with the unit file's --port-range on every reconnect (that made UI
	// saves appear to "revert" after the next hello).
	if len(hello.Cores) > 0 {
		if b, err := json.Marshal(hello.Cores); err == nil {
			_ = db.SetNodeCoresJSON(h.DB, node.ID, string(b))
		}
	}

	// A node may have missed rule changes while it was offline. Reconcile now so
	// the kernel state converges on reconnect instead of drifting until the next
	// mutation. The rev check keeps this a no-op when the node is already in sync.
	h.reconcileOnConnect(node.ID, hello.LastAppliedRev)
	// If an admin started a panel host migration, late-connecting agents still
	// on the old panel get redirected once they hello here.
	h.maybePushPendingRedirect(node.ID)

	go h.writerLoop(ac)
	h.readerLoop(ctx, ac)
}

// reconcileOnConnect re-pushes the node's ruleset after a (re)connect unless the
// agent's reported last-applied rev already matches what the panel would send.
// Redispatch runs off-goroutine, so it schedules the apply without blocking the
// hello path; the apply_ack is handled once readerLoop starts.
func (h *Hub) reconcileOnConnect(nodeID int64, lastAppliedRev string) {
	if h.Redispatch == nil {
		return
	}
	ruleHops, err := db.ActiveRuleHopsForPush(h.DB, nodeID)
	if err != nil {
		// Can't compute the target rev — force a resync rather than risk drift.
		go h.Redispatch([]int64{nodeID})
		return
	}
	if lastAppliedRev != "" && computeRev(buildRules(h.DB, ruleHops)) == lastAppliedRev {
		return
	}
	go h.Redispatch([]int64{nodeID})
}

func (h *Hub) registerConn(ac *agentConn) {
	h.mu.Lock()
	if old, ok := h.conns[ac.nodeID]; ok {
		old.signalClose()
		old.ws.Close(websocket.StatusGoingAway, "replaced by newer connection")
	}
	h.conns[ac.nodeID] = ac
	h.mu.Unlock()
}

func (h *Hub) unregisterConn(ac *agentConn) {
	h.mu.Lock()
	if cur, ok := h.conns[ac.nodeID]; ok && cur == ac {
		delete(h.conns, ac.nodeID)
	}
	h.mu.Unlock()
	ac.signalClose()
	_ = db.MarkNodeOffline(h.DB, ac.nodeID)
}

// Close gracefully shuts down every agent connection.
func (h *Hub) Close() {
	h.mu.Lock()
	conns := make([]*agentConn, 0, len(h.conns))
	for _, ac := range h.conns {
		conns = append(conns, ac)
	}
	h.conns = make(map[int64]*agentConn)
	h.mu.Unlock()

	for _, ac := range conns {
		ac.signalClose()
		_ = ac.ws.Close(websocket.StatusGoingAway, "panel shutting down")
	}
}

func (h *Hub) writerLoop(ac *agentConn) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("hub: writerLoop panic for node %d: %v", ac.nodeID, r)
			ac.signalClose()
		}
	}()
	for {
		select {
		case <-ac.closed:
			return
		case b := <-ac.writeCh:
			ctx, cancel := context.WithTimeout(context.Background(), writeTimeoutFor(len(b)))
			err := ac.ws.Write(ctx, websocket.MessageText, b)
			cancel()
			if err != nil {
				ac.ws.Close(websocket.StatusInternalError, "write error")
				return
			}
		}
	}
}

func (h *Hub) readerLoop(parent context.Context, ac *agentConn) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("hub: readerLoop panic for node %d: %v", ac.nodeID, r)
			ac.signalClose()
		}
	}()
	for {
		ctx, cancel := context.WithTimeout(parent, hubReadTimeout)
		_, b, err := ac.ws.Read(ctx)
		cancel()
		if err != nil {
			return
		}
		var env wsproto.Envelope
		if err := json.Unmarshal(b, &env); err != nil {
			log.Printf("hub: malformed envelope from node %d: %v", ac.nodeID, err)
			continue
		}
		switch env.Type {
		case wsproto.TypePing:
			pong, _ := json.Marshal(wsproto.Pong{TS: time.Now().UnixMilli()})
			ac.enqueueWrite(wsproto.Envelope{Type: wsproto.TypePong, ID: env.ID, Payload: pong})
			// Refresh last_seen on every ping so "假在线" stale-detection has a
			// live clock; agents ping ~10s so this is cheap and bounds drift.
			if err := db.TouchNodeLastSeen(h.DB, ac.nodeID); err != nil {
				log.Printf("hub: TouchNodeLastSeen node %d: %v", ac.nodeID, err)
			}
		case wsproto.TypeCounters:
			var co wsproto.Counters
			if err := json.Unmarshal(env.Payload, &co); err != nil {
				log.Printf("hub: node %d malformed counters: %v", ac.nodeID, err)
				// Explicit NACK when the agent asked for an ack (ID set) so it
				// retransmits instead of silently dropping the batch.
				if env.ID != "" {
					ackP, _ := json.Marshal(wsproto.CountersAck{OK: false, Error: "malformed payload"})
					ac.enqueueWrite(wsproto.Envelope{Type: wsproto.TypeCountersAck, ID: env.ID, Payload: ackP})
				}
				continue
			}
			ok, errMsg := h.applyCounters(ac.nodeID, co.Samples)
			if env.ID != "" {
				ack := wsproto.CountersAck{OK: ok}
				if !ok {
					ack.Error = errMsg
					if ack.Error == "" {
						ack.Error = "persist failed"
					}
				}
				ackP, _ := json.Marshal(ack)
				ac.enqueueWrite(wsproto.Envelope{Type: wsproto.TypeCountersAck, ID: env.ID, Payload: ackP})
			}
		case wsproto.TypeProxyCounters:
			var pc wsproto.ProxyCounters
			if err := json.Unmarshal(env.Payload, &pc); err != nil {
				log.Printf("hub: node %d malformed proxy_counters: %v", ac.nodeID, err)
				if env.ID != "" {
					ackP, _ := json.Marshal(wsproto.ProxyCountersAck{OK: false, Error: "malformed payload"})
					ac.enqueueWrite(wsproto.Envelope{Type: wsproto.TypeProxyCountersAck, ID: env.ID, Payload: ackP})
				}
				continue
			}
			ok, errMsg := h.applyProxyCounters(ac.nodeID, pc.Samples)
			if env.ID != "" {
				ack := wsproto.ProxyCountersAck{OK: ok}
				if !ok {
					ack.Error = errMsg
					if ack.Error == "" {
						ack.Error = "persist failed"
					}
				}
				ackP, _ := json.Marshal(ack)
				ac.enqueueWrite(wsproto.Envelope{Type: wsproto.TypeProxyCountersAck, ID: env.ID, Payload: ackP})
			}
		case wsproto.TypeRuleCreate:
			h.handleRuleCreate(ac, env)
		case wsproto.TypeRuleUpdate:
			h.handleRuleUpdate(ac, env)
		case wsproto.TypeMigrateRules:
			h.handleMigrateRules(ac, env)
		case wsproto.TypeRuleHopEdit:
			var e wsproto.RuleHopEdit
			if err := json.Unmarshal(env.Payload, &e); err != nil {
				sendRuleAckErr(ac, env.ID, "malformed payload")
				continue
			}
			entry, cerr := h.applyRuleHopEdit(ac.nodeID, e.RuleID, e.ListenPort, e.Mode, e.Comment)
			ack := wsproto.RuleCmdAck{OK: cerr == nil, Entry: entry}
			if cerr != nil {
				ack.Error = cerr.Error()
			}
			ackP, _ := json.Marshal(ack)
			ac.enqueueWrite(wsproto.Envelope{Type: wsproto.TypeRuleCmdAck, ID: env.ID, Payload: ackP})
		case wsproto.TypeRuleDelete:
			var dl wsproto.RuleDelete
			if err := json.Unmarshal(env.Payload, &dl); err != nil {
				sendRuleAckErr(ac, env.ID, "malformed payload")
				continue
			}
			cerr := h.applyRuleDelete(ac.nodeID, dl.RuleID)
			ack := wsproto.RuleCmdAck{OK: cerr == nil}
			if cerr != nil {
				ack.Error = cerr.Error()
			}
			ackP, _ := json.Marshal(ack)
			ac.enqueueWrite(wsproto.Envelope{Type: wsproto.TypeRuleCmdAck, ID: env.ID, Payload: ackP})
		case wsproto.TypeApplyAck, wsproto.TypeHelloAck, wsproto.TypeUpgradeAck, wsproto.TypeProbeAck, wsproto.TypePanelRedirectAck, wsproto.TypeProxyServiceApplyAck, wsproto.TypeProxyServiceStopAck, wsproto.TypeCoreInstallAck:
			ac.dispatchAck(env)
		default:
			log.Printf("hub: node %d unknown frame type %q", ac.nodeID, env.Type)
		}
	}
}

func (ac *agentConn) enqueueWrite(env wsproto.Envelope) {
	b, err := json.Marshal(env)
	if err != nil {
		return
	}
	select {
	case ac.writeCh <- b:
	case <-ac.closed:
	}
}

func (ac *agentConn) dispatchAck(env wsproto.Envelope) {
	ac.pendMu.Lock()
	ch, ok := ac.pending[env.ID]
	if ok {
		delete(ac.pending, env.ID)
	}
	ac.pendMu.Unlock()
	if ok {
		ch <- env.Payload
	}
}

func (h *Hub) SendApplyRuleset(nodeID int64, rules []nft.Rule, rev string) (string, error) {
	h.mu.RLock()
	ac, ok := h.conns[nodeID]
	h.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("node %d not connected", nodeID)
	}
	id := ac.nextID()
	ch := make(chan json.RawMessage, 1)
	ac.pendMu.Lock()
	ac.pending[id] = ch
	ac.pendMu.Unlock()
	defer func() {
		ac.pendMu.Lock()
		delete(ac.pending, id)
		ac.pendMu.Unlock()
	}()

	payload, _ := json.Marshal(wsproto.ApplyRuleset{Rev: rev, Rules: rules})
	ac.enqueueWrite(wsproto.Envelope{Type: wsproto.TypeApplyRuleset, ID: id, Payload: payload})

	select {
	case raw := <-ch:
		var ack wsproto.ApplyAck
		if err := json.Unmarshal(raw, &ack); err != nil {
			return "", fmt.Errorf("malformed apply_ack: %w", err)
		}
		if !ack.OK {
			return "", fmt.Errorf("apply rejected: %s", ack.Error)
		}
		return ack.Warning, nil
	case <-time.After(applyAckTimeout):
		return "", errors.New("apply_ack timeout")
	case <-ac.closed:
		return "", errors.New("connection closed before ack")
	}
}

const proxyServiceApplyAckTimeout = 30 * time.Second
const coreInstallAckTimeout = 4 * time.Minute

// SendCoreInstall pushes a core_install frame and waits for ack (download+install).
func (h *Hub) SendCoreInstall(nodeID int64, req wsproto.CoreInstall) (wsproto.CoreInstallAck, error) {
	h.mu.RLock()
	ac, ok := h.conns[nodeID]
	h.mu.RUnlock()
	if !ok {
		return wsproto.CoreInstallAck{}, fmt.Errorf("node %d not connected", nodeID)
	}
	id := ac.nextID()
	ch := make(chan json.RawMessage, 1)
	ac.pendMu.Lock()
	ac.pending[id] = ch
	ac.pendMu.Unlock()
	defer func() {
		ac.pendMu.Lock()
		delete(ac.pending, id)
		ac.pendMu.Unlock()
	}()

	payload, _ := json.Marshal(req)
	ac.enqueueWrite(wsproto.Envelope{Type: wsproto.TypeCoreInstall, ID: id, Payload: payload})

	select {
	case raw := <-ch:
		var ack wsproto.CoreInstallAck
		if err := json.Unmarshal(raw, &ack); err != nil {
			return wsproto.CoreInstallAck{}, fmt.Errorf("malformed core_install_ack: %w", err)
		}
		return ack, nil
	case <-time.After(coreInstallAckTimeout):
		return wsproto.CoreInstallAck{}, errors.New("core_install_ack timeout")
	case <-ac.closed:
		return wsproto.CoreInstallAck{}, errors.New("connection closed")
	}
}

// SendProxyServiceApply pushes a proxy_service_apply frame and waits for ack.
func (h *Hub) SendProxyServiceApply(nodeID int64, req wsproto.ProxyServiceApply) (wsproto.ProxyServiceApplyAck, error) {
	h.mu.RLock()
	ac, ok := h.conns[nodeID]
	h.mu.RUnlock()
	if !ok {
		return wsproto.ProxyServiceApplyAck{}, fmt.Errorf("node %d not connected", nodeID)
	}
	id := ac.nextID()
	ch := make(chan json.RawMessage, 1)
	ac.pendMu.Lock()
	ac.pending[id] = ch
	ac.pendMu.Unlock()
	defer func() {
		ac.pendMu.Lock()
		delete(ac.pending, id)
		ac.pendMu.Unlock()
	}()

	payload, _ := json.Marshal(req)
	ac.enqueueWrite(wsproto.Envelope{Type: wsproto.TypeProxyServiceApply, ID: id, Payload: payload})

	select {
	case raw := <-ch:
		var ack wsproto.ProxyServiceApplyAck
		if err := json.Unmarshal(raw, &ack); err != nil {
			return wsproto.ProxyServiceApplyAck{}, fmt.Errorf("malformed proxy_service_apply_ack: %w", err)
		}
		return ack, nil
	case <-time.After(proxyServiceApplyAckTimeout):
		return wsproto.ProxyServiceApplyAck{}, errors.New("proxy_service_apply_ack timeout")
	case <-ac.closed:
		return wsproto.ProxyServiceApplyAck{}, errors.New("connection closed")
	}
}


// SendProxyServiceStop asks the agent to tear down a core instance.
func (h *Hub) SendProxyServiceStop(nodeID int64, req wsproto.ProxyServiceStop) (wsproto.ProxyServiceStopAck, error) {
	h.mu.RLock()
	ac, ok := h.conns[nodeID]
	h.mu.RUnlock()
	if !ok {
		return wsproto.ProxyServiceStopAck{}, fmt.Errorf("node %d not connected", nodeID)
	}
	id := ac.nextID()
	ch := make(chan json.RawMessage, 1)
	ac.pendMu.Lock()
	ac.pending[id] = ch
	ac.pendMu.Unlock()
	defer func() {
		ac.pendMu.Lock()
		delete(ac.pending, id)
		ac.pendMu.Unlock()
	}()

	payload, _ := json.Marshal(req)
	ac.enqueueWrite(wsproto.Envelope{Type: wsproto.TypeProxyServiceStop, ID: id, Payload: payload})

	select {
	case raw := <-ch:
		var ack wsproto.ProxyServiceStopAck
		if err := json.Unmarshal(raw, &ack); err != nil {
			return wsproto.ProxyServiceStopAck{}, fmt.Errorf("malformed proxy_service_stop_ack: %w", err)
		}
		return ack, nil
	case <-time.After(coreInstallAckTimeout):
		return wsproto.ProxyServiceStopAck{}, errors.New("proxy_service_stop_ack timeout")
	case <-ac.closed:
		return wsproto.ProxyServiceStopAck{}, errors.New("connection closed")
	}
}


func (h *Hub) IsConnected(nodeID int64) bool {
	h.mu.RLock()
	_, ok := h.conns[nodeID]
	h.mu.RUnlock()
	return ok
}

func (h *Hub) SendProbe(nodeID int64, target string) (wsproto.ProbeAck, error) {
	return h.SendProbeEx(nodeID, wsproto.Probe{Target: target})
}

// SendProbeEx sends a probe with optional mode (tcp|tls) and SNI.
func (h *Hub) SendProbeEx(nodeID int64, req wsproto.Probe) (wsproto.ProbeAck, error) {
	h.mu.RLock()
	ac, ok := h.conns[nodeID]
	h.mu.RUnlock()
	if !ok {
		return wsproto.ProbeAck{}, fmt.Errorf("node %d not connected", nodeID)
	}
	id := ac.nextID()
	ch := make(chan json.RawMessage, 1)
	ac.pendMu.Lock()
	ac.pending[id] = ch
	ac.pendMu.Unlock()
	defer func() {
		ac.pendMu.Lock()
		delete(ac.pending, id)
		ac.pendMu.Unlock()
	}()

	payload, _ := json.Marshal(req)
	ac.enqueueWrite(wsproto.Envelope{Type: wsproto.TypeProbe, ID: id, Payload: payload})

	// TLS handshake can take longer than plain TCP dial.
	timeout := 10 * time.Second
	if strings.EqualFold(req.Mode, "tls") {
		timeout = 15 * time.Second
	}
	select {
	case raw := <-ch:
		var ack wsproto.ProbeAck
		if err := json.Unmarshal(raw, &ack); err != nil {
			return wsproto.ProbeAck{}, fmt.Errorf("malformed probe_ack: %w", err)
		}
		return ack, nil
	case <-time.After(timeout):
		return wsproto.ProbeAck{}, errors.New("probe timeout")
	case <-ac.closed:
		return wsproto.ProbeAck{}, errors.New("connection closed")
	}
}

func (h *Hub) BroadcastConfigUpdate(poolSize int) {
	payload, _ := json.Marshal(wsproto.ConfigUpdate{PoolSize: poolSize})
	env := wsproto.Envelope{Type: wsproto.TypeConfigUpdate, Payload: payload}
	h.mu.RLock()
	conns := make([]*agentConn, 0, len(h.conns))
	for _, ac := range h.conns {
		conns = append(conns, ac)
	}
	h.mu.RUnlock()
	for _, ac := range conns {
		ac.enqueueWrite(env)
	}
}

const panelRedirectAckTimeout = 15 * time.Second

// SendPanelRedirect pushes a panel_redirect frame and waits for ack.
// Connection close after dispatch is treated as success (agent is switching).
func (h *Hub) SendPanelRedirect(nodeID int64, panelURL string, force bool) error {
	h.mu.RLock()
	ac, ok := h.conns[nodeID]
	h.mu.RUnlock()
	if !ok {
		return fmt.Errorf("节点未连接")
	}

	id := ac.nextID()
	ch := make(chan json.RawMessage, 1)
	ac.pendMu.Lock()
	ac.pending[id] = ch
	ac.pendMu.Unlock()
	defer func() {
		ac.pendMu.Lock()
		delete(ac.pending, id)
		ac.pendMu.Unlock()
	}()

	payload, _ := json.Marshal(wsproto.PanelRedirect{PanelURL: panelURL, Force: force})
	ac.enqueueWrite(wsproto.Envelope{Type: wsproto.TypePanelRedirect, ID: id, Payload: payload})

	select {
	case raw := <-ch:
		var ack wsproto.PanelRedirectAck
		if err := json.Unmarshal(raw, &ack); err != nil {
			h.noteRedirectAck(nodeID, panelURL, false, "malformed ack")
			return fmt.Errorf("malformed panel_redirect_ack: %w", err)
		}
		if !ack.OK {
			h.noteRedirectAck(nodeID, panelURL, false, ack.Error)
			return fmt.Errorf("%s", ack.Error)
		}
		h.noteRedirectAck(nodeID, panelURL, true, "")
		return nil
	case <-time.After(panelRedirectAckTimeout):
		h.noteRedirectAck(nodeID, panelURL, false, "timeout")
		return fmt.Errorf("panel_redirect 应答超时")
	case <-ac.closed:
		// Agent typically drops the old session after accepting redirect.
		h.noteRedirectAck(nodeID, panelURL, true, "connection closed after dispatch")
		return nil
	}
}

// BroadcastPanelRedirect best-effort notifies every online agent. Returns
// per-node error strings (empty = ok / closed-as-ok).
func (h *Hub) BroadcastPanelRedirect(panelURL string, force bool) map[int64]string {
	ids := h.OnlineNodeIDs()
	out := make(map[int64]string, len(ids))
	var wg sync.WaitGroup
	var mu sync.Mutex
	for _, id := range ids {
		id := id
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := h.SendPanelRedirect(id, panelURL, force)
			mu.Lock()
			if err != nil {
				out[id] = err.Error()
			} else {
				out[id] = ""
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	return out
}

// maybePushPendingRedirect sends panel_redirect after hello when the admin
// left a pending migration URL in settings.
func (h *Hub) maybePushPendingRedirect(nodeID int64) {
	url, err := db.GetSetting(h.DB, "pending_panel_redirect_url")
	if err != nil || strings.TrimSpace(url) == "" {
		return
	}
	url = strings.TrimSpace(url)
	go func() {
		// Small delay so the agent finishes post-hello setup (migrate_rules, etc.).
		time.Sleep(500 * time.Millisecond)
		if err := h.SendPanelRedirect(nodeID, url, false); err != nil {
			log.Printf("hub: pending panel_redirect node %d: %v", nodeID, err)
		}
	}()
}

// Helpers --------------------------------------------------------------

// trustedProxyNets contains the CIDRs from which X-Forwarded-For / X-Real-IP
// are considered trustworthy. Defaults to loopback and RFC1918 private ranges
// so a same-machine or LAN reverse proxy is trusted, but a random internet peer
// cannot spoof its address. The server also calls this for the speed/landing
// HTTP endpoints, so it lives on Hub's helper section rather than inside the
// WS handler.
var trustedProxyNets = func() []*net.IPNet {
	nets := []*net.IPNet{}
	for _, cidr := range []string{"127.0.0.0/8", "::1/128", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"} {
		if _, n, err := net.ParseCIDR(cidr); err == nil {
			nets = append(nets, n)
		}
	}
	return nets
}()

func isTrustedProxy(ip net.IP) bool {
	for _, n := range trustedProxyNets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func extractIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	// Strip zone / accidental brackets; do NOT use LastIndex(":") — that
	// corrupts IPv6 literals when SplitHostPort already failed.
	host = strings.Trim(host, "[]")
	remoteIP := net.ParseIP(host)

	// Behind nginx/caddy on the panel host, RemoteAddr is loopback/private.
	// Prefer the left-most public address from X-Forwarded-For / X-Real-IP
	// when the immediate peer is a trusted proxy.
	if remoteIP != nil && isTrustedProxy(remoteIP) {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			for _, part := range strings.Split(xff, ",") {
				cand := strings.TrimSpace(part)
				cand = strings.Trim(cand, "[]")
				if cand == "" {
					continue
				}
				// Skip non-public hops (proxy chain internals).
				if ip := net.ParseIP(cand); ip != nil && ipNonPublic(ip) {
					continue
				}
				return cand
			}
			// Fall through: all XFF hops private — keep remote or X-Real-IP.
		}
		if xri := strings.TrimSpace(r.Header.Get("X-Real-IP")); xri != "" {
			xri = strings.Trim(xri, "[]")
			if ip := net.ParseIP(xri); ip == nil || !ipNonPublic(ip) {
				return xri
			}
		}
	}
	return host
}

// ipNonPublic reports loopback / RFC1918 / link-local / unspecified.
func ipNonPublic(ip net.IP) bool {
	if ip == nil {
		return true
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified()
}

func ipStrNonPublic(s string) bool {
	ip := net.ParseIP(strings.Trim(strings.TrimSpace(s), "[]"))
	if ip == nil {
		// Hostnames are treated as public-facing (operator-configured).
		return false
	}
	return ipNonPublic(ip)
}

// panelSelfAddresses returns IPs that identify the panel host itself so we
// never store them as a remote node's connect/relay address.
//
// Sources:
//  1. All non-loopback addresses on local interfaces (panel public/private NICs)
//  2. settings.panel_url host when it is a literal IP
//  3. DNS A/AAAA for panel_url hostname (domain installs)
func panelSelfAddresses(d *sql.DB) map[string]bool {
	out := localHostIPSet()
	if d == nil {
		return out
	}
	if v, err := db.GetSetting(d, "panel_url"); err == nil {
		v = strings.TrimSpace(v)
		if v != "" {
			host := v
			if strings.Contains(v, "://") {
				if u, err := parseURLHost(v); err == nil {
					host = u
				}
			} else if h, _, err := net.SplitHostPort(v); err == nil {
				host = h
			}
			host = strings.Trim(host, "[]")
			if ip := net.ParseIP(host); ip != nil {
				out[ip.String()] = true
			} else if host != "" {
				// Domain panel_url: resolve so reverse-proxy XFF of the panel
				// public IP is recognized as self even when not on a local NIC
				// (e.g. floating IP / LB VIP only in DNS).
				for _, ip := range resolveHostIPs(host) {
					out[ip] = true
				}
			}
		}
	}
	return out
}

// localHostIPSet returns every IP configured on this host (minus loopback /
// unspecified / link-local). Used to recognize the panel when agents connect
// through a reverse proxy that stamps the panel's own public address into
// X-Forwarded-For / X-Real-IP.
func localHostIPSet() map[string]bool {
	out := map[string]bool{}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return out
	}
	for _, a := range addrs {
		var ip net.IP
		switch v := a.(type) {
		case *net.IPNet:
			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		}
		if ip == nil || ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() {
			continue
		}
		// Normalize IPv4-mapped form.
		if v4 := ip.To4(); v4 != nil {
			out[v4.String()] = true
		} else {
			out[ip.String()] = true
		}
	}
	return out
}

// resolveHostIPs looks up A/AAAA for host. Best-effort; empty on failure.
// Results are cached briefly so frequent hellos do not hammer the resolver.
func resolveHostIPs(host string) []string {
	host = strings.TrimSpace(host)
	if host == "" {
		return nil
	}
	resolveHostIPsMu.Lock()
	if t, ok := resolveHostIPsAt[host]; ok && time.Since(t) < 5*time.Minute {
		out := append([]string(nil), resolveHostIPsCache[host]...)
		resolveHostIPsMu.Unlock()
		return out
	}
	resolveHostIPsMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil
	}
	var out []string
	for _, a := range ips {
		ip := a.IP
		if ip == nil || ip.IsLoopback() || ip.IsUnspecified() {
			continue
		}
		if v4 := ip.To4(); v4 != nil {
			out = append(out, v4.String())
		} else {
			out = append(out, ip.String())
		}
	}
	resolveHostIPsMu.Lock()
	resolveHostIPsCache[host] = out
	resolveHostIPsAt[host] = time.Now()
	resolveHostIPsMu.Unlock()
	return append([]string(nil), out...)
}

var (
	resolveHostIPsMu    sync.Mutex
	resolveHostIPsCache = map[string][]string{}
	resolveHostIPsAt    = map[string]time.Time{}
)

func parseURLHost(raw string) (string, error) {
	// Minimal parse without importing net/url at call sites repeatedly —
	// net/url is fine; keep local helper for clarity.
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	// Use strings to avoid heavy deps: scheme://host[:port]/path]
	rest := raw
	if i := strings.Index(raw, "://"); i >= 0 {
		rest = raw[i+3:]
	}
	if i := strings.IndexAny(rest, "/?"); i >= 0 {
		rest = rest[:i]
	}
	host := rest
	if h, _, err := net.SplitHostPort(rest); err == nil {
		host = h
	}
	return strings.Trim(host, "[]"), nil
}

// resolveNodeConnectIP chooses the address stored as nodes.address (UI「连接 IP」).
// IPv4 is primary for the data plane.
//
// Priority:
//  1. Agent-probed public IPv4
//  2. Public observed WS peer (v4) that is NOT the panel itself
//  3. Agent-probed public IPv6
//  4. Public observed WS peer (v6) that is NOT the panel
//  5. Private/loopback observed (lab / direct dial)
//  6. Any remaining probe string
//
// Reverse proxies commonly stamp the panel public IP into X-Forwarded-For /
// X-Real-IP; those must never beat a real agent probe or a non-self peer.
func resolveNodeConnectIP(d *sql.DB, observed, probedV4, probedV6 string) string {
	self := panelSelfAddresses(d)
	obs := strings.Trim(strings.TrimSpace(observed), "[]")
	obsIP := net.ParseIP(obs)
	obsSelf := obsIP != nil && self[obsIP.String()]

	publicCand := func(s string) string {
		s = strings.Trim(strings.TrimSpace(s), "[]")
		if s == "" {
			return ""
		}
		ip := net.ParseIP(s)
		if ip == nil || ipNonPublic(ip) || self[ip.String()] {
			return ""
		}
		return s
	}

	// 1) public IPv4 from agent
	if p := publicCand(probedV4); p != "" {
		return p
	}
	// 2) public IPv4 observed (real peer, not panel)
	if !obsSelf && obsIP != nil && obsIP.To4() != nil {
		if p := publicCand(obs); p != "" {
			return p
		}
	}
	// 3) public IPv6 from agent
	if p := publicCand(probedV6); p != "" {
		return p
	}
	// 4) public IPv6 observed
	if !obsSelf && obsIP != nil && obsIP.To4() == nil {
		if p := publicCand(obs); p != "" {
			return p
		}
	}
	// 5) private/loopback observed peer (lab)
	if obs != "" && !obsSelf {
		return obs
	}
	// 6) last resort: any probe (even private)
	if s := strings.TrimSpace(probedV4); s != "" {
		return s
	}
	if s := strings.TrimSpace(probedV6); s != "" {
		return strings.Trim(s, "[]")
	}
	return obs
}

func fillNodeRelayHosts(d *sql.DB, node *db.Node, connectIP, observedIP, probedV4, probedV6, declaredV4, declaredV6 string) {
	self := panelSelfAddresses(d)
	pickV4 := func(cands ...string) string {
		var private string
		for _, s := range cands {
			s = strings.TrimSpace(strings.Trim(s, "[]"))
			if s == "" {
				continue
			}
			ip := net.ParseIP(s)
			if ip == nil {
				// hostname — only for v4 relay
				return s
			}
			if ip.To4() == nil {
				continue
			}
			if self[ip.String()] {
				continue
			}
			if !ipNonPublic(ip) {
				return s
			}
			if private == "" {
				private = s
			}
		}
		return private
	}
	pickV6 := func(cands ...string) string {
		var private string
		for _, s := range cands {
			s = strings.TrimSpace(strings.Trim(s, "[]"))
			if s == "" {
				continue
			}
			ip := net.ParseIP(s)
			if ip == nil || ip.To4() != nil {
				continue
			}
			if self[ip.String()] {
				continue
			}
			if !ipNonPublic(ip) {
				return s
			}
			if private == "" {
				private = s
			}
		}
		return private
	}

	connectIsV6 := false
	if ip := net.ParseIP(connectIP); ip != nil {
		connectIsV6 = ip.To4() == nil
	}
	// Evict pre-split IPv6 literals stuck in relay_host.
	if ip := net.ParseIP(node.RelayHost); ip != nil && ip.To4() == nil {
		if node.RelayHostV6 == "" && !(connectIsV6 && connectIP != "" && pickV6(connectIP) != "") {
			_ = db.UpdateNodeRelayHostV6(d, node.ID, node.RelayHost)
			node.RelayHostV6 = node.RelayHost
		}
		_ = db.UpdateNodeRelayHost(d, node.ID, "")
		node.RelayHost = ""
	}

	// Heal mistaken auto-fill (not operator-declared / not --relay-host):
	//  - panel self address (reverse-proxy noise)
	//  - private IP when agent now reports a public probe
	// Do NOT overwrite a different public IP / hostname the operator set in UI.
	publicProbeV4 := ""
	if p := pickV4(probedV4); p != "" {
		if ip := net.ParseIP(p); ip != nil && !ipNonPublic(ip) {
			publicProbeV4 = p
		}
	}
	publicProbeV6 := ""
	if p := pickV6(probedV6); p != "" {
		if ip := net.ParseIP(p); ip != nil && !ipNonPublic(ip) {
			publicProbeV6 = p
		}
	}

	if node.RelayHost != "" && !node.RelayHostDeclared {
		ip := net.ParseIP(node.RelayHost)
		shouldClear := false
		if ip != nil && self[ip.String()] {
			shouldClear = true
		} else if publicProbeV4 != "" && ip != nil && ip.To4() != nil && ipNonPublic(ip) {
			shouldClear = true
		} else if publicProbeV4 != "" && ip != nil && ip.To4() != nil {
			// Relay was auto-seeded from a bad observed/XFF hop (same as this
			// connection's observed IP) and agent now reports a different public
			// egress — heal even when panelSelfAddresses missed the panel IP
			// (domain panel_url + DNS failure, floating IP not on NIC, etc.).
			obs := strings.Trim(strings.TrimSpace(observedIP), "[]")
			if obs != "" && ip.String() == obs && ip.String() != publicProbeV4 {
				shouldClear = true
			}
		}
		if shouldClear {
			_ = db.UpdateNodeRelayHost(d, node.ID, "")
			node.RelayHost = ""
		}
	}
	if node.RelayHostV6 != "" && !node.RelayHostV6Declared {
		ip := net.ParseIP(node.RelayHostV6)
		shouldClear := false
		if ip != nil && self[ip.String()] {
			shouldClear = true
		} else if publicProbeV6 != "" && ip != nil && ip.To4() == nil && ipNonPublic(ip) {
			shouldClear = true
		} else if publicProbeV6 != "" && ip != nil && ip.To4() == nil {
			obs := strings.Trim(strings.TrimSpace(observedIP), "[]")
			if obs != "" && ip.String() == obs && ip.String() != publicProbeV6 {
				shouldClear = true
			}
		}
		if shouldClear {
			_ = db.UpdateNodeRelayHostV6(d, node.ID, "")
			node.RelayHostV6 = ""
		}
	}

	if node.RelayHost == "" && declaredV4 == "" {
		// Prefer public agent probe, then connectIP, then observed peer, then private.
		v4 := pickV4(probedV4, connectIP, observedIP)
		if v4 != "" {
			_ = db.UpdateNodeRelayHost(d, node.ID, v4)
			node.RelayHost = v4
		}
	}
	if node.RelayHostV6 == "" && declaredV6 == "" {
		v6 := pickV6(probedV6, connectIP, observedIP)
		if v6 != "" {
			_ = db.UpdateNodeRelayHostV6(d, node.ID, v6)
			node.RelayHostV6 = v6
		}
	}
}

// applyDeclaredRelayHosts handles operator-declared relay_host/relay_host_v6
// values sent via Hello.DeclaredRelayHost/DeclaredRelayHostV6 (see
// cmd/nft-agent's --relay-host/--relay-host-v6 flags). Unlike
// fillNodeRelayHosts, which only ever seeds an empty field once, a declared
// value is authoritative: it overwrites whatever is in the DB on every
// hello where it's present, so config drift self-heals. When the daemon
// stops declaring a value (flag removed, daemon restarted), the DB field
// unlocks but keeps its last value rather than going blank, so a live route
// doesn't disappear out from under the running link.
func applyDeclaredRelayHosts(d *sql.DB, node *db.Node, declaredV4, declaredV6 string) {
	if declaredV4 != "" {
		if isValidRelayHost(declaredV4) {
			if node.RelayHost != declaredV4 || !node.RelayHostDeclared {
				_ = db.UpdateNodeRelayHost(d, node.ID, declaredV4)
				_ = db.SetNodeRelayHostDeclared(d, node.ID, true)
				node.RelayHost, node.RelayHostDeclared = declaredV4, true
			}
		} else {
			log.Printf("hub: node %d declared invalid relay_host %q, ignoring", node.ID, declaredV4)
		}
	} else if node.RelayHostDeclared {
		_ = db.SetNodeRelayHostDeclared(d, node.ID, false)
		node.RelayHostDeclared = false
	}

	if declaredV6 != "" {
		if isValidRelayHostV6(declaredV6) {
			if node.RelayHostV6 != declaredV6 || !node.RelayHostV6Declared {
				_ = db.UpdateNodeRelayHostV6(d, node.ID, declaredV6)
				_ = db.SetNodeRelayHostV6Declared(d, node.ID, true)
				node.RelayHostV6, node.RelayHostV6Declared = declaredV6, true
			}
		} else {
			log.Printf("hub: node %d declared invalid relay_host_v6 %q, ignoring", node.ID, declaredV6)
		}
	} else if node.RelayHostV6Declared {
		_ = db.SetNodeRelayHostV6Declared(d, node.ID, false)
		node.RelayHostV6Declared = false
	}
}

func lookupNodeBySecret(d *sql.DB, secret string) (*db.Node, error) {
	if secret == "" {
		return nil, errors.New("empty secret")
	}
	// Node secrets are stored in plaintext; the agent presents the same value,
	// so compare directly. (Legacy v3.0.0 rows stored a SHA-256 hash and will
	// no longer match — those nodes must reset their token to reconnect.)
	var id int64
	err := d.QueryRow(`SELECT id FROM nodes WHERE secret=? AND disabled=0`, secret).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return db.GetNode(d, id)
}

func readEnvelope(ctx context.Context, ws *websocket.Conn, timeout time.Duration) (wsproto.Envelope, error) {
	rctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	_, r, err := ws.Reader(rctx)
	if err != nil {
		return wsproto.Envelope{}, err
	}
	b, err := io.ReadAll(io.LimitReader(r, hubMaxReadBytes+1))
	if err != nil {
		return wsproto.Envelope{}, err
	}
	if len(b) > hubMaxReadBytes {
		return wsproto.Envelope{}, fmt.Errorf("frame exceeds %d bytes", hubMaxReadBytes)
	}
	var env wsproto.Envelope
	if err := json.Unmarshal(b, &env); err != nil {
		return wsproto.Envelope{}, err
	}
	return env, nil
}

func writeEnvelope(ctx context.Context, ws *websocket.Conn, env wsproto.Envelope) error {
	b, err := json.Marshal(env)
	if err != nil {
		return err
	}
	wctx, cancel := context.WithTimeout(ctx, writeTimeoutFor(len(b)))
	defer cancel()
	return ws.Write(wctx, websocket.MessageText, b)
}

func writeError(ctx context.Context, ws *websocket.Conn, code, msg string) {
	p, _ := json.Marshal(wsproto.Error{Code: code, Message: msg})
	_ = writeEnvelope(ctx, ws, wsproto.Envelope{Type: wsproto.TypeError, Payload: p})
}

// applyCounters folds per-rule bytes_delta into the rule_hops table and the
// owning user's usage. The (node_id, listen_port, proto) tuple identifies
// the hop. Each sample is resolved to its rule_hop row so we learn the hop
// id and the owning rule/user.
//
// The same bytes flow through every hop of a chain, so the global user quota is
// billed exactly once — at the entry hop (position 0) — weighted by the entry
// node's own rate_multiplier and the user's billing rate. Per-grant quota
// charges raw bytes once per logical segment, at that segment's first hop, onto
// the segment's logical node grant. Quota suppression keys on the same logical
// node end to end.
// applyCounters folds agent counter samples into ledgers and the speed cache.
// Returns (true, "") on success — including empty heartbeats. Returns
// (false, reason) when the batch could not be fully persisted so the agent
// can retransmit via CountersReadd (counters_ack NACK).
func (h *Hub) applyCounters(nodeID int64, samples []wsproto.CounterSample) (ok bool, errMsg string) {
	// Empty heartbeat: agent is alive with zero deltas this tick. Only refresh
	// the speed cache (zero sticky rates); skip hop/traffic DB work.
	if len(samples) == 0 {
		h.speedCache.update(nodeID, nil)
		return true, ""
	}

	hopMap, err := db.RuleHopMapByNode(h.DB, nodeID)
	if err != nil {
		log.Printf("hub: node %d load rule hop map for counters: %v", nodeID, err)
		return false, "load hop map: " + err.Error()
	}
	// Only the rules referenced by this node's hops are ever looked up, so load
	// just those instead of scanning the whole rules table every counters batch.
	ruleIDSet := map[int64]bool{}
	for _, rh := range hopMap {
		ruleIDSet[rh.RuleID] = true
	}
	ruleIDs := make([]int64, 0, len(ruleIDSet))
	for id := range ruleIDSet {
		ruleIDs = append(ruleIDs, id)
	}
	ruleMap, _ := db.RulesByIDs(h.DB, ruleIDs)
	if ruleMap == nil {
		ruleMap = map[int64]*db.Rule{}
	}
	multipliers, err := db.NodeRateMultipliers(h.DB)
	if err != nil {
		log.Printf("hub: node %d load node rate multipliers: %v", nodeID, err)
		multipliers = map[int64]float64{}
	}
	// Segment-first hops drive per-grant accounting: each logical segment's
	// grant is charged once, at the hop where the segment begins.
	segFirst, err := db.SegmentFirstHops(h.DB, ruleIDs)
	if err != nil {
		log.Printf("hub: node %d load segment first hops: %v", nodeID, err)
		segFirst = map[int64]map[int]int64{}
	}

	// Landing-exit ledger lookups for this batch: which (owner, host, port)
	// triples are present landing exits, and each rule's final hop position —
	// the only hop whose bytes reach the exit ledger, since middle hops target
	// system relay addresses. On a load error the batch skips exit metering
	// entirely (under-counting beats mis-counting).
	ownerSet := map[int64]bool{}
	for _, r := range ruleMap {
		if r.OwnerID.Valid {
			ownerSet[r.OwnerID.Int64] = true
		}
	}
	ownerIDs := make([]int64, 0, len(ownerSet))
	for id := range ownerSet {
		ownerIDs = append(ownerIDs, id)
	}
	exitSet, err := db.PresentLandingExitSet(h.DB, ownerIDs)
	if err != nil {
		log.Printf("hub: node %d load landing exit set: %v", nodeID, err)
		exitSet = nil
	}
	maxPos, err := db.MaxHopPositions(h.DB, ruleIDs)
	if err != nil {
		log.Printf("hub: node %d load hop positions: %v", nodeID, err)
		exitSet = nil
	}

	node, err := db.GetNode(h.DB, nodeID)
	if err != nil {
		log.Printf("hub: node %d load for billing direction: %v", nodeID, err)
		return false, "load node: " + err.Error()
	}

	type userNode struct{ userID, nodeID int64 }
	touched := map[userNode]bool{}

	// Pre-load all owning users and run cycle-reset checks once per batch,
	// outside the sample loop and outside the flush transaction. This removes
	// per-sample GetUserByID/CheckAndResetTrafficCycle queries from the hot
	// path while keeping the reset/redispatch behavior identical.
	userCache := map[int64]*db.User{}
	if len(ownerIDs) > 0 {
		loaded, err := db.GetUsersByIDs(h.DB, ownerIDs)
		if err != nil {
			log.Printf("hub: node %d load users for counters: %v", nodeID, err)
		} else {
			for uid, u := range loaded {
				if u == nil {
					continue
				}
				if reset, _ := db.CheckAndResetTrafficCycle(h.DB, u); reset {
					if u.Disabled && u.DisableReason.Valid && u.DisableReason.String == "流量超额" {
						_ = db.SetUserDisabled(h.DB, uid, false, "")
					}
					if nodes, err := db.DistinctUserNodes(h.DB, uid); err == nil && h.Redispatch != nil {
						go h.Redispatch(nodes)
					}
				}
				userCache[uid] = u
			}
		}
	}

	// Accumulate all row mutations and flush them in one transaction after the
	// loop. Reads, cycle resets and redispatch stay outside any tx: with
	// MaxOpenConns(1) a tx holds the only connection, so a pool read or a
	// redispatch goroutine inside it would deadlock.
	type hopWrite struct{ lastBytes, lastUp, lastDown, addTotal, addBilled int64 }
	hopWrites := map[int64]*hopWrite{}
	userNodeAdds := map[userNode]int64{}
	userAdds := map[int64]int64{}
	totalUserAdds := map[int64]int64{}
	ruleExitAdds := map[int64]int64{}
	exitAdds := map[db.UserExitKey]int64{}
	// The reporting node's raw ledger: every sampled byte counts, both
	// directions regardless of unidirectional billing, and before the rule_hop
	// match below — a sample whose rule was deleted mid-batch is still real
	// forwarded volume.
	var rawAdd int64

	for _, s := range samples {
		// Pre-v0.33 agents send BytesDelta without direction; fall back to it
		// so traffic accounting continues while the node upgrades.
		if s.BytesUp == 0 && s.BytesDown == 0 && s.BytesDelta > 0 {
			s.BytesUp = s.BytesDelta
		}
		totalDelta := s.BytesUp + s.BytesDown
		rawAdd += totalDelta
		billedDelta := totalDelta
		if node.Unidirectional {
			billedDelta = s.BytesUp
		}
		key := fmt.Sprintf("%s/%d", s.Proto, s.ListenPort)
		rh, ok := hopMap[key]
		if !ok {
			log.Printf("hub: node %d counters sample for %s/%d matched no rule_hop row (rule may have been deleted)", nodeID, s.Proto, s.ListenPort)
			continue
		}
		r := ruleMap[rh.RuleID]

		// Exit ledger: final hop only, raw and unweighted — it records real
		// traffic to the destination, independent of billing multipliers and
		// the node's unidirectional setting. Growth must mark the pair touched
		// itself: a downlink-only batch on a unidirectional node bills 0 and
		// would otherwise never reach the quota callback.
		if r != nil && r.OwnerID.Valid && totalDelta > 0 && len(exitSet) > 0 && rh.Position == maxPos[rh.RuleID] {
			key := db.UserExitKey{UserID: r.OwnerID.Int64, Host: r.ExitHost, Port: r.ExitPort}
			if exitSet[key] {
				exitAdds[key] += totalDelta
				touched[userNode{key.UserID, nodeID}] = true
			}
		}

		// User billing is now based on the landing exit traffic only: the raw
		// upload+download bytes observed at the rule's final hop. Entry node
		// multipliers are intentionally excluded from user billing.
		billedBase := billedDelta
		var userID int64
		hasOwner := r != nil && r.OwnerID.Valid && billedDelta > 0
		if hasOwner {
			userID = r.OwnerID.Int64

			// Entry multiplier is kept only for the legacy rule_hops.billed_bytes
			// admin metric; it does not affect user billing.
			entryMult, ok := multipliers[r.NodeID]
			if !ok || entryMult < 0 {
				entryMult = 1.0
			}
			billedBase = int64(math.Round(float64(billedDelta) * entryMult))
		}

		// rule_hops: last_bytes stay raw for speed display. total_bytes is always
		// the raw forwarded volume; billed_bytes carries the rate-neutral billed
		// base (raw × entry node multiplier) on the entry hop and stays 0 on
		// middle hops. A tcp+udp hop can fan in as two samples to the same row;
		// last_* take the last sample and the total sums.
		w := hopWrites[rh.ID]
		if w == nil {
			w = &hopWrite{}
			hopWrites[rh.ID] = w
		}
		w.lastBytes = totalDelta
		w.lastUp = s.BytesUp
		w.lastDown = s.BytesDown
		w.addTotal += totalDelta
		if rh.Position == 0 {
			w.addBilled += billedBase
		}

		if !hasOwner {
			continue
		}

		// Per-grant quota: raw bytes, charged once per logical segment at its
		// first hop, onto the segment's logical node grant (the entry segment's
		// via is rules.node_id, so its grant is included). Suppression marks the
		// same logical node so RulesAffectedByNode and OnTrafficUpdate stay in
		// step with this accounting.
		if via, ok := segFirst[rh.RuleID][rh.Position]; ok {
			userNodeAdds[userNode{userID, via}] += billedDelta
			touched[userNode{userID, via}] = true
		}
		// User billing: count raw up+down at the final hop only. This matches the
		// landing exit ledger and is the single source of truth for user display.
		if len(maxPos) > 0 && rh.Position == maxPos[rh.RuleID] && totalDelta > 0 {
			ruleExitAdds[rh.RuleID] += totalDelta
			userAdds[userID] += totalDelta
			totalUserAdds[userID] += totalDelta
		}
	}

	// Flush every accumulated mutation in a single transaction: one commit (one
	// fsync) for the whole batch instead of 3-5 auto-commits per sample.
	// persistOK tracks whether the agent may drop this batch (true) or must
	// retransmit (false — DB begin/commit/row errors).
	persistOK := true
	persistErr := ""
	if len(hopWrites) > 0 || len(userNodeAdds) > 0 || len(userAdds) > 0 || len(totalUserAdds) > 0 || len(ruleExitAdds) > 0 || len(exitAdds) > 0 || rawAdd > 0 {
		tx, err := h.DB.Begin()
		if err != nil {
			log.Printf("hub: node %d counters tx begin: %v", nodeID, err)
			persistOK = false
			persistErr = "tx begin: " + err.Error()
		} else {
			ok := true
			if rawAdd > 0 {
				if err := db.AddNodeRawTraffic(tx, nodeID, rawAdd); err != nil {
					log.Printf("hub: node %d raw traffic add: %v", nodeID, err)
					ok = false
					persistErr = "raw traffic: " + err.Error()
				}
				if ok {
					if err := db.AddNodeDailyRawTraffic(tx, nodeID, rawAdd); err != nil {
						log.Printf("hub: node %d daily raw traffic add: %v", nodeID, err)
						ok = false
						persistErr = "daily raw: " + err.Error()
					}
				}
			}
			for id, w := range hopWrites {
				if !ok {
					break
				}
				if _, err := tx.Exec(`UPDATE rule_hops SET last_bytes=?, last_bytes_up=?, last_bytes_down=?, total_bytes=total_bytes+?, billed_bytes=billed_bytes+? WHERE id=?`,
					w.lastBytes, w.lastUp, w.lastDown, w.addTotal, w.addBilled, id); err != nil {
					log.Printf("hub: node %d counters rule_hop update: %v", nodeID, err)
					ok = false
					persistErr = "hop update: " + err.Error()
					break
				}
			}
			for un, delta := range userNodeAdds {
				if !ok {
					break
				}
				if _, err := tx.Exec(`UPDATE user_nodes SET traffic_used_bytes = traffic_used_bytes + ? WHERE user_id=? AND node_id=?`, delta, un.userID, un.nodeID); err != nil {
					log.Printf("hub: user %d node %d per-node traffic add: %v", un.userID, un.nodeID, err)
					ok = false
					persistErr = "user_node: " + err.Error()
					break
				}
			}
			for uid, delta := range userAdds {
				if !ok {
					break
				}
				if _, err := tx.Exec(`UPDATE users SET traffic_used_bytes = traffic_used_bytes + ? WHERE id=?`, delta, uid); err != nil {
					log.Printf("hub: user %d traffic add: %v", uid, err)
					ok = false
					persistErr = "user traffic: " + err.Error()
					break
				}
				// Same raw final-hop delta into today's per-user day bucket
				// (Asia/Shanghai). Independent of billing_rate / admin reset.
				if err := db.AddUserDailyTraffic(tx, uid, delta); err != nil {
					log.Printf("hub: user %d daily traffic add: %v", uid, err)
					ok = false
					persistErr = "daily traffic: " + err.Error()
					break
				}
			}
			for uid, delta := range totalUserAdds {
				if !ok {
					break
				}
				if _, err := tx.Exec(`UPDATE users SET total_traffic_used_bytes = total_traffic_used_bytes + ? WHERE id=?`, delta, uid); err != nil {
					log.Printf("hub: user %d total traffic add: %v", uid, err)
					ok = false
					persistErr = "total traffic: " + err.Error()
					break
				}
			}
			for ruleID, delta := range ruleExitAdds {
				if !ok {
					break
				}
				if _, err := tx.Exec(`UPDATE rules SET exit_bytes = exit_bytes + ? WHERE id=?`, delta, ruleID); err != nil {
					log.Printf("hub: rule %d exit bytes add: %v", ruleID, err)
					ok = false
					persistErr = "exit_bytes: " + err.Error()
					break
				}
			}
			for k, delta := range exitAdds {
				if !ok {
					break
				}
				// A zero-row hit means the row was flipped absent and deleted
				// between load and flush; dropping one batch is the intent of
				// that deletion.
				if _, err := tx.Exec(`UPDATE user_landing_exits SET used_bytes = used_bytes + ?, updated_at = ? WHERE user_id=? AND host=? AND port=?`,
					delta, time.Now().Unix(), k.UserID, k.Host, k.Port); err != nil {
					log.Printf("hub: user %d exit %s:%d ledger add: %v", k.UserID, k.Host, k.Port, err)
					ok = false
					persistErr = "landing exit: " + err.Error()
					break
				}
			}
			if ok {
				if err := tx.Commit(); err != nil {
					log.Printf("hub: node %d counters tx commit: %v", nodeID, err)
					persistOK = false
					persistErr = "tx commit: " + err.Error()
				}
			} else {
				_ = tx.Rollback()
				persistOK = false
			}
		}
	}

	deltas := make([]counterDelta, 0, len(samples))
	for _, s := range samples {
		// Attribute the hop's speed to its rule's owner so the per-user speed
		// view can filter it. An unmatched hop (rule deleted mid-batch) or an
		// ownerless admin rule leaves ownerID 0 — it still counts toward the
		// node total, just not toward any user's share.
		//
		// ruleID is set on every matched hop so multi-hop / composite chains
		// still produce a per-rule rate even if only a middle hop is currently
		// reporting. The rule snapshot prefers the entry hop (position 0)
		// when present and falls back to any hop of that rule otherwise —
		// never summing every hop of the same chain (that would N× the rate).
		var ownerID, ruleID int64
		var hopPos = -1
		if rh, ok := hopMap[s.Proto+"/"+strconv.Itoa(s.ListenPort)]; ok {
			if r := ruleMap[rh.RuleID]; r != nil && r.OwnerID.Valid {
				ownerID = r.OwnerID.Int64
			}
			ruleID = rh.RuleID
			hopPos = rh.Position
		}
		elapsedSec := 0.0
		if s.ElapsedMs > 0 {
			elapsedSec = float64(s.ElapsedMs) / 1000.0
		}
		deltas = append(deltas, counterDelta{
			proto:         s.Proto,
			listenPortStr: strconv.Itoa(s.ListenPort),
			bytesUp:       s.BytesUp,
			bytesDown:     s.BytesDown,
			elapsedSec:    elapsedSec,
			ownerID:       ownerID,
			ruleID:        ruleID,
			hopPos:        hopPos,
		})
	}
	// Always update speed cache — empty samples are heartbeats that zero idle hops.
	h.speedCache.update(nodeID, deltas)

	// Quota enforcement (the OnTrafficUpdate callback) can call back into the hub
	// to re-dispatch a ruleset, which blocks on an apply_ack that this very
	// readerLoop must deliver. Run it off-goroutine so the reader never waits on
	// itself — otherwise every enforcement fires the 30s apply timeout and flaps
	// the node. touched is loop-local, so snapshotting it here is race-free.
	if h.OnTrafficUpdate != nil && len(touched) > 0 {
		pairs := make([]userNode, 0, len(touched))
		for un := range touched {
			pairs = append(pairs, un)
		}
		go func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("hub: OnTrafficUpdate panic: %v", r)
				}
			}()
			for _, un := range pairs {
				h.OnTrafficUpdate(un.userID, un.nodeID)
			}
		}()
	}
	return persistOK, persistErr
}

// applyProxyCounters folds agent proxy_counters samples into per-instance
// traffic ledgers. Returns (true,"") on success so the agent can drop the
// batch; (false, reason) triggers retransmit via proxy_counters_ack NACK.
func (h *Hub) applyProxyCounters(nodeID int64, samples []wsproto.ProxyCounterSample) (bool, string) {
	if len(samples) == 0 {
		return true, ""
	}
	tx, err := h.DB.Begin()
	if err != nil {
		log.Printf("hub: node %d proxy_counters tx begin: %v", nodeID, err)
		return false, "tx begin: " + err.Error()
	}
	ok := true
	errMsg := ""
	for _, s := range samples {
		if s.InstanceID <= 0 || (s.BytesUp == 0 && s.BytesDown == 0) {
			continue
		}
		if err := db.AddProxyInstanceTraffic(tx, s.InstanceID, nodeID, s.BytesUp, s.BytesDown); err != nil {
			// Unknown instance (deleted) is non-fatal for the rest of the batch.
			if strings.Contains(err.Error(), "not on node") {
				log.Printf("hub: node %d proxy_counters skip instance %d: %v", nodeID, s.InstanceID, err)
				continue
			}
			log.Printf("hub: node %d proxy_counters instance %d: %v", nodeID, s.InstanceID, err)
			ok = false
			errMsg = err.Error()
			break
		}
		// Count proxy raw bytes toward the node's raw ledger (same as forward).
		raw := s.BytesUp + s.BytesDown
		if raw > 0 {
			if err := db.AddNodeRawTraffic(tx, nodeID, raw); err != nil {
				log.Printf("hub: node %d proxy raw traffic: %v", nodeID, err)
				ok = false
				errMsg = err.Error()
				break
			}
			if err := db.AddNodeDailyRawTraffic(tx, nodeID, raw); err != nil {
				log.Printf("hub: node %d proxy daily raw: %v", nodeID, err)
				ok = false
				errMsg = err.Error()
				break
			}
		}
	}
	if ok {
		if err := tx.Commit(); err != nil {
			log.Printf("hub: node %d proxy_counters commit: %v", nodeID, err)
			return false, "tx commit: " + err.Error()
		}
		return true, ""
	}
	_ = tx.Rollback()
	return false, errMsg
}


// applyRuleHopEdit folds a node-reported edit to its hop in ruleID back
// into the rule skeleton and re-dispatches every node the regeneration
// touched, returning the rule's copyable entry endpoint. The hop is located
// by (ruleID, nodeID): a rule can't repeat a node, so that pair is unique.
func (h *Hub) applyRuleHopEdit(nodeID, ruleID int64, listenPort int, mode, comment string) (string, error) {
	r, err := db.GetRule(h.DB, ruleID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("规则不存在")
	}
	if err != nil {
		return "", err
	}
	hops, err := db.ListRuleHops(h.DB, ruleID)
	if err != nil {
		return "", err
	}
	found := false
	inputs := make([]db.HopInput, len(hops))
	for i, hp := range hops {
		in := db.HopInput{NodeID: hp.NodeID, Mode: hp.Mode, ViaNodeID: hp.ViaNodeID}
		if hp.NodeID == nodeID {
			found = true
			in.DesiredPort = listenPort
			in.Mode = db.NormalizeForwardMode(mode)
			in.Comment = comment
		}
		inputs[i] = in
	}
	if !found {
		return "", fmt.Errorf("节点不在该规则上")
	}
	tx, err := h.DB.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	entry, _, affected, err := db.RegenerateRule(tx, r, inputs, nil)
	if err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	if h.Redispatch != nil {
		h.Redispatch(affected)
	}
	return entry, nil
}

// applyRuleDelete removes the whole rule (all hops on all nodes) on behalf
// of a node that participates in it, then re-dispatches every node that ran
// its hops so the deleted rules leave the kernel.
func (h *Hub) applyRuleDelete(nodeID, ruleID int64) error {
	hops, err := db.ListRuleHops(h.DB, ruleID)
	if err != nil {
		return err
	}
	onRule := false
	for _, hp := range hops {
		if hp.NodeID == nodeID {
			onRule = true
			break
		}
	}
	if !onRule {
		return fmt.Errorf("节点不在该规则上")
	}
	nodes, err := db.DeleteRule(h.DB, ruleID)
	if err != nil {
		return err
	}
	if h.Redispatch != nil {
		h.Redispatch(nodes)
	}
	return nil
}

// handleRuleCreate creates a new single-hop rule on the requesting node.
// The node must have an owner (via node.owner_id) who is active and within quota.
func (h *Hub) handleRuleCreate(ac *agentConn, env wsproto.Envelope) {
	var req wsproto.RuleCreate
	if err := json.Unmarshal(env.Payload, &req); err != nil {
		sendRuleAckErr(ac, env.ID, "malformed payload")
		return
	}
	node, err := db.GetNode(h.DB, ac.nodeID)
	if err != nil {
		sendRuleAckErr(ac, env.ID, "节点不存在")
		return
	}
	if node.OwnerID == nil {
		sendRuleAckErr(ac, env.ID, "节点无归属用户")
		return
	}
	ownerID := *node.OwnerID
	u, err := db.GetUserByID(h.DB, ownerID)
	if err != nil {
		sendRuleAckErr(ac, env.ID, "归属用户不存在")
		return
	}
	if u.Disabled {
		sendRuleAckErr(ac, env.ID, "用户已被禁用")
		return
	}
	if u.ExpiresAt.Valid && u.ExpiresAt.Int64 > 0 && u.ExpiresAt.Int64 < time.Now().Unix() {
		sendRuleAckErr(ac, env.ID, "用户已过期")
		return
	}
	total, _ := db.CountRulesForUser(h.DB, ownerID)
	if total >= u.MaxForwards {
		sendRuleAckErr(ac, env.ID, fmt.Sprintf("超出用户最大转发数（%d）", u.MaxForwards))
		return
	}

	tx, err := h.DB.Begin()
	if err != nil {
		sendRuleAckErr(ac, env.ID, "内部错误")
		return
	}
	defer tx.Rollback()

	rl := &db.Rule{
		NodeID:   ac.nodeID,
		OwnerID:  sql.NullInt64{Int64: ownerID, Valid: true},
		Name:     req.Name,
		Proto:    req.Proto,
		ExitHost: req.ExitHost,
		ExitPort: req.ExitPort,
		Comment:  req.Comment,
	}
	id, err := db.CreateRule(tx, rl)
	if err != nil {
		sendRuleAckErr(ac, env.ID, err.Error())
		return
	}
	rl.ID = id

	hop := db.HopInput{NodeID: ac.nodeID, Mode: db.NormalizeForwardMode(req.Mode), ViaNodeID: ac.nodeID}
	if req.ListenPort > 0 {
		hop.DesiredPort = req.ListenPort
	}
	entry, _, affected, err := db.RegenerateRule(tx, rl, []db.HopInput{hop}, nil)
	if err != nil {
		sendRuleAckErr(ac, env.ID, err.Error())
		return
	}
	if err := tx.Commit(); err != nil {
		sendRuleAckErr(ac, env.ID, "提交失败")
		return
	}
	if h.Redispatch != nil {
		go h.Redispatch(affected)
	}
	ackP, _ := json.Marshal(wsproto.RuleCmdAck{OK: true, Entry: entry})
	ac.enqueueWrite(wsproto.Envelope{Type: wsproto.TypeRuleCmdAck, ID: env.ID, Payload: ackP})
}

// handleRuleUpdate modifies a single-hop rule's header and regenerates it.
// Only rules with exactly 1 hop that belong to the node's owner are editable.
func (h *Hub) handleRuleUpdate(ac *agentConn, env wsproto.Envelope) {
	var req wsproto.RuleUpdate
	if err := json.Unmarshal(env.Payload, &req); err != nil {
		sendRuleAckErr(ac, env.ID, "malformed payload")
		return
	}
	node, err := db.GetNode(h.DB, ac.nodeID)
	if err != nil {
		sendRuleAckErr(ac, env.ID, "节点不存在")
		return
	}
	if node.OwnerID == nil {
		sendRuleAckErr(ac, env.ID, "节点无归属用户")
		return
	}
	rl, err := db.GetRule(h.DB, req.RuleID)
	if err != nil {
		sendRuleAckErr(ac, env.ID, "规则不存在")
		return
	}
	if !rl.OwnerID.Valid || rl.OwnerID.Int64 != *node.OwnerID {
		sendRuleAckErr(ac, env.ID, "无权操作该规则")
		return
	}
	// Only single-hop rules are editable from a node
	hops, err := db.ListRuleHops(h.DB, req.RuleID)
	if err != nil {
		sendRuleAckErr(ac, env.ID, "读取跳数失败")
		return
	}
	if len(hops) != 1 {
		sendRuleAckErr(ac, env.ID, "仅支持编辑单跳规则")
		return
	}

	rl.Name = req.Name
	rl.Proto = req.Proto
	rl.ExitHost = req.ExitHost
	rl.ExitPort = req.ExitPort
	rl.Comment = req.Comment

	tx, err := h.DB.Begin()
	if err != nil {
		sendRuleAckErr(ac, env.ID, "内部错误")
		return
	}
	defer tx.Rollback()

	if err := db.UpdateRuleHeader(tx, rl); err != nil {
		sendRuleAckErr(ac, env.ID, err.Error())
		return
	}
	hop := db.HopInput{NodeID: ac.nodeID, Mode: db.NormalizeForwardMode(req.Mode), ViaNodeID: ac.nodeID}
	if req.ListenPort > 0 {
		hop.DesiredPort = req.ListenPort
	}
	entry, _, affected, err := db.RegenerateRule(tx, rl, []db.HopInput{hop}, nil)
	if err != nil {
		sendRuleAckErr(ac, env.ID, err.Error())
		return
	}
	if err := tx.Commit(); err != nil {
		sendRuleAckErr(ac, env.ID, "提交失败")
		return
	}
	if h.Redispatch != nil {
		go h.Redispatch(affected)
	}
	ackP, _ := json.Marshal(wsproto.RuleCmdAck{OK: true, Entry: entry})
	ac.enqueueWrite(wsproto.Envelope{Type: wsproto.TypeRuleCmdAck, ID: env.ID, Payload: ackP})
}

// handleMigrateRules bulk-imports rules from an agent's local state.
// Each rule becomes a new single-hop rule owned by the node's owner.
func (h *Hub) handleMigrateRules(ac *agentConn, env wsproto.Envelope) {
	var req wsproto.MigrateRules
	if err := json.Unmarshal(env.Payload, &req); err != nil {
		sendRuleAckErr(ac, env.ID, "malformed payload")
		return
	}
	node, err := db.GetNode(h.DB, ac.nodeID)
	if err != nil {
		sendRuleAckErr(ac, env.ID, "节点不存在")
		return
	}
	if node.OwnerID == nil {
		sendRuleAckErr(ac, env.ID, "节点无归属用户")
		return
	}
	ownerID := *node.OwnerID
	u, err := db.GetUserByID(h.DB, ownerID)
	if err != nil {
		sendRuleAckErr(ac, env.ID, "归属用户不存在")
		return
	}
	if u.Disabled {
		sendRuleAckErr(ac, env.ID, "用户已被禁用")
		return
	}
	if u.ExpiresAt.Valid && u.ExpiresAt.Int64 > 0 && u.ExpiresAt.Int64 < time.Now().Unix() {
		sendRuleAckErr(ac, env.ID, "用户已过期")
		return
	}

	total, _ := db.CountRulesForUser(h.DB, ownerID)
	if total+len(req.Rules) > u.MaxForwards {
		sendRuleAckErr(ac, env.ID, fmt.Sprintf("超出用户最大转发数（%d），当前 %d 条，迁入 %d 条", u.MaxForwards, total, len(req.Rules)))
		return
	}

	tx, err := h.DB.Begin()
	if err != nil {
		sendRuleAckErr(ac, env.ID, "内部错误")
		return
	}
	defer tx.Rollback()

	var allAffected []int64
	for _, r := range req.Rules {
		exitHost := r.DestIP
		if r.DestHost != "" {
			exitHost = r.DestHost
		}
		name := r.Comment
		if name == "" {
			name = r.RuleName
		}
		rl := &db.Rule{
			NodeID:   ac.nodeID,
			OwnerID:  sql.NullInt64{Int64: ownerID, Valid: true},
			Name:     name,
			Proto:    r.Proto,
			ExitHost: exitHost,
			ExitPort: r.DestPort,
		}
		id, err := db.CreateRule(tx, rl)
		if err != nil {
			sendRuleAckErr(ac, env.ID, fmt.Sprintf("创建规则失败: %v", err))
			return
		}
		rl.ID = id
		hop := db.HopInput{NodeID: ac.nodeID, Mode: db.NormalizeForwardMode(r.Mode), ViaNodeID: ac.nodeID}
		if r.SrcPort > 0 {
			hop.DesiredPort = r.SrcPort
		}
		_, _, affected, err := db.RegenerateRule(tx, rl, []db.HopInput{hop}, nil)
		if err != nil {
			sendRuleAckErr(ac, env.ID, fmt.Sprintf("生成规则失败: %v", err))
			return
		}
		allAffected = append(allAffected, affected...)
	}

	if err := tx.Commit(); err != nil {
		sendRuleAckErr(ac, env.ID, "提交失败")
		return
	}
	if h.Redispatch != nil {
		go h.Redispatch(allAffected)
	}
	ackP, _ := json.Marshal(wsproto.RuleCmdAck{OK: true})
	ac.enqueueWrite(wsproto.Envelope{Type: wsproto.TypeRuleCmdAck, ID: env.ID, Payload: ackP})
}

func sendRuleAckErr(ac *agentConn, id, msg string) {
	ackP, _ := json.Marshal(wsproto.RuleCmdAck{OK: false, Error: msg})
	ac.enqueueWrite(wsproto.Envelope{Type: wsproto.TypeRuleCmdAck, ID: id, Payload: ackP})
}
