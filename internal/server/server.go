package server

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"nft/internal/db"
	"nft/internal/landing"
	"nft/internal/nft"
	"nft/internal/resolver"
)

type Server struct {
	DB                *sql.DB
	Hub               *Hub
	Dispatcher        *Dispatcher
	Landing           *landing.Fetcher
	loginLimiter      *loginLimiter
	stopExpiry        chan struct{}
	stopCycle         chan struct{}
	stopLandingSync   chan struct{}
	stopLandingExpiry chan struct{}
	enforcerWg        sync.WaitGroup
	stopAll           chan struct{}
	stopOnce          sync.Once
	asyncWg           sync.WaitGroup
}

func New(d *sql.DB) (*Server, error) {
	if _, err := EnsureSelfNode(d); err != nil {
		return nil, fmt.Errorf("ensure self node: %w", err)
	}
	hub := NewHub(d)
	disp := &Dispatcher{DB: d, Hub: hub}
	s := &Server{
		DB: d, Hub: hub, Dispatcher: disp, Landing: landing.NewFetcher(),
		loginLimiter: newLoginLimiter(),
		stopExpiry: make(chan struct{}), stopCycle: make(chan struct{}),
		stopLandingSync: make(chan struct{}), stopLandingExpiry: make(chan struct{}),
		stopAll: make(chan struct{}),
	}
	hub.OnTrafficUpdate = func(userID int64, nodeID int64) {
		s.enforcePerNodeQuota(userID, nodeID)
		s.enforceUserQuota(userID)
		s.enforceExitQuota(userID)
	}
	hub.Redispatch = s.redispatchNodes
	s.enforcerWg.Add(4)
	safeGo(func() { defer s.enforcerWg.Done(); s.expiryEnforcer() })
	safeGo(func() { defer s.enforcerWg.Done(); s.cycleResetEnforcer() })
	safeGo(func() { defer s.enforcerWg.Done(); s.landingSyncEnforcer() })
	safeGo(func() { defer s.enforcerWg.Done(); s.landingExpiryEnforcer() })
	return s, nil
}

// expiryEnforcer periodically scans for users whose expires_at has passed
// and re-dispatches the affected nodes so their forwarding rules are
// removed from the kernel.
func (s *Server) expiryEnforcer() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopExpiry:
			return
		case <-ticker.C:
			nodes, err := db.ExpiredUserNodeIDs(s.DB)
			if err != nil {
				log.Printf("expiry: query expired-user nodes: %v", err)
				continue
			}
			for _, n := range nodes {
				if err := s.dispatchToNode(n); err != nil {
					log.Printf("expiry: re-dispatch node %d: %v", n, err)
				}
			}
		}
	}
}

// cycleResetEnforcer periodically checks every user's traffic reset window.
// When the window rolls over it re-enables any user who was disabled for
// exceeding quota and re-dispatches their nodes, restoring their rules to
// the kernel for the fresh cycle.
//
// This covers users who are globally disabled (all rules already removed from
// the kernel) and therefore receive no traffic — without this goroutine they
// would never reach the cycle-reset check in applyCounters.
//
// The re-push runs unconditionally after a reset, not only for re-enabled
// users: per-grant quota exclusions are evaluated at push time only, so a
// suppressed rule stays dead until something re-pushes even when the user was
// never globally disabled.
func (s *Server) cycleResetEnforcer() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopCycle:
			return
		case <-ticker.C:
			users, err := db.ListUsers(s.DB)
			if err != nil {
				log.Printf("cycle: list users: %v", err)
				continue
			}
			for _, u := range users {
				if u.TrafficResetDays <= 0 {
					continue
				}
				reset, err := db.CheckAndResetTrafficCycle(s.DB, u)
				if err != nil {
					log.Printf("cycle: check reset for user %d: %v", u.ID, err)
					continue
				}
				if !reset {
					continue
				}
				if u.Disabled && u.DisableReason.Valid && u.DisableReason.String == "流量超额" {
					if err := db.SetUserDisabled(s.DB, u.ID, false, ""); err != nil {
						log.Printf("cycle: re-enable user %d: %v", u.ID, err)
						continue
					}
				}
				// Quota exclusions are evaluated at push time only; a fresh
				// cycle must re-push or suppressed rules stay dead.
				if nodes, err := db.DistinctUserNodes(s.DB, u.ID); err == nil {
					for _, n := range nodes {
						if err := s.dispatchToNode(n); err != nil {
							log.Printf("cycle: re-dispatch node %d for user %d: %v", n, u.ID, err)
						}
					}
				}
			}
		}
	}
}

// landingSyncEnforcer keeps materialized landing-exit sets in step with
// subscription content when no page load resolves them. The first pass runs
// immediately and includes manual-URI users, backfilling existing deployments
// right after upgrade; the table then persists, so later restarts have no
// empty-set window.
func (s *Server) landingSyncEnforcer() {
	// If Stop() is called before this goroutine starts, skip the backfill pass
	// so we never touch the database after shutdown has begun.
	select {
	case <-s.stopLandingSync:
		return
	default:
	}
	s.landingSyncPass(true)
	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopLandingSync:
			return
		case <-ticker.C:
			s.landingSyncPass(false)
		}
	}
}

// landingExpiryEnforcer periodically scans for landing exits whose expires_at
// has passed and disables them (present=0), then re-dispatches affected nodes
// so rules using those exits are removed from the kernel.
func (s *Server) landingExpiryEnforcer() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopLandingExpiry:
			return
		case <-ticker.C:
			expired, err := db.ExpiredLandingExits(s.DB)
			if err != nil {
				log.Printf("landing-expiry: query: %v", err)
				continue
			}
			for _, k := range expired {
				if _, err := s.DB.Exec(`UPDATE user_landing_exits SET present=0, updated_at=? WHERE user_id=? AND host=? AND port=?`,
					time.Now().Unix(), k.UserID, k.Host, k.Port); err != nil {
					log.Printf("landing-expiry: disable %d/%s:%d: %v", k.UserID, k.Host, k.Port, err)
					continue
				}
				log.Printf("landing-expiry: disabled expired exit user=%d %s:%d", k.UserID, k.Host, k.Port)
				s.redispatchUserExit(k.UserID, k.Host, k.Port)
			}
		}
	}
}

// Stop gracefully shuts down the server's background goroutines and the hub.
// It waits for both the periodic enforcers and any in-flight async dispatches
// to finish, so it must be called before closing the database connection.
func (s *Server) Stop() {
	if s.Hub != nil {
		s.Hub.Close()
	}
	close(s.stopExpiry)
	close(s.stopCycle)
	close(s.stopLandingSync)
	close(s.stopLandingExpiry)
	s.enforcerWg.Wait()
	s.stopOnce.Do(func() { close(s.stopAll) })
	s.asyncWg.Wait()
}

// goAsync starts fn in a goroutine unless the server is already stopped.
// It tracks the goroutine in asyncWg so Stop() can wait for it.
func (s *Server) goAsync(fn func()) {
	select {
	case <-s.stopAll:
		return
	default:
	}
	s.asyncWg.Add(1)
	go func() {
		defer s.asyncWg.Done()
		select {
		case <-s.stopAll:
			return
		default:
		}
		fn()
	}()
}

// landingSyncPass syncs every user with a landing source. includeManualOnly
// widens the pass to users without a subscription — their set only changes on
// save, so the periodic pass skips them.
func (s *Server) landingSyncPass(includeManualOnly bool) {
	users, err := db.ListUsers(s.DB)
	if err != nil {
		log.Printf("landing: sync pass list users: %v", err)
		return
	}
	for _, u := range users {
		if !hasLandingSource(u) {
			continue
		}
		if !includeManualOnly && !hasDynamicSource(u) {
			continue
		}
		nodes, ok := s.resolveLandingExits(u, false)
		if !ok {
			continue
		}
		s.syncLandingExits(u, nodes)
	}
}

// userBillableTraffic returns the traffic volume the panel shows and enforces
// against the user's quota: raw used bytes scaled by billing_rate. Rate ≤ 0 is
// treated as 1 so a misconfigured profile never freezes the counter at 0.
func userBillableTraffic(u *db.User) int64 {
	if u == nil {
		return 0
	}
	rate := u.BillingRate
	if rate <= 0 {
		rate = 1
	}
	return int64(math.Round(float64(u.TrafficUsedBytes) * rate))
}

// enforceUserQuota disables a user that has reached its traffic quota and
// re-pushes every node it had rule hops on so ActiveRuleHopsForPush (which
// excludes disabled users) removes them from the kernel. Quota 0 = unlimited.
//
// Enforcement uses the same billable figure the account UI shows (used ×
// billing_rate), so a user who already appears over-quota cannot keep using
// the service while the raw counter alone is still under the cap.
func (s *Server) enforceUserQuota(userID int64) {
	u, err := db.GetUserByID(s.DB, userID)
	if err != nil {
		log.Printf("quota: load user %d: %v", userID, err)
		return
	}
	billable := userBillableTraffic(u)
	if u.Disabled || u.TrafficQuotaBytes <= 0 || billable < u.TrafficQuotaBytes {
		return
	}
	if err := db.SetUserDisabled(s.DB, userID, true, "流量超额"); err != nil {
		log.Printf("quota: disable user %d: %v", userID, err)
		return
	}
	log.Printf("user %d disabled: traffic quota reached (billable %d = used %d × rate %g / quota %d bytes)",
		userID, billable, u.TrafficUsedBytes, u.BillingRate, u.TrafficQuotaBytes)
	nodes, err := db.DistinctUserNodes(s.DB, userID)
	if err != nil {
		log.Printf("quota: user %d nodes: %v", userID, err)
		return
	}
	for _, n := range nodes {
		if err := s.dispatchToNode(n); err != nil {
			log.Printf("quota: re-dispatch node %d after disabling user %d: %v", n, userID, err)
		}
	}
}

// enforcePerNodeQuota re-dispatches nodes affected by any per-node quota
// overrun for userID. Only nodes whose rules include a hop where quota > 0
// and used >= quota are targeted, so unrelated nodes are never churned.
func (s *Server) enforcePerNodeQuota(userID int64, nodeID int64) {
	exceeded, err := db.NodesExceedingQuota(s.DB, userID)
	if err != nil {
		log.Printf("quota: per-node check user %d: %v", userID, err)
		return
	}
	for _, excNode := range exceeded {
		affectedNodes, err := db.RulesAffectedByNode(s.DB, userID, excNode)
		if err != nil {
			log.Printf("quota: affected nodes for user %d node %d: %v", userID, excNode, err)
			continue
		}
		for _, n := range affectedNodes {
			if err := s.dispatchToNode(n); err != nil {
				log.Printf("quota: re-dispatch node %d after per-node quota user %d: %v", n, userID, err)
			}
		}
	}
}

// enforceExitQuota re-pushes the nodes carrying rules whose landing-exit
// ledger reached quota, so ActiveRuleHopsForPush drops exactly the rules
// pointed at the exhausted exit.
func (s *Server) enforceExitQuota(userID int64) {
	exceeded, err := db.ExitsExceedingQuota(s.DB, userID)
	if err != nil {
		log.Printf("quota: exit check user %d: %v", userID, err)
		return
	}
	for _, k := range exceeded {
		s.redispatchUserExit(userID, k.Host, k.Port)
	}
}

// dispatchToNode builds the panel-segment ruleset for nodeID from the
// rule_hops DB and dispatches it via the Hub (or unix socket for the
// self-node).
//
// The outcome is reflected on the nodes row so the panel UI can show
// sync status: success stamps last_apply_at and clears last_error;
// failure stamps last_error while preserving last_apply_at.
func (s *Server) dispatchToNode(nodeID int64) error {
	ruleHops, err := db.ActiveRuleHopsForPush(s.DB, nodeID)
	if err != nil {
		_ = db.MarkNodeDispatchError(s.DB, nodeID, err.Error())
		return err
	}
	rules := buildRules(s.DB, ruleHops)
	rev := computeRev(rules)
	warning, err := s.Dispatcher.Dispatch(nodeID, rules, rev)
	if err != nil {
		_ = db.MarkNodeDispatchError(s.DB, nodeID, err.Error())
		return err
	}
	_ = db.MarkNodeApplied(s.DB, nodeID, warning)
	return nil
}

// dispatchAfterMutation wraps the common "CRUD-handler dispatches to a
// node and wants to surface failure to the admin doing the mutation"
// pattern.
func (s *Server) dispatchAfterMutation(w http.ResponseWriter, nodeID int64, action string) {
	if err := s.dispatchToNode(nodeID); err != nil {
		setFlash(w, fmt.Sprintf("%s 已保存，但下发到节点失败：%v", action, err))
		log.Printf("dispatch node %d (%s): %v", nodeID, action, err)
		return
	}
	if n, err := db.GetNode(s.DB, nodeID); err == nil && n.LastWarning != "" {
		setFlash(w, fmt.Sprintf("%s 已保存，但 %s", action, n.LastWarning))
	}
}

// dispatchAfterFanout dispatches to every node touched by a user-scope
// mutation. Per-node errors are aggregated into a single flash because
// the flash cookie holds only one message.
// An overall timeout plus a bounded concurrency semaphore prevent a single
// offline/slow node from holding the HTTP request for the full per-node
// dispatch timeout.
func (s *Server) dispatchAfterFanout(w http.ResponseWriter, nodeIDs []int64, action string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	type result struct {
		nodeID int64
		err    error
	}
	results := make(chan result, len(nodeIDs))
	var wg sync.WaitGroup

	// Bound concurrency so a large fanout doesn't hammer the database and
	// WebSocket hub all at once.
	const maxConcurrent = 4
	sem := make(chan struct{}, maxConcurrent)

	for _, n := range nodeIDs {
		wg.Add(1)
		go func(nodeID int64) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				results <- result{nodeID: nodeID, err: ctx.Err()}
				return
			}
			defer func() { <-sem }()

			if err := ctx.Err(); err != nil {
				results <- result{nodeID: nodeID, err: err}
				return
			}
			results <- result{nodeID: nodeID, err: s.dispatchToNode(nodeID)}
		}(n)
	}
	wg.Wait()
	close(results)

	var failed []string
	for r := range results {
		if r.err != nil {
			log.Printf("dispatch node %d (%s): %v", r.nodeID, action, r.err)
			failed = append(failed, fmt.Sprintf("节点 %d: %v", r.nodeID, r.err))
		}
	}
	if len(failed) > 0 {
		sort.Strings(failed)
		setFlash(w, fmt.Sprintf("%s 已保存，但下发到 %d 个节点失败（%s）",
			action, len(failed), strings.Join(failed, "；")))
	}
}

// redispatchNodes re-pushes kernel state to every node a background (WS-driven)
// rule mutation touched. Dispatches run off the caller's goroutine.
func (s *Server) redispatchNodes(nodeIDs []int64) {
	for _, n := range nodeIDs {
		id := n
		s.goAsync(func() {
			if err := s.dispatchToNode(id); err != nil {
				log.Printf("dispatch node %d (规则变更): %v", id, err)
			}
		})
	}
}

// buildRules converts panel-side RuleHop rows into kernel-side nft.Rule
// values. Lookup tables are preloaded in bulk so the conversion is
// O(ruleHops) with no per-row queries.
func buildRules(d *sql.DB, ruleHops []*db.RuleHop) []nft.Rule {
	ruleMap, _ := db.RulesByID(d)
	if ruleMap == nil {
		ruleMap = map[int64]*db.Rule{}
	}
	users, _ := db.UsersByID(d)
	if users == nil {
		users = map[int64]*db.User{}
	}
	shapes, _ := db.GrantShapes(d)

	ruleIDSet := map[int64]bool{}
	for _, rh := range ruleHops {
		ruleIDSet[rh.RuleID] = true
	}
	ruleIDs := make([]int64, 0, len(ruleIDSet))
	for id := range ruleIDSet {
		ruleIDs = append(ruleIDs, id)
	}
	hopCounts, _ := db.RuleHopCounts(d, ruleIDs)
	if hopCounts == nil {
		hopCounts = map[int64]int{}
	}

	rules := make([]nft.Rule, 0, len(ruleHops))
	for _, rh := range ruleHops {
		rule := nft.Rule{
			Proto:    rh.Proto,
			SrcPort:  rh.ListenPort,
			DestPort: rh.TargetPort,
			Comment:  rh.Comment,
			Mode:     rh.Mode,
			HopCount: hopCounts[rh.RuleID],
		}
			if r := ruleMap[rh.RuleID]; r != nil {
				rule.RuleID = r.ID
				rule.RuleName = r.Name
				if r.OwnerID.Valid {
					if u := users[r.OwnerID.Int64]; u != nil {
						rule.OwnerName = u.Username
					}
					// Shaping follows the rule owner's grant for the segment this hop
					// belongs to. The hop's via_node_id is the logical segment node
					// (e.g. the composite node ID for composite expansions), while the
					// hop's own node_id is the physical node running the forwarding.
					// Try via_node_id first, then the physical hop node, then the rule's
					// entry node — so grants on any layer activate the shared bucket.
					//
					// Per-grant rate wins; when it is 0/missing, fall back to the user's
					// global speed_limit_mbytes so a profile-level cap still shapes
					// every hop of the owner's rules.
					var shapeGrant *db.GrantShape
					candidates := [...]int64{rh.ViaNodeID, rh.NodeID, r.NodeID}
					for _, nid := range candidates {
						if gs, ok := shapes[[2]int64{r.OwnerID.Int64, nid}]; ok {
							shapeGrant = &gs
							break
						}
					}
					rate := 0
					grantID := int64(0)
					if shapeGrant != nil {
						rate = int(shapeGrant.RateLimitMBytes)
						grantID = shapeGrant.GrantID
					}
					if rate <= 0 {
						if u := users[r.OwnerID.Int64]; u != nil && u.SpeedLimitMBytes > 0 {
							rate = u.SpeedLimitMBytes
							// No per-node grant row: use a stable synthetic group in the
							// high half of the 16-bit mark space so all of this owner's
							// hops share one bucket without colliding with ordinary
							// SQLite rowids that typically sit well below 0x8000.
							if grantID <= 0 {
								grantID = 0x8000 + (r.OwnerID.Int64 & 0x7FFF)
							}
						}
					}
					if rate > 0 && grantID > 0 {
						rule.ShapeGroup = grantID
						// RateMBytes is the historical wire name; the value is
						// Mbps (10 ≈ "10M" on a residential line). All of this
						// owner's rules that match the same grant share one
						// ShapeGroup bucket on the agent.
						rule.RateMBytes = rate
						// Legacy per-rule mirror for pre-group agents (Mbps).
						rule.BandwidthMbps = rate
						// Shared grant caps are enforced by the userspace token
						// bucket. Kernel+tc is best-effort (iface detection,
						// CAP_NET_ADMIN) and often degrades silently — force TCP
						// onto userspace so concurrent rules of the same grant
						// share one real bucket end-to-end.
						switch rule.Proto {
						case "tcp", "tcp+udp":
							rule.Mode = nft.ModeUserspace
						}
					}
				}
			}
		if resolver.IsHostname(rh.TargetHost) {
			rule.DestHost = rh.TargetHost
		} else {
			rule.DestIP = rh.TargetHost
		}
		rules = append(rules, rule)
	}
	return rules
}

// computeRev returns a stable hash of the ruleset so a reconnecting
// agent whose last_applied_rev matches can be skipped. Determinism
// hinges on ActiveRuleHopsForPush returning rows in a stable order
// (it sorts by listen_port).
//
// Rule metadata is panel-side display info, not part of the data plane;
// exclude it so a rule rename does not force a redundant re-apply on
// reconnecting nodes.
func computeRev(rules []nft.Rule) string {
	bare := make([]nft.Rule, len(rules))
	for i, r := range rules {
		r.RuleID = 0
		r.RuleName = ""
		r.OwnerName = ""
		bare[i] = r
	}
	h := sha256.New()
	b, _ := json.Marshal(bare)
	h.Write(b)
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(logRequests)
	r.Get("/healthz", s.healthz)
	r.HandleFunc("/v1/agents", s.Hub.ServeWS)
	r.Get("/v1/binary", s.serveBinary)

	// --- JSON API ---
	r.Route("/api", func(r chi.Router) {
		r.Use(s.csrfProtect)
		r.Post("/login", s.apiLogin)
		r.Post("/logout", s.apiLogout)
		r.Get("/branding", s.apiBranding)

		r.Group(func(r chi.Router) {
			r.Use(s.requireAPIAuth)
			r.Get("/me", s.apiMe)
			r.Post("/change-password", s.apiChangePassword)
			r.Get("/probe", s.probeEndpoint)
			r.Get("/probe-chain", s.probeChainEndpoint)
			r.Get("/dashboard", s.apiDashboard)
			r.Post("/sub-fetch", s.apiSubFetch)
			r.Get("/node-roles", s.apiGetNodeRoles)
		})

		// Admin routes
		r.Group(func(r chi.Router) {
			r.Use(s.requireAPIAuth, s.requireRole("admin"))

			r.Get("/nodes", s.apiListNodes)
			r.Post("/nodes", s.apiCreateNode)
			r.Get("/nodes/{id}", s.apiGetNode)
			r.Post("/nodes/{id}/rename", s.apiRenameNode)
			r.Post("/nodes/{id}/relay-host", s.apiSetNodeRelayHost)
			r.Post("/nodes/{id}/relay-host-v6", s.apiSetNodeRelayHostV6)
			r.Post("/nodes/{id}/port-range", s.apiUpdateNodePortRange)
			r.Post("/nodes/{id}/resync", s.apiResyncNode)
			r.Post("/nodes/{id}/reset-token", s.apiResetNodeToken)
			r.Post("/nodes/{id}/upgrade", s.apiUpgradeNode)
			r.Delete("/nodes/{id}", s.apiDeleteNode)
			r.Post("/nodes/{id}/toggle", s.apiToggleNode)
			r.Post("/nodes/{id}/owner", s.apiUpdateNodeOwner)
			r.Post("/nodes/reorder", s.apiReorderNodes)
			r.Post("/nodes/resync-all", s.apiResyncAllNodes)
			r.Post("/nodes/upgrade-all", s.apiUpgradeAllNodes)
			r.Post("/nodes/{id}/rate-multiplier", s.apiSetNodeRateMultiplier)
			r.Post("/nodes/{id}/unidirectional", s.apiSetNodeUnidirectional)
			r.Post("/nodes/{id}/no-direct-exit", s.apiSetNodeNoDirectExit)
			r.Get("/nodes/{id}/hops", s.apiListNodeHops)
			r.Post("/nodes/{id}/hops", s.apiUpdateNodeHops)
			r.Post("/nodes/{id}/roles", s.apiUpdateNodeRolesMask)
			r.Get("/nodes/{id}/bindings", s.apiListNodeBindings)
			r.Post("/nodes/{id}/bindings", s.apiUpdateNodeBindings)
			r.Get("/nodes/{id}/downstream-bindings", s.apiListNodeDownstreamBindings)
			r.Post("/nodes/{id}/downstream-bindings", s.apiUpdateNodeDownstreamBindings)
			r.Get("/node-bindings", s.apiListAllNodeBindings)

			r.Get("/settings", s.apiGetSettings)
			r.Post("/settings", s.apiSaveSettings)
			r.Post("/node-roles", s.apiSetNodeRoles)

			r.Get("/rules", s.apiListRules)
			r.Post("/rules", s.apiCreateRule)
			r.Get("/rules/{id}", s.apiGetRule)
			r.Put("/rules/{id}", s.apiUpdateRule)
			r.Delete("/rules/{id}", s.apiDeleteRule)
			r.Post("/rules/{id}/hops/{pos}/reallocate", s.apiReallocateRuleHop)

			r.Get("/users/{id}", s.apiGetUser)
			r.Post("/users", s.apiCreateUser)
			r.Post("/users/{id}/grants", s.apiGrantNode)
			r.Delete("/users/{id}/grants/{nodeID}", s.apiRevokeNode)
			r.Post("/users/{id}/grants/batch-revoke", s.apiBatchRevokeNodes)
			r.Post("/users/{id}/quota", s.apiSetUserQuota)
			r.Post("/users/{id}/max-forwards", s.apiSetMaxForwards)
			r.Post("/users/{id}/landing", s.apiSetUserLanding)
			r.Post("/users/{id}/expiry", s.apiSetUserExpiry)
			r.Post("/users/{id}/reset-traffic", s.apiResetUserTraffic)
			r.Post("/users/{id}/reset-days", s.apiSetResetDays)
			r.Post("/users/{id}/nodes/{nodeID}/max-forwards", s.apiSetPerNodeMaxForwards)
			r.Post("/users/{id}/nodes/{nodeID}/quota", s.apiSetPerNodeQuota)
			r.Post("/users/{id}/nodes/{nodeID}/rate-limit", s.apiSetPerNodeRateLimit)
			r.Post("/users/{id}/nodes/{nodeID}/reset-traffic", s.apiResetPerNodeTraffic)
			r.Get("/users/{id}/landing-exits", s.apiListUserLandingExits)
			r.Post("/users/{id}/landing-exits/quota", s.apiSetLandingExitQuota)
			r.Post("/users/{id}/landing-exits/reset", s.apiResetLandingExitTraffic)
			r.Post("/users/{id}/landing-exits/delete", s.apiDeleteLandingExit)
			r.Post("/users/{id}/landing-exits/rename", s.apiRenameLandingExit)
			r.Post("/users/{id}/landing-exits/expires", s.apiSetLandingExitExpires)

			r.Get("/users", s.apiListUsers)
			r.Patch("/users/{id}/profile", s.apiUpdateUserProfile)
			r.Post("/users/{id}/admin-note", s.apiSetAdminNote)
			r.Post("/users/{id}/toggle", s.apiToggleUser)
			r.Post("/users/{id}/reset-password", s.apiResetUserPassword)
			r.Delete("/users/{id}", s.apiDeleteUser)
			r.Post("/grants/batch-apply", s.apiBatchApplyGrants)

		// Announcements
		r.Get("/announcements", s.apiListAnnouncements)
		r.Post("/announcements", s.apiCreateAnnouncement)
		r.Delete("/announcements/{id}", s.apiDeleteAnnouncement)

		// Node repository
		r.Get("/node-repo", s.apiListNodeRepo)
		r.Post("/node-repo", s.apiCreateNodeRepoEntry)
		r.Patch("/node-repo/{id}", s.apiUpdateNodeRepoEntry)
		r.Delete("/node-repo/{id}", s.apiDeleteNodeRepoEntry)
		r.Post("/users/{id}/assign-repo", s.apiAssignRepoToUser)

			// Allow admin to change own username (same handler as user route)
			// r.Post("/my/username", s.apiChangeUsername) — moved to shared auth-only group below
		})

		// User routes
		r.Group(func(r chi.Router) {
			r.Use(s.requireAPIAuth, s.requireRole("user"))
			r.Get("/my", s.apiMyDashboard)
			// /my/username moved to shared auth-only group below so both admin and user can use it
			r.Get("/my/landing-nodes", s.apiMyLandingNodes)
		r.Get("/my/announcements", s.apiMyAnnouncements)
			r.Get("/my/rules", s.apiMyListRules)
			r.Get("/my/rules/{id}", s.apiMyGetRule)
			r.Post("/my/rules", s.apiMyCreateRule)
			r.Put("/my/rules/{id}", s.apiMyUpdateRule)
			r.Delete("/my/rules/{id}", s.apiMyDeleteRule)
			r.Get("/my/token", s.apiMyGetToken)
			r.Post("/my/token", s.apiMyCreateToken)
			r.Delete("/my/token", s.apiMyDeleteToken)
			r.Post("/my/token/refresh", s.apiMyRefreshToken)
			r.Post("/my/token/toggle", s.apiMyToggleToken)
		})

		r.Group(func(r chi.Router) {
			r.Use(s.requireAPIAuth)
			r.Post("/my/username", s.apiChangeUsername)
			r.Get("/ws/speed", s.apiSpeedWS)
		})
	})

	// Public API (token auth)
	r.Route("/api/v1", func(r chi.Router) {
		r.Use(s.requireTokenAuth)
		r.Get("/info", s.apiTokenInfo)
	})

	r.NotFound(spaHandler().ServeHTTP)

	return r
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	if err := s.DB.PingContext(r.Context()); err != nil {
		http.Error(w, "db: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintln(w, `{"ok":true}`)
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s", r.Method, r.URL.Path)
		next.ServeHTTP(w, r)
	})
}
