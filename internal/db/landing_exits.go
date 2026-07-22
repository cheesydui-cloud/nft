package db

import (
	"database/sql"
	"strings"
)

// LandingExit is one row of a user's materialized landing-exit set plus its
// traffic ledger. Present=false rows are exits that dropped out of the landing
// source; their quota/used are kept so a returning exit resumes seamlessly.
// URI is server-internal (relay-URI rewriting); it never serializes into
// admin-facing JSON.
type LandingExit struct {
	UserID       int64  `json:"user_id"`
	Host         string `json:"host"`
	Port         int    `json:"port"`
	Name         string `json:"name"`
	NameOverride string `json:"name_override"`
	Protocol     string `json:"protocol"`
	URI          string `json:"-"`
	Present      bool   `json:"present"`
	QuotaBytes   int64  `json:"quota_bytes"`
	UsedBytes    int64  `json:"used_bytes"`
	UpdatedAt    int64  `json:"updated_at"`
	ExpiresAt    int64  `json:"expires_at"`
	Source       string `json:"source"`
}

// LandingExitInput is a deduplicated landing node destined for the
// materialized set (a plain struct so this package stays decoupled from the
// landing parser).
type LandingExitInput struct {
	Host     string
	Port     int
	Name     string
	Protocol string
	URI      string
}

// LandingExitKey addresses one exit within a user's set.
type LandingExitKey struct {
	Host string
	Port int
}

// UserExitKey addresses one exit ledger row across users.
type UserExitKey struct {
	UserID int64
	Host   string
	Port   int
}

// SyncUserLandingExits materializes a successfully resolved landing set.
// Inputs must already be deduplicated by host:port (first wins, manual URIs
// preceding subscription nodes). Rows missing from the input are swept if their
// ledger is empty (quota==0 && used==0), since present=0 retention exists only
// to resume a returning exit's quota/usage and an empty ledger has nothing to
// resume; ledger-bearing rows flip to present=0 instead so their quota keeps
// enforcing and usage survives. quota/used are never touched here. srcSubURL/srcURIs
// are the source values the resolution ran against: if the users row no
// longer matches (the admin changed the source during a slow subscription
// fetch), the stale result is discarded with synced=false. The returned keys
// flipped presence while at/over quota — their push-exclusion state changed,
// so the caller must re-dispatch the rules pointed at them.
func SyncUserLandingExits(d *sql.DB, userID int64, exits []LandingExitInput, srcSubURL, srcURIs string) (flipped []LandingExitKey, synced bool, err error) {
	tx, err := d.Begin()
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()

	var curSub, curURIs string
	if err := tx.QueryRow(`SELECT landing_sub_url, landing_uris FROM users WHERE id=?`, userID).Scan(&curSub, &curURIs); err != nil {
		return nil, false, err
	}
	if curSub != srcSubURL || curURIs != srcURIs {
		return nil, false, nil
	}

	type rowState struct {
		present     bool
		overQuota   bool
		emptyLedger bool
		source      string
	}
	existing := map[LandingExitKey]rowState{}
	rows, err := tx.Query(`SELECT host, port, present, quota_bytes, used_bytes, source FROM user_landing_exits WHERE user_id=?`, userID)
	if err != nil {
		return nil, false, err
	}
	for rows.Next() {
		var k LandingExitKey
		var present int
		var quota, used int64
		var source string
		if err := rows.Scan(&k.Host, &k.Port, &present, &quota, &used, &source); err != nil {
			rows.Close()
			return nil, false, err
		}
		existing[k] = rowState{present: present == 1, overQuota: quota > 0 && used >= quota, emptyLedger: quota == 0 && used == 0, source: source}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, false, err
	}
	rows.Close()

	nowTs := now()
	inInput := map[LandingExitKey]bool{}
	for _, e := range exits {
		k := LandingExitKey{Host: e.Host, Port: e.Port}
		if inInput[k] {
			continue
		}
		inInput[k] = true
		if _, err := tx.Exec(`INSERT INTO user_landing_exits(user_id, host, port, name, protocol, uri, present, updated_at, source)
			VALUES (?,?,?,?,?,?,1,?,?)
			ON CONFLICT(user_id, host, port) DO UPDATE SET name=excluded.name, protocol=excluded.protocol, uri=excluded.uri, present=1, updated_at=excluded.updated_at, source=excluded.source`,
			userID, e.Host, e.Port, e.Name, e.Protocol, e.URI, nowTs, "auto"); err != nil {
			return nil, false, err
		}
		if st, ok := existing[k]; ok && !st.present && st.overQuota {
			flipped = append(flipped, k)
		}
	}
	for k, st := range existing {
		if inInput[k] {
			continue
		}
		// Repo-imported nodes are never swept by source sync.
		if st.source == "repo" {
			continue
		}
		// Dropped out of the source. An empty ledger has nothing to resume, so
		// sweep it rather than leave a stale "not in source" row — this also
		// reaches rows already at present=0 whose ledger was later cleared.
		if st.emptyLedger {
			if _, err := tx.Exec(`DELETE FROM user_landing_exits WHERE user_id=? AND host=? AND port=?`,
				userID, k.Host, k.Port); err != nil {
				return nil, false, err
			}
			continue
		}
		if !st.present {
			continue
		}
		if _, err := tx.Exec(`UPDATE user_landing_exits SET present=0, updated_at=? WHERE user_id=? AND host=? AND port=?`,
			nowTs, userID, k.Host, k.Port); err != nil {
			return nil, false, err
		}
		if st.overQuota {
			flipped = append(flipped, k)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	return flipped, true, nil
}

// AppendUserLandingExits adds new landing exits to a user without removing
// existing ones. Only inserts/updates entries from the input; does NOT sweep
// or flip presence of rows absent from the input. Used by the node-pool
// assignment flow so importing repo nodes doesn't wipe subscription/manual exits.
// Returns keys that flipped from present=0 to present=1 while at/over quota.
func AppendUserLandingExits(d *sql.DB, userID int64, exits []LandingExitInput) ([]LandingExitKey, error) {
	tx, err := d.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// Check existing state for flip detection
	type rowState struct {
		present   bool
		overQuota bool
	}
	existing := map[LandingExitKey]rowState{}
	rows, err := tx.Query(`SELECT host, port, present, quota_bytes, used_bytes FROM user_landing_exits WHERE user_id=?`, userID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var k LandingExitKey
		var present int
		var quota, used int64
		if err := rows.Scan(&k.Host, &k.Port, &present, &quota, &used); err != nil {
			rows.Close()
			return nil, err
		}
		existing[k] = rowState{present: present == 1, overQuota: quota > 0 && used >= quota}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	nowTs := now()
	seen := map[LandingExitKey]bool{}
	var flipped []LandingExitKey
	for _, e := range exits {
		k := LandingExitKey{Host: e.Host, Port: e.Port}
		if seen[k] {
			continue
		}
		seen[k] = true
		if _, err := tx.Exec(`INSERT INTO user_landing_exits(user_id, host, port, name, protocol, uri, present, updated_at, source)
			VALUES (?,?,?,?,?,?,1,?,?)
			ON CONFLICT(user_id, host, port) DO UPDATE SET name=excluded.name, protocol=excluded.protocol, uri=excluded.uri, present=1, updated_at=excluded.updated_at, source=excluded.source`,
			userID, e.Host, e.Port, e.Name, e.Protocol, e.URI, nowTs, "repo"); err != nil {
			return nil, err
		}
		if st, ok := existing[k]; ok && !st.present && st.overQuota {
			flipped = append(flipped, k)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return flipped, nil
}

const landingExitCols = `user_id, host, port, name, name_override, protocol, uri, present, quota_bytes, used_bytes, updated_at, expires_at, source`

func scanLandingExit(r rowScanner) (*LandingExit, error) {
	e := &LandingExit{}
	var present int
	var expiresAt sql.NullInt64
	if err := r.Scan(&e.UserID, &e.Host, &e.Port, &e.Name, &e.NameOverride, &e.Protocol, &e.URI, &present, &e.QuotaBytes, &e.UsedBytes, &e.UpdatedAt, &expiresAt, &e.Source); err != nil {
		return nil, err
	}
	e.Present = present == 1
	if expiresAt.Valid {
		e.ExpiresAt = expiresAt.Int64
	}
	return e, nil
}

// ListUserLandingExits returns the user's full materialized set, present rows
// first, for the admin quota card.
func ListUserLandingExits(d *sql.DB, userID int64) ([]*LandingExit, error) {
	return queryAll(d, `SELECT `+landingExitCols+` FROM user_landing_exits WHERE user_id=? ORDER BY present DESC, name, host, port`,
		scanLandingExit, userID)
}

// PresentLandingExitsForUser returns only the rows that drive classification,
// metering and push exclusion.
func PresentLandingExitsForUser(d *sql.DB, userID int64) ([]*LandingExit, error) {
	return queryAll(d, `SELECT `+landingExitCols+` FROM user_landing_exits WHERE user_id=? AND present=1 ORDER BY name, host, port`,
		scanLandingExit, userID)
}

// PresentLandingExitSet returns the present (user, host, port) triples for the
// given users — the per-batch lookup applyCounters classifies samples against.
func PresentLandingExitSet(d *sql.DB, userIDs []int64) (map[UserExitKey]bool, error) {
	out := map[UserExitKey]bool{}
	if len(userIDs) == 0 {
		return out, nil
	}
	ph := strings.Repeat("?,", len(userIDs)-1) + "?"
	args := make([]any, len(userIDs))
	for i, id := range userIDs {
		args[i] = id
	}
	rows, err := d.Query(`SELECT user_id, host, port FROM user_landing_exits WHERE present=1 AND user_id IN (`+ph+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var k UserExitKey
		if err := rows.Scan(&k.UserID, &k.Host, &k.Port); err != nil {
			return nil, err
		}
		out[k] = true
	}
	return out, rows.Err()
}

// MaxHopPositions returns each rule's final hop position. Only the final hop
// meters into the exit ledger: middle hops target system relay addresses,
// which must never be mistaken for the user's destination.
func MaxHopPositions(d *sql.DB, ruleIDs []int64) (map[int64]int, error) {
	out := map[int64]int{}
	if len(ruleIDs) == 0 {
		return out, nil
	}
	ph := strings.Repeat("?,", len(ruleIDs)-1) + "?"
	args := make([]any, len(ruleIDs))
	for i, id := range ruleIDs {
		args[i] = id
	}
	rows, err := d.Query(`SELECT rule_id, MAX(position) FROM rule_hops WHERE rule_id IN (`+ph+`) GROUP BY rule_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var pos int
		if err := rows.Scan(&id, &pos); err != nil {
			return nil, err
		}
		out[id] = pos
	}
	return out, rows.Err()
}

// exitRowPresent reports whether the row exists and is present. found=false
// means no such row.
func exitRowPresent(d *sql.DB, userID int64, host string, port int) (found, present bool, err error) {
	var p int
	err = d.QueryRow(`SELECT present FROM user_landing_exits WHERE user_id=? AND host=? AND port=?`, userID, host, port).Scan(&p)
	if err == sql.ErrNoRows {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	return true, p == 1, nil
}

// SetUserLandingExitQuota updates one exit's quota (0 = unlimited). present
// tells the caller whether a re-dispatch is warranted — present=0 residual
// rows sit outside the push exclusion.
func SetUserLandingExitQuota(d *sql.DB, userID int64, host string, port int, quota int64) (updated, present bool, err error) {
	found, present, err := exitRowPresent(d, userID, host, port)
	if err != nil || !found {
		return false, false, err
	}
	_, err = d.Exec(`UPDATE user_landing_exits SET quota_bytes=?, updated_at=? WHERE user_id=? AND host=? AND port=?`,
		quota, now(), userID, host, port)
	return err == nil, present, err
}

// ResetUserLandingExitTraffic zeroes one exit's ledger.
func ResetUserLandingExitTraffic(d *sql.DB, userID int64, host string, port int) (updated, present bool, err error) {
	found, present, err := exitRowPresent(d, userID, host, port)
	if err != nil || !found {
		return false, false, err
	}
	_, err = d.Exec(`UPDATE user_landing_exits SET used_bytes=0, updated_at=? WHERE user_id=? AND host=? AND port=?`,
		now(), userID, host, port)
	return err == nil, present, err
}

// DeleteUserLandingExit removes a residual (present=0) row by default.
// When force=true it also deletes present=1 rows — used by admin delete so
// manually imported/repo nodes can be removed. Returns (status, wasPresent, error).
func DeleteUserLandingExit(d *sql.DB, userID int64, host string, port int, force bool) (string, bool, error) {
	found, present, err := exitRowPresent(d, userID, host, port)
	if err != nil {
		return "", false, err
	}
	if !found {
		return "notfound", false, nil
	}
	if present && !force {
		return "present", true, nil
	}
	if _, err := d.Exec(`DELETE FROM user_landing_exits WHERE user_id=? AND host=? AND port=?`, userID, host, port); err != nil {
		return "", present, err
	}
	return "deleted", present, nil
}

// ExitsExceedingQuota returns the user's present exits whose ledger reached
// quota. Quota 0 (unlimited) never exceeds.
func ExitsExceedingQuota(d *sql.DB, userID int64) ([]LandingExitKey, error) {
	rows, err := d.Query(`SELECT host, port FROM user_landing_exits
		WHERE user_id=? AND present=1 AND quota_bytes>0 AND used_bytes>=quota_bytes`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LandingExitKey
	for rows.Next() {
		var k LandingExitKey
		if err := rows.Scan(&k.Host, &k.Port); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// ExpiredLandingExits returns present exits whose expires_at is non-zero
// and in the past. These should be auto-disabled.
func ExpiredLandingExits(d *sql.DB) ([]UserExitKey, error) {
	rows, err := d.Query(`SELECT user_id, host, port FROM user_landing_exits
		WHERE present=1 AND expires_at > 0 AND expires_at <= strftime('%s','now')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UserExitKey
	for rows.Next() {
		var k UserExitKey
		if err := rows.Scan(&k.UserID, &k.Host, &k.Port); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// NodesForUserExit returns the distinct physical hop nodes of the user's rules
// that exit to host:port. Composite entries are already expanded into physical
// hops in rule_hops; composite virtual nodes have no agent connection and must
// never enter a dispatch set.
func NodesForUserExit(d *sql.DB, userID int64, host string, port int) ([]int64, error) {
	return queryInt64s(d, `
		SELECT DISTINCT rh.node_id
		FROM rule_hops rh
		JOIN rules r ON r.id = rh.rule_id
		WHERE r.owner_id=? AND r.exit_host=? AND r.exit_port=?`, userID, host, port)
}

// SetUserLandingExitName sets or clears (name == "") one exit's display-name
// override. The override lives outside SyncUserLandingExits so a subscription
// refresh cannot undo an admin rename; the parsed name column stays intact so
// clearing the override restores it. Renames never change push exclusion, so
// no re-dispatch hint is returned.
func SetUserLandingExitName(d *sql.DB, userID int64, host string, port int, name string) (updated bool, err error) {
	found, _, err := exitRowPresent(d, userID, host, port)
	if err != nil || !found {
		return false, err
	}
	_, err = d.Exec(`UPDATE user_landing_exits SET name_override=?, updated_at=? WHERE user_id=? AND host=? AND port=?`,
		name, now(), userID, host, port)
	return err == nil, err
}

// SetUserLandingExitExpires sets (or clears, with expiresAt==0) the expiry
// timestamp for one landing exit. 0 = never expire.
func SetUserLandingExitExpires(d *sql.DB, userID int64, host string, port int, expiresAt int64) (updated bool, err error) {
	found, _, err := exitRowPresent(d, userID, host, port)
	if err != nil || !found {
		return false, err
	}
	r, err := d.Exec(`UPDATE user_landing_exits SET expires_at=?, updated_at=? WHERE user_id=? AND host=? AND port=?`,
		expiresAt, now(), userID, host, port)
	if err != nil {
		return false, err
	}
	n, _ := r.RowsAffected()
	return n > 0, nil
}

// RepoExitPropagateResult summarizes cascading updates when a node-repo entry's
// address (or metadata) changes. EndpointChanged is true when host/port moved.
type RepoExitPropagateResult struct {
	EndpointChanged bool
	ExitsUpdated    int
	RulesUpdated    int
	// NodeIDs that carry affected rules — caller should redispatch them.
	NodeIDs []int64
	// Users whose landing exit endpoint moved (for per-exit redispatch helpers).
	MovedUsers []int64
	OldHost    string
	OldPort    int
	NewHost    string
	NewPort    int
}

// PropagateRepoExitChange rewrites every user_landing_exits row and every rule
// that still points at oldHost:oldPort so they follow a node-repo address change.
//
// Matching strategy (no foreign key from exits → repo):
//   - Landing exits: source='repo' AND host/port match the previous repo endpoint.
//   - Rules: exit_host/exit_port match that endpoint (any owner). Last-hop
//     rule_hops target is updated in lockstep so the data plane dials the new
//     address without a full RegenerateRule (listen ports stay put).
//
// When the new endpoint already exists for a user (PRIMARY KEY conflict), the
// old row is dropped after merging quota/used (sum) so history is not lost.
// Metadata-only edits (same host:port) still refresh name/protocol/uri/expires.
func PropagateRepoExitChange(d *sql.DB, oldHost, newHost string, oldPort, newPort int, name, protocol, uri string, expiresAt int64) (RepoExitPropagateResult, error) {
	out := RepoExitPropagateResult{
		OldHost: oldHost, OldPort: oldPort,
		NewHost: newHost, NewPort: newPort,
	}
	if oldHost == "" || oldPort == 0 || newHost == "" || newPort == 0 {
		return out, nil
	}
	out.EndpointChanged = oldHost != newHost || oldPort != newPort

	tx, err := d.Begin()
	if err != nil {
		return out, err
	}
	defer tx.Rollback()

	nowTs := now()

	// --- user_landing_exits (repo-sourced only) ---
	type exitRow struct {
		userID       int64
		name         string
		nameOverride string
		protocol     string
		uri          string
		present      int
		quota        int64
		used         int64
		expires      sql.NullInt64
		source       string
	}
	rows, err := tx.Query(`SELECT user_id, name, name_override, protocol, uri, present, quota_bytes, used_bytes, expires_at, source
		FROM user_landing_exits WHERE host=? AND port=? AND source='repo'`, oldHost, oldPort)
	if err != nil {
		return out, err
	}
	var olds []exitRow
	for rows.Next() {
		var e exitRow
		if err := rows.Scan(&e.userID, &e.name, &e.nameOverride, &e.protocol, &e.uri, &e.present, &e.quota, &e.used, &e.expires, &e.source); err != nil {
			rows.Close()
			return out, err
		}
		olds = append(olds, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}

	movedUsers := map[int64]bool{}
	for _, e := range olds {
		// Prefer admin rename: keep name_override; update source name from repo.
		srcName := name
		if srcName == "" {
			srcName = e.name
		}
		proto := protocol
		if proto == "" {
			proto = e.protocol
		}
		u := uri
		if u == "" {
			u = e.uri
		}
		exp := expiresAt
		if exp < 0 {
			if e.expires.Valid {
				exp = e.expires.Int64
			} else {
				exp = 0
			}
		}

		if !out.EndpointChanged {
			if _, err := tx.Exec(`UPDATE user_landing_exits SET name=?, protocol=?, uri=?, expires_at=?, updated_at=?
				WHERE user_id=? AND host=? AND port=? AND source='repo'`,
				srcName, proto, u, exp, nowTs, e.userID, oldHost, oldPort); err != nil {
				return out, err
			}
			out.ExitsUpdated++
			continue
		}

		// Endpoint moved. Merge into existing new key if present.
		var existQuota, existUsed int64
		var existPresent int
		var existNameOverride string
		err := tx.QueryRow(`SELECT quota_bytes, used_bytes, present, name_override FROM user_landing_exits
			WHERE user_id=? AND host=? AND port=?`, e.userID, newHost, newPort).
			Scan(&existQuota, &existUsed, &existPresent, &existNameOverride)
		if err == sql.ErrNoRows {
			if _, err := tx.Exec(`UPDATE user_landing_exits SET host=?, port=?, name=?, protocol=?, uri=?, expires_at=?, updated_at=?, source='repo'
				WHERE user_id=? AND host=? AND port=? AND source='repo'`,
				newHost, newPort, srcName, proto, u, exp, nowTs, e.userID, oldHost, oldPort); err != nil {
				return out, err
			}
			out.ExitsUpdated++
			movedUsers[e.userID] = true
			continue
		}
		if err != nil {
			return out, err
		}
		// Conflict: keep higher quota, sum used, prefer existing name_override then old.
		quota := existQuota
		if e.quota > quota {
			quota = e.quota
		}
		used := existUsed + e.used
		override := existNameOverride
		if override == "" {
			override = e.nameOverride
		}
		present := existPresent
		if e.present == 1 {
			present = 1
		}
		if _, err := tx.Exec(`UPDATE user_landing_exits SET name=?, name_override=?, protocol=?, uri=?, present=?,
			quota_bytes=?, used_bytes=?, expires_at=?, updated_at=?, source='repo'
			WHERE user_id=? AND host=? AND port=?`,
			srcName, override, proto, u, present, quota, used, exp, nowTs, e.userID, newHost, newPort); err != nil {
			return out, err
		}
		if _, err := tx.Exec(`DELETE FROM user_landing_exits WHERE user_id=? AND host=? AND port=?`,
			e.userID, oldHost, oldPort); err != nil {
			return out, err
		}
		out.ExitsUpdated++
		movedUsers[e.userID] = true
	}

	// --- rules + last-hop rule_hops ---
	// Always rewrite rules that still dial the old endpoint (covers repo-imported
	// exits used as rule targets). Metadata-only: no rule change needed when host:port
	// unchanged — hops already point correctly.
	if out.EndpointChanged {
		res, err := tx.Exec(`UPDATE rules SET exit_host=?, exit_port=? WHERE exit_host=? AND exit_port=?`,
			newHost, newPort, oldHost, oldPort)
		if err != nil {
			return out, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			out.RulesUpdated = int(n)
		}
		// Last hop only: intermediate hops target the next relay, not the exit.
		if _, err := tx.Exec(`UPDATE rule_hops SET target_host=?, target_port=?
			WHERE target_host=? AND target_port=?
			  AND position = (SELECT MAX(h2.position) FROM rule_hops h2 WHERE h2.rule_id = rule_hops.rule_id)`,
			newHost, newPort, oldHost, oldPort); err != nil {
			return out, err
		}
	}

	// Data-plane targets only change when host/port moved; metadata-only
	// edits (name/uri/expires) stay panel-side and need no agent push.
	if out.EndpointChanged {
		nodeRows, err := tx.Query(`
			SELECT DISTINCT rh.node_id
			FROM rule_hops rh
			JOIN rules r ON r.id = rh.rule_id
			WHERE r.exit_host=? AND r.exit_port=?`, newHost, newPort)
		if err != nil {
			return out, err
		}
		seenN := map[int64]bool{}
		for nodeRows.Next() {
			var nid int64
			if err := nodeRows.Scan(&nid); err != nil {
				nodeRows.Close()
				return out, err
			}
			if !seenN[nid] {
				seenN[nid] = true
				out.NodeIDs = append(out.NodeIDs, nid)
			}
		}
		nodeRows.Close()
		if err := nodeRows.Err(); err != nil {
			return out, err
		}
	}

	for uid := range movedUsers {
		out.MovedUsers = append(out.MovedUsers, uid)
	}

	if err := tx.Commit(); err != nil {
		return out, err
	}
	return out, nil
}


// RepoExitUser is one user who has a present, repo-sourced landing exit at host:port.
type RepoExitUser struct {
	UserID       int64  `json:"user_id"`
	Username     string `json:"username"`
	NameOverride string `json:"name_override"`
	Name         string `json:"name"`
	QuotaBytes   int64  `json:"quota_bytes"`
	UsedBytes    int64  `json:"used_bytes"`
	ExpiresAt    int64  `json:"expires_at"`
	// RuleCount is how many of this user's rules exit to the same host:port.
	RuleCount int `json:"rule_count"`
}

// CountRepoExitUsers returns the number of distinct users with a present
// source=repo landing exit for each host:port key ("host:port" → count).
// Used to annotate the node-repo list without N+1 queries.
func CountRepoExitUsers(d *sql.DB) (map[string]int, error) {
	rows, err := d.Query(`SELECT host, port, COUNT(*) FROM user_landing_exits
		WHERE source='repo' AND present=1 GROUP BY host, port`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var host string
		var port, n int
		if err := rows.Scan(&host, &port, &n); err != nil {
			return nil, err
		}
		out[host+":"+itoa(int64(port))] = n
	}
	return out, rows.Err()
}

// ListRepoExitUsers returns users who currently hold a present, repo-sourced
// landing exit at host:port, ordered by username. RuleCount is filled from
// rules owned by those users that still dial the same exit.
func ListRepoExitUsers(d *sql.DB, host string, port int) ([]RepoExitUser, error) {
	rows, err := d.Query(`
		SELECT e.user_id, u.username, e.name, e.name_override, e.quota_bytes, e.used_bytes, e.expires_at,
			(SELECT COUNT(*) FROM rules r WHERE r.owner_id=e.user_id AND r.exit_host=e.host AND r.exit_port=e.port)
		FROM user_landing_exits e
		JOIN users u ON u.id = e.user_id
		WHERE e.host=? AND e.port=? AND e.source='repo' AND e.present=1
		ORDER BY u.username COLLATE NOCASE`, host, port)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RepoExitUser
	for rows.Next() {
		var u RepoExitUser
		var exp sql.NullInt64
		if err := rows.Scan(&u.UserID, &u.Username, &u.Name, &u.NameOverride, &u.QuotaBytes, &u.UsedBytes, &exp, &u.RuleCount); err != nil {
			return nil, err
		}
		if exp.Valid {
			u.ExpiresAt = exp.Int64
		}
		out = append(out, u)
	}
	if out == nil {
		out = []RepoExitUser{}
	}
	return out, rows.Err()
}
