package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Proxy service protocol / core constants (wire + DB values).
const (
	ProxyProtoVLESS       = "vless"
	ProxyProtoShadowsocks = "shadowsocks"
	ProxyProtoMieru       = "mieru"
	ProxyProtoSocks5      = "socks5"
	ProxyProtoAnyTLS      = "anytls"
	ProxyProtoNaive       = "naive"
	ProxyCoreXray         = "xray"
	ProxyCoreSingBox      = "sing-box"
	ProxyCoreMieru        = "mieru"
	ProxyStatusDraft      = "draft"
	ProxyStatusReady      = "ready"
	ProxyStatusPartial    = "partial"
	ProxyStatusError      = "error"
	ProxyDeployPending    = "pending"
	ProxyDeployReady      = "ready"
	ProxyDeployError      = "error"
	ProxyDeployOffline    = "offline"
)

// ProxyService is an admin-published protocol template (Weir-style).
type ProxyService struct {
	ID         int64           `json:"id"`
	Name       string          `json:"name"`
	Protocol   string          `json:"protocol"`
	Core       string          `json:"core"`
	ConfigJSON json.RawMessage `json:"config_json"`
	SubVisible bool            `json:"sub_visible"`
	Status     string          `json:"status"`
	CreatedAt  int64           `json:"created_at"`
	UpdatedAt  int64           `json:"updated_at"`
	// Filled by list/detail APIs, not always loaded from DB.
		InstanceCount    int                     `json:"instance_count,omitempty"`
		ReadyCount       int                     `json:"ready_count,omitempty"`
		DeployedNodeIDs  []int64                 `json:"deployed_node_ids,omitempty"`
		Instances        []*ProxyServiceInstance `json:"instances,omitempty"`
	}

// ProxyServiceInstance is one deployment of a service onto a line node.
type ProxyServiceInstance struct {
	ID           int64  `json:"id"`
	ServiceID    int64  `json:"service_id"`
	NodeID       int64  `json:"node_id"`
	ListenPort   int    `json:"listen_port"`
	ShareHost    string `json:"share_host"`
	URI          string `json:"uri"`
	DeployStatus string `json:"deploy_status"`
	LastError    string `json:"last_error"`
	CoreVersion  string `json:"core_version"`
	SyncedRepoID int64  `json:"synced_repo_id"`
	// TrafficUp/DownBytes are cumulative raw bytes from agent proxy_counters.
	TrafficUpBytes   int64 `json:"traffic_up_bytes"`
	TrafficDownBytes int64 `json:"traffic_down_bytes"`
	TrafficUpdatedAt int64 `json:"traffic_updated_at"`
	CreatedAt        int64 `json:"created_at"`
	UpdatedAt        int64 `json:"updated_at"`
	// Optional join fields for API views.
	NodeName   string `json:"node_name,omitempty"`
	NodeOnline int    `json:"node_online,omitempty"`
}

// NodeCore is one detected proxy core on an agent host.
type NodeCore struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
	Path    string `json:"path,omitempty"`
}

const proxyServiceCols = `id, name, protocol, core, config_json, sub_visible, status, created_at, updated_at`
const proxyInstanceCols = `id, service_id, node_id, listen_port, share_host, uri, deploy_status, last_error, core_version, synced_repo_id, COALESCE(traffic_up_bytes,0), COALESCE(traffic_down_bytes,0), COALESCE(traffic_updated_at,0), created_at, updated_at`

func scanProxyService(r rowScanner) (*ProxyService, error) {
	s := &ProxyService{}
	var sub int
	var cfg string
	if err := r.Scan(&s.ID, &s.Name, &s.Protocol, &s.Core, &cfg, &sub, &s.Status, &s.CreatedAt, &s.UpdatedAt); err != nil {
		return nil, err
	}
	s.SubVisible = sub == 1
	if cfg == "" {
		cfg = "{}"
	}
	s.ConfigJSON = json.RawMessage(cfg)
	return s, nil
}

func scanProxyInstance(r rowScanner) (*ProxyServiceInstance, error) {
	inst := &ProxyServiceInstance{}
	if err := r.Scan(
		&inst.ID, &inst.ServiceID, &inst.NodeID, &inst.ListenPort, &inst.ShareHost, &inst.URI,
		&inst.DeployStatus, &inst.LastError, &inst.CoreVersion, &inst.SyncedRepoID,
		&inst.TrafficUpBytes, &inst.TrafficDownBytes, &inst.TrafficUpdatedAt,
		&inst.CreatedAt, &inst.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return inst, nil
}

// AddProxyInstanceTraffic folds a raw up/down delta into one instance ledger.
// instance must belong to nodeID so a misrouted sample cannot bill another node.
func AddProxyInstanceTraffic(d DBTX, instanceID, nodeID, up, down int64) error {
	if up == 0 && down == 0 {
		return nil
	}
	now := time.Now().Unix()
	res, err := d.Exec(`UPDATE proxy_service_instances
		SET traffic_up_bytes = traffic_up_bytes + ?,
		    traffic_down_bytes = traffic_down_bytes + ?,
		    traffic_updated_at = ?
		WHERE id=? AND node_id=?`,
		up, down, now, instanceID, nodeID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("proxy instance %d not on node %d", instanceID, nodeID)
	}
	return nil
}

// ResetProxyInstanceTraffic zeroes one instance's traffic counters (admin).
func ResetProxyInstanceTraffic(d *sql.DB, instanceID int64) error {
	_, err := d.Exec(`UPDATE proxy_service_instances
		SET traffic_up_bytes=0, traffic_down_bytes=0, traffic_updated_at=? WHERE id=?`,
		time.Now().Unix(), instanceID)
	return err
}

// DefaultCoreForProtocol returns the recommended core for a protocol template.
func DefaultCoreForProtocol(protocol string) string {
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case ProxyProtoVLESS:
		return ProxyCoreXray
	case ProxyProtoShadowsocks, "ss":
		return ProxyCoreSingBox
	case ProxyProtoMieru:
		return ProxyCoreMieru
	case ProxyProtoSocks5, "socks":
		return ProxyCoreSingBox
	case ProxyProtoAnyTLS:
		return ProxyCoreSingBox
	case ProxyProtoNaive, "naiveproxy":
		return ProxyCoreSingBox
	default:
		return ""
	}
}

// NormalizeProxyProtocol folds aliases onto canonical DB values.
func NormalizeProxyProtocol(p string) string {
	switch strings.ToLower(strings.TrimSpace(p)) {
	case "ss", "shadowsocks":
		return ProxyProtoShadowsocks
	case "vless":
		return ProxyProtoVLESS
	case "mieru":
		return ProxyProtoMieru
	case "socks", "socks5":
		return ProxyProtoSocks5
	case "anytls":
		return ProxyProtoAnyTLS
	case "naive", "naiveproxy":
		return ProxyProtoNaive
	default:
		return strings.ToLower(strings.TrimSpace(p))
	}
}

// ValidateProxyProtocolCore checks the allowed protocol×core matrix.
func ValidateProxyProtocolCore(protocol, core string) error {
	p := NormalizeProxyProtocol(protocol)
	c := strings.ToLower(strings.TrimSpace(core))
	switch p {
	case ProxyProtoVLESS:
		if c != ProxyCoreXray {
			return fmt.Errorf("VLESS 一期仅支持核心 xray")
		}
	case ProxyProtoShadowsocks:
		if c != ProxyCoreSingBox {
			return fmt.Errorf("Shadowsocks 一期仅支持核心 sing-box")
		}
	case ProxyProtoMieru:
		if c != ProxyCoreMieru && c != "mbox" && c != "mbox (mieru)" {
			// Accept mbox aliases from UI; store as mieru.
			if c != ProxyCoreMieru {
				return fmt.Errorf("mieru 仅支持核心 mbox/mieru")
			}
		}
	case ProxyProtoSocks5:
		if c != ProxyCoreSingBox {
			return fmt.Errorf("SOCKS5 仅支持核心 sing-box")
		}
	case ProxyProtoAnyTLS:
		if c != ProxyCoreSingBox {
			return fmt.Errorf("AnyTLS 仅支持核心 sing-box（≥1.12）")
		}
	case ProxyProtoNaive:
		if c != ProxyCoreSingBox {
			return fmt.Errorf("Naive 仅支持核心 sing-box（协议兼容实现，非 Caddy 原版）")
		}
	default:
		return fmt.Errorf("不支持的协议: %s", protocol)
	}
	return nil
}

// CanonicalCore normalizes core names for storage.
func CanonicalCore(core string) string {
	c := strings.ToLower(strings.TrimSpace(core))
	switch c {
	case "mbox", "mbox (mieru)", "mieru":
		return ProxyCoreMieru
	case "singbox", "sing-box":
		return ProxyCoreSingBox
	case "xray":
		return ProxyCoreXray
	default:
		return c
	}
}

// CreateProxyService inserts a draft service.
func CreateProxyService(d *sql.DB, name, protocol, core string, config json.RawMessage, subVisible bool) (*ProxyService, error) {
	protocol = NormalizeProxyProtocol(protocol)
	core = CanonicalCore(core)
	if err := ValidateProxyProtocolCore(protocol, core); err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("名称不能为空")
	}
	if len(config) == 0 {
		config = json.RawMessage(`{}`)
	}
	now := time.Now().Unix()
	sub := 0
	if subVisible {
		sub = 1
	}
	res, err := d.Exec(`INSERT INTO proxy_services (name, protocol, core, config_json, sub_visible, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		name, protocol, core, string(config), sub, ProxyStatusDraft, now, now)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return GetProxyService(d, id)
}

// UpdateProxyService rewrites name/config/sub_visible (protocol/core fixed after create).
func UpdateProxyService(d *sql.DB, id int64, name string, config json.RawMessage, subVisible bool) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("名称不能为空")
	}
	if len(config) == 0 {
		config = json.RawMessage(`{}`)
	}
	sub := 0
	if subVisible {
		sub = 1
	}
	_, err := d.Exec(`UPDATE proxy_services SET name=?, config_json=?, sub_visible=?, updated_at=? WHERE id=?`,
		name, string(config), sub, time.Now().Unix(), id)
	return err
}

// SetProxyServiceStatus updates aggregate status.
func SetProxyServiceStatus(d *sql.DB, id int64, status string) error {
	_, err := d.Exec(`UPDATE proxy_services SET status=?, updated_at=? WHERE id=?`, status, time.Now().Unix(), id)
	return err
}

// GetProxyService returns one service without instances.
func GetProxyService(d *sql.DB, id int64) (*ProxyService, error) {
	row := d.QueryRow(`SELECT `+proxyServiceCols+` FROM proxy_services WHERE id=?`, id)
	return scanProxyService(row)
}

// ListDeployedProxyNodeIDs returns distinct node_ids that host any proxy_service instance.
func ListDeployedProxyNodeIDs(d *sql.DB) ([]int64, error) {
	rows, err := d.Query(`SELECT DISTINCT node_id FROM proxy_service_instances WHERE node_id > 0 ORDER BY node_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// LookupProxyServiceShare looks up a ready share URI for serviceID on nodeID.
// Prefer ready instances with non-empty uri; fall back to any non-empty uri.
// Returns protocol, share URI, and service display name.
func LookupProxyServiceShare(d *sql.DB, serviceID, nodeID int64) (protocol, uri, name string, ok bool) {
	if serviceID <= 0 || nodeID <= 0 || d == nil {
		return "", "", "", false
	}
	var proto, svcName string
	if err := d.QueryRow(`SELECT protocol, name FROM proxy_services WHERE id=?`, serviceID).Scan(&proto, &svcName); err != nil {
		return "", "", "", false
	}
	var shareURI string
	err := d.QueryRow(`
		SELECT uri FROM proxy_service_instances
		WHERE service_id=? AND node_id=? AND TRIM(uri) != ''
		ORDER BY CASE WHEN deploy_status=? THEN 0 ELSE 1 END, id DESC
		LIMIT 1`, serviceID, nodeID, ProxyDeployReady).Scan(&shareURI)
	if err != nil || strings.TrimSpace(shareURI) == "" {
		return proto, "", svcName, false
	}
	return proto, strings.TrimSpace(shareURI), svcName, true
}

// ListProxyServices returns all services with instance counts.
func ListProxyServices(d *sql.DB) ([]*ProxyService, error) {
	rows, err := d.Query(`SELECT ` + proxyServiceCols + ` FROM proxy_services ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ProxyService
	for rows.Next() {
		s, err := scanProxyService(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
		for _, s := range out {
			_ = d.QueryRow(`SELECT COUNT(*), COALESCE(SUM(CASE WHEN deploy_status='ready' THEN 1 ELSE 0 END),0)
				FROM proxy_service_instances WHERE service_id=?`, s.ID).Scan(&s.InstanceCount, &s.ReadyCount)
			rows2, err := d.Query(`SELECT DISTINCT node_id FROM proxy_service_instances WHERE service_id=? AND node_id > 0 ORDER BY node_id`, s.ID)
			if err != nil {
				continue
			}
			var ids []int64
			for rows2.Next() {
				var nid int64
				if err := rows2.Scan(&nid); err != nil {
					continue
				}
				ids = append(ids, nid)
			}
			_ = rows2.Close()
			s.DeployedNodeIDs = ids
		}
		return out, nil
	}

// DeleteProxyService removes the service and its instances (FK cascade).
func DeleteProxyService(d *sql.DB, id int64) error {
	_, err := d.Exec(`DELETE FROM proxy_services WHERE id=?`, id)
	return err
}

// ListProxyInstances returns instances for a service, joined with node name/online.
func ListProxyInstances(d *sql.DB, serviceID int64) ([]*ProxyServiceInstance, error) {
	rows, err := d.Query(`
		SELECT i.id, i.service_id, i.node_id, i.listen_port, i.share_host, i.uri,
		       i.deploy_status, i.last_error, i.core_version, i.synced_repo_id, i.created_at, i.updated_at,
		       COALESCE(n.name,''), COALESCE(n.online,0)
		FROM proxy_service_instances i
		LEFT JOIN nodes n ON n.id = i.node_id
		WHERE i.service_id=?
		ORDER BY i.id`, serviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ProxyServiceInstance
	for rows.Next() {
		inst := &ProxyServiceInstance{}
		if err := rows.Scan(
			&inst.ID, &inst.ServiceID, &inst.NodeID, &inst.ListenPort, &inst.ShareHost, &inst.URI,
			&inst.DeployStatus, &inst.LastError, &inst.CoreVersion, &inst.SyncedRepoID,
			&inst.CreatedAt, &inst.UpdatedAt, &inst.NodeName, &inst.NodeOnline,
		); err != nil {
			return nil, err
		}
		out = append(out, inst)
	}
	return out, rows.Err()
}

// GetProxyInstance returns one instance by id.
func GetProxyInstance(d *sql.DB, id int64) (*ProxyServiceInstance, error) {
	row := d.QueryRow(`SELECT `+proxyInstanceCols+` FROM proxy_service_instances WHERE id=?`, id)
	return scanProxyInstance(row)
}

// UpsertProxyInstance creates or updates the instance row for (service, node).
func UpsertProxyInstance(d *sql.DB, serviceID, nodeID int64, listenPort int, shareHost string) (*ProxyServiceInstance, error) {
	now := time.Now().Unix()
	shareHost = strings.TrimSpace(shareHost)
	// Try update existing
	var id int64
	err := d.QueryRow(`SELECT id FROM proxy_service_instances WHERE service_id=? AND node_id=?`, serviceID, nodeID).Scan(&id)
	if err == sql.ErrNoRows {
		res, err := d.Exec(`INSERT INTO proxy_service_instances
			(service_id, node_id, listen_port, share_host, uri, deploy_status, last_error, core_version, synced_repo_id, created_at, updated_at)
			VALUES (?, ?, ?, ?, '', ?, '', '', 0, ?, ?)`,
			serviceID, nodeID, listenPort, shareHost, ProxyDeployPending, now, now)
		if err != nil {
			return nil, err
		}
		id, _ = res.LastInsertId()
		return GetProxyInstance(d, id)
	}
	if err != nil {
		return nil, err
	}
	_, err = d.Exec(`UPDATE proxy_service_instances SET listen_port=?, share_host=?, deploy_status=?, last_error='', updated_at=? WHERE id=?`,
		listenPort, shareHost, ProxyDeployPending, now, id)
	if err != nil {
		return nil, err
	}
	return GetProxyInstance(d, id)
}

// UpdateProxyInstanceDeploy records apply result from agent (or local dry-run).
func UpdateProxyInstanceDeploy(d *sql.DB, id int64, status, uri, lastErr, coreVersion string) error {
	_, err := d.Exec(`UPDATE proxy_service_instances SET deploy_status=?, uri=?, last_error=?, core_version=?, updated_at=? WHERE id=?`,
		status, uri, lastErr, coreVersion, time.Now().Unix(), id)
	return err
}

// UpdateProxyInstanceURI rewrites only the share URI (e.g. after config edit with encryption change).
func UpdateProxyInstanceURI(d *sql.DB, id int64, uri string) error {
	_, err := d.Exec(`UPDATE proxy_service_instances SET uri=?, updated_at=? WHERE id=?`,
		uri, time.Now().Unix(), id)
	return err
}

// SetProxyInstanceSyncedRepo links instance to a node_repo row.
func SetProxyInstanceSyncedRepo(d *sql.DB, id, repoID int64) error {
	_, err := d.Exec(`UPDATE proxy_service_instances SET synced_repo_id=?, updated_at=? WHERE id=?`,
		repoID, time.Now().Unix(), id)
	return err
}

// DeleteProxyInstance removes one instance row.
func DeleteProxyInstance(d *sql.DB, id int64) error {
	_, err := d.Exec(`DELETE FROM proxy_service_instances WHERE id=?`, id)
	return err
}

// SubExportNode is one ready, sub-visible proxy instance the user may import.
type SubExportNode struct {
	InstanceID int64  `json:"instance_id"`
	ServiceID  int64  `json:"service_id"`
	NodeID     int64  `json:"node_id"`
	Name       string `json:"name"`
	Protocol   string `json:"protocol"`
	URI        string `json:"uri"`
	ShareHost  string `json:"share_host"`
	ListenPort int    `json:"listen_port"`
	NodeName   string `json:"node_name"`
}

// ListSubVisibleReadyInstancesForUser returns published proxy instances that:
//   - belong to services with sub_visible=1
//   - are deploy_status=ready with non-empty uri
//   - run on a line node the user is granted (user_nodes)
func ListSubVisibleReadyInstancesForUser(d *sql.DB, userID int64) ([]SubExportNode, error) {
	rows, err := d.Query(`
		SELECT i.id, i.service_id, i.node_id, s.name, s.protocol, i.uri, i.share_host, i.listen_port,
		       COALESCE(n.name, '')
		FROM proxy_service_instances i
		JOIN proxy_services s ON s.id = i.service_id
		JOIN user_nodes g ON g.node_id = i.node_id AND g.user_id = ?
		LEFT JOIN nodes n ON n.id = i.node_id
		WHERE s.sub_visible = 1
		  AND i.deploy_status = ?
		  AND TRIM(i.uri) != ''
		ORDER BY s.name COLLATE NOCASE, n.name COLLATE NOCASE, i.id`,
		userID, ProxyDeployReady)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SubExportNode
	for rows.Next() {
		var n SubExportNode
		if err := rows.Scan(
			&n.InstanceID, &n.ServiceID, &n.NodeID, &n.Name, &n.Protocol,
			&n.URI, &n.ShareHost, &n.ListenPort, &n.NodeName,
		); err != nil {
			return nil, err
		}
		// Prefer "Service · Node" when multiple nodes; keep service name if single-ish.
		svc := strings.TrimSpace(n.Name)
		node := strings.TrimSpace(n.NodeName)
		if svc != "" && node != "" {
			n.Name = svc + " · " + node
		} else if svc != "" {
			n.Name = svc
		} else if node != "" {
			n.Name = node
		} else {
			n.Name = fmt.Sprintf("node-%d", n.NodeID)
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// RecomputeProxyServiceStatus sets service status from its instances.
func RecomputeProxyServiceStatus(d *sql.DB, serviceID int64) error {
	var total, ready, errN int
	err := d.QueryRow(`SELECT COUNT(*),
		COALESCE(SUM(CASE WHEN deploy_status='ready' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN deploy_status='error' THEN 1 ELSE 0 END),0)
		FROM proxy_service_instances WHERE service_id=?`, serviceID).Scan(&total, &ready, &errN)
	if err != nil {
		return err
	}
	status := ProxyStatusDraft
	switch {
	case total == 0:
		status = ProxyStatusDraft
	case ready == total:
		status = ProxyStatusReady
	case ready > 0:
		status = ProxyStatusPartial
	case errN > 0:
		status = ProxyStatusError
	default:
		status = ProxyStatusPartial
	}
	return SetProxyServiceStatus(d, serviceID, status)
}

// SetNodeCoresJSON stores the cores array reported by hello.
func SetNodeCoresJSON(d *sql.DB, nodeID int64, coresJSON string) error {
	if strings.TrimSpace(coresJSON) == "" {
		coresJSON = "[]"
	}
	_, err := d.Exec(`UPDATE nodes SET cores_json=? WHERE id=?`, coresJSON, nodeID)
	return err
}

// GetNodeCores parses cores_json for a node.
func GetNodeCores(d *sql.DB, nodeID int64) ([]NodeCore, error) {
	var raw string
	err := d.QueryRow(`SELECT cores_json FROM nodes WHERE id=?`, nodeID).Scan(&raw)
	if err != nil {
		return nil, err
	}
	return ParseNodeCoresJSON(raw)
}

// ParseNodeCoresJSON decodes a cores JSON array.
func ParseNodeCoresJSON(raw string) ([]NodeCore, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" {
		return nil, nil
	}
	var cores []NodeCore
	if err := json.Unmarshal([]byte(raw), &cores); err != nil {
		return nil, err
	}
	return cores, nil
}

// NodeHasCore reports whether cores list includes the given core name.
func NodeHasCore(cores []NodeCore, want string) bool {
	want = CanonicalCore(want)
	for _, c := range cores {
		if CanonicalCore(c.Name) == want {
			return true
		}
	}
	return false
}

// ListNodePortsUsedByProxy returns listen ports already taken by proxy instances on a node.
func ListNodePortsUsedByProxy(d *sql.DB, nodeID int64, excludeInstanceID int64) ([]int, error) {
	rows, err := d.Query(`SELECT listen_port FROM proxy_service_instances WHERE node_id=? AND id!=? AND listen_port>0`,
		nodeID, excludeInstanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ports []int
	for rows.Next() {
		var p int
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		ports = append(ports, p)
	}
	return ports, rows.Err()
}
