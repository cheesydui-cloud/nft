package server

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/coder/websocket"
)

func (s *Server) apiSpeedWS(w http.ResponseWriter, r *http.Request) {
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		return
	}
	defer ws.CloseNow()

	ctx := r.Context()

	// Admins see each node's total throughput; a regular user sees only their
	// own share of every node, so the granted-nodes list reflects the user's
	// own speed rather than everyone's traffic on the node.
	actor := userFromCtx(ctx)
	perUser := actor != nil && actor.Role != "admin"

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			var snap []SpeedEntry
			var rules []RuleSpeedEntry
			if perUser {
				snap = s.Hub.speedCache.snapshotForUser(actor.ID)
				rules = s.Hub.speedCache.snapshotRulesForUser(actor.ID)
			} else {
				snap = s.Hub.speedCache.snapshot()
				rules = s.Hub.speedCache.snapshotRules()
			}
			data, err := json.Marshal(map[string]any{"speeds": snap, "rule_speeds": rules})
			if err != nil {
				continue
			}
			if err := ws.Write(ctx, websocket.MessageText, data); err != nil {
				return
			}
		}
	}
}
