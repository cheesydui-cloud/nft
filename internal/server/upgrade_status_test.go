package server

import (
	"database/sql"
	"testing"
	"time"

	"nft/internal/db"
)

func TestDeriveUpgradeStatus(t *testing.T) {
	base := time.Unix(1_000_000, 0)
	mk := func(at int64, ver, status, errText, agent, sha string) *db.Node {
		n := &db.Node{AgentVersion: agent, AgentSHA: sha, LastUpgradeVersion: ver, LastUpgradeStatus: status, LastUpgradeError: errText}
		if at > 0 {
			n.LastUpgradeAt = sql.NullInt64{Int64: at, Valid: true}
		}
		return n
	}
	const latestSHA = "abc123"
	cases := []struct {
		name       string
		node       *db.Node
		server     string
		latestSHA  string
		now        time.Time
		want       string
	}{
		{"never", mk(0, "", "", "", "v1.0.0", ""), "v1.0.0", latestSHA, base, "none"},
		{"ok exact", mk(base.Unix(), "v0.32.2", "acked", "", "v0.32.2", ""), "v0.32.4", latestSHA, base, "ok"},
		{"at server version hides push", mk(base.Unix(), "v0.32.2", "acked", "", "v0.32.4", ""), "v0.32.4", latestSHA, base, "none"},
		{"error", mk(base.Unix(), "v0.32.2", "error", "节点未连接", "v0.32.1", ""), "v0.32.4", latestSHA, base, "error"},
		{"pending within grace", mk(base.Unix(), "v0.32.2", "acked", "", "v0.32.1", ""), "v0.32.4", latestSHA, base.Add(30 * time.Second), "pending"},
		{"stuck past grace", mk(base.Unix(), "v0.32.2", "acked", "", "v0.32.1", ""), "v0.32.4", latestSHA, base.Add(5 * time.Minute), "stuck"},
		{"current hides stale error", mk(base.Unix(), "v0.32.2", "error", "节点未连接", "v0.32.4", ""), "v0.32.4", latestSHA, base, "none"},
		{"newer than server hides stale", mk(base.Unix(), "v0.32.2", "acked", "", "v0.33.0", ""), "v0.32.4", latestSHA, base, "none"},
		{"latest label with matching sha hides error", mk(base.Unix(), "v1.0.2", "error", "连接断开", "latest", latestSHA), "v1.0.2", latestSHA, base, "none"},
		{"latest label with mismatched sha shows error", mk(base.Unix(), "v1.0.2", "error", "连接断开", "latest", "oldsha"), "v1.0.2", latestSHA, base, "error"},
	}
	for _, tc := range cases {
		got := deriveUpgradeStatus(tc.node, tc.server, tc.latestSHA, tc.now)
		if got.Status != tc.want {
			t.Errorf("%s: status=%q want %q", tc.name, got.Status, tc.want)
		}
	}
}
