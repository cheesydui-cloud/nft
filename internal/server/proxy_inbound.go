package server

import (
	"database/sql"
	"encoding/json"
	"log"

	"nft/internal/db"
	"nft/internal/proxysvc"
)

// ensureUserProxyCred returns the user's inbound secret for a service,
// minting one on first use. The first stored cred on a service inherits
// the published template so existing share URIs keep working.
func (s *Server) ensureUserProxyCred(userID, serviceID int64, protocol string, template json.RawMessage) (*db.UserProxyCred, error) {
	if c, err := db.GetUserProxyCred(s.DB, userID, serviceID); err == nil && c != nil {
		return c, nil
	} else if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	uuid, username, password := proxysvc.MintUserSecret(protocol, template)
	if n, err := db.CountUserProxyCreds(s.DB, serviceID); err == nil && n == 0 {
		tu, tuser, tpass := proxysvc.TemplateSecret(protocol, template)
		if tu != "" {
			uuid = tu
		}
		if tuser != "" {
			username = tuser
		}
		if tpass != "" {
			password = tpass
		}
	}
	return db.EnsureUserProxyCred(s.DB, userID, serviceID, uuid, username, password)
}

func inboundFromCred(c *db.UserProxyCred) proxysvc.InboundClient {
	if c == nil {
		return proxysvc.InboundClient{}
	}
	return proxysvc.InboundClient{UUID: c.UUID, Username: c.Username, Password: c.Password}
}

// overlayProxyConfigForPublish rebuilds inbound clients from users who still
// have an enabled rule on the service. Empty set = nobody can auth.
func (s *Server) overlayProxyConfigForPublish(svc *db.ProxyService, cfg json.RawMessage) (json.RawMessage, error) {
	if svc == nil {
		return cfg, nil
	}
	rows, err := db.ListActiveProxyClients(s.DB, svc.ID)
	if err != nil {
		return nil, err
	}
	live := make([]proxysvc.InboundClient, 0, len(rows))
	for _, row := range rows {
		cl := proxysvc.InboundClient{UUID: row.UUID, Username: row.Username, Password: row.Password}
		if cl.UUID == "" && cl.Username == "" && cl.Password == "" {
			cred, eerr := s.ensureUserProxyCred(row.UserID, svc.ID, svc.Protocol, cfg)
			if eerr != nil {
				log.Printf("proxy inbound: mint cred user=%d svc=%d: %v", row.UserID, svc.ID, eerr)
				continue
			}
			cl = inboundFromCred(cred)
		}
		live = append(live, cl)
	}
	return proxysvc.OverlayInboundClients(svc.Protocol, cfg, live)
}

// overlayProxyConfigForUser is a single-user overlay (rule-scoped entry plane
// and per-user share URI).
func (s *Server) overlayProxyConfigForUser(svc *db.ProxyService, cfg json.RawMessage, userID int64) (json.RawMessage, error) {
	if svc == nil || userID <= 0 {
		return cfg, nil
	}
	cred, err := s.ensureUserProxyCred(userID, svc.ID, svc.Protocol, cfg)
	if err != nil {
		return nil, err
	}
	cl := inboundFromCred(cred)
	overlaid, err := proxysvc.OverlayInboundClients(svc.Protocol, cfg, []proxysvc.InboundClient{cl})
	if err != nil {
		return nil, err
	}
	return proxysvc.ConfigWithUserSecret(svc.Protocol, overlaid, cl), nil
}

func (s *Server) syncProxyInboundForService(serviceID int64) {
	if s == nil || serviceID <= 0 {
		return
	}
	if _, err := s.republishProxyServiceAll(serviceID); err != nil {
		log.Printf("proxy inbound: republish service %d: %v", serviceID, err)
	}
}

func (s *Server) syncProxyInboundForServices(ids []int64) {
	seen := map[int64]bool{}
	for _, id := range ids {
		if id <= 0 || seen[id] {
			continue
		}
		seen[id] = true
		s.syncProxyInboundForService(id)
	}
}

func (s *Server) syncProxyInboundForUser(userID int64) {
	if userID <= 0 {
		return
	}
	ids, err := db.ListProxyServiceIDsForUserRules(s.DB, userID)
	if err != nil {
		log.Printf("proxy inbound: list user %d services: %v", userID, err)
		return
	}
	s.syncProxyInboundForServices(ids)
}

func ruleProxyServiceID(r *db.Rule) int64 {
	if r == nil {
		return 0
	}
	return r.ProxyServiceID
}
