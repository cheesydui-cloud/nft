package db

import (
	"database/sql"
	"strings"
)

// NodeRepoEntry is one node in the admin-maintained node repository.
type NodeRepoEntry struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Protocol  string `json:"protocol"`
	Host      string `json:"host"`
	Port      int    `json:"port"`
	URI       string `json:"uri"`
	Remark    string `json:"remark"`
	ExpiresAt int64  `json:"expires_at"`
	CreatedAt int64  `json:"created_at"`
	// GroupID is the folder this entry sits in (0 = ungrouped). GroupName is a
	// denormalized label kept in sync for display and legacy clients.
	GroupID   int64  `json:"group_id"`
	GroupName string `json:"group_name"`
}

const nodeRepoCols = `id, name, protocol, host, port, uri, remark, expires_at, created_at, group_name, group_id`

func scanNodeRepo(rows *sql.Rows) (NodeRepoEntry, error) {
	var n NodeRepoEntry
	err := rows.Scan(&n.ID, &n.Name, &n.Protocol, &n.Host, &n.Port, &n.URI, &n.Remark, &n.ExpiresAt, &n.CreatedAt, &n.GroupName, &n.GroupID)
	return n, err
}

// ListNodeRepo returns all nodes in the repository.
func ListNodeRepo(d *sql.DB) ([]NodeRepoEntry, error) {
	rows, err := d.Query(`SELECT ` + nodeRepoCols + ` FROM node_repo ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NodeRepoEntry
	for rows.Next() {
		n, err := scanNodeRepo(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, nil
}

// CreateNodeRepoEntry inserts a new node into the repository.
// groupName, when non-empty, creates/ensures a folder and assigns group_id.
func CreateNodeRepoEntry(d *sql.DB, name, protocol, host string, port int, uri, remark string, expiresAt int64, groupName string) (NodeRepoEntry, error) {
	var groupID int64
	groupName = strings.TrimSpace(groupName)
	if groupName != "" {
		f, err := EnsureNodeRepoFolder(d, groupName)
		if err != nil {
			return NodeRepoEntry{}, err
		}
		groupID = f.ID
	}
	n := NodeRepoEntry{Name: name, Protocol: protocol, Host: host, Port: port, URI: uri, Remark: remark, ExpiresAt: expiresAt, CreatedAt: now(), GroupName: groupName, GroupID: groupID}
	res, err := d.Exec(`INSERT INTO node_repo (name, protocol, host, port, uri, remark, expires_at, created_at, group_name, group_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		n.Name, n.Protocol, n.Host, n.Port, n.URI, n.Remark, n.ExpiresAt, n.CreatedAt, n.GroupName, n.GroupID)
	if err != nil {
		return n, err
	}
	n.ID, _ = res.LastInsertId()
	return n, nil
}

// GetNodeRepoEntry returns one repository node by id.
func GetNodeRepoEntry(d *sql.DB, id int64) (NodeRepoEntry, error) {
	row := d.QueryRow(`SELECT `+nodeRepoCols+` FROM node_repo WHERE id=?`, id)
	var n NodeRepoEntry
	err := row.Scan(&n.ID, &n.Name, &n.Protocol, &n.Host, &n.Port, &n.URI, &n.Remark, &n.ExpiresAt, &n.CreatedAt, &n.GroupName, &n.GroupID)
	if err != nil {
		return n, err
	}
	return n, nil
}

// UpdateNodeRepoEntry updates an existing node in the repository.
func UpdateNodeRepoEntry(d *sql.DB, id int64, name, protocol, host string, port int, uri, remark string, expiresAt int64, groupName string) error {
	var groupID int64
	groupName = strings.TrimSpace(groupName)
	if groupName != "" {
		f, err := EnsureNodeRepoFolder(d, groupName)
		if err != nil {
			return err
		}
		groupID = f.ID
	}
	_, err := d.Exec(`UPDATE node_repo SET name=?, protocol=?, host=?, port=?, uri=?, remark=?, expires_at=?, group_name=?, group_id=? WHERE id=?`,
		name, protocol, host, port, uri, remark, expiresAt, groupName, groupID, id)
	return err
}

// SetNodeRepoGroup assigns a free-form group label to a repo entry (empty = ungrouped).
func SetNodeRepoGroup(d *sql.DB, id int64, groupName string) error {
	groupName = strings.TrimSpace(groupName)
	if groupName == "" {
		return SetNodeRepoFolder(d, id, 0)
	}
	f, err := EnsureNodeRepoFolder(d, groupName)
	if err != nil {
		return err
	}
	return SetNodeRepoFolder(d, id, f.ID)
}

// SetNodeRepoGroupsBatch assigns the same group label to many repo entries.
func SetNodeRepoGroupsBatch(d *sql.DB, ids []int64, groupName string) error {
	groupName = strings.TrimSpace(groupName)
	if groupName == "" {
		return SetNodeRepoFoldersBatch(d, ids, 0)
	}
	f, err := EnsureNodeRepoFolder(d, groupName)
	if err != nil {
		return err
	}
	return SetNodeRepoFoldersBatch(d, ids, f.ID)
}

// DeleteNodeRepoEntry removes a node from the repository by ID.
func DeleteNodeRepoEntry(d *sql.DB, id int64) error {
	_, err := d.Exec(`DELETE FROM node_repo WHERE id = ?`, id)
	return err
}

// ListNodeRepoByIDs returns specific repo entries by their IDs.
func ListNodeRepoByIDs(d *sql.DB, ids []int64) ([]NodeRepoEntry, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	args := make([]any, len(ids))
	placeholders := ""
	for i, id := range ids {
		if i > 0 {
			placeholders += ","
		}
		placeholders += "?"
		args[i] = id
	}
	rows, err := d.Query(`SELECT `+nodeRepoCols+` FROM node_repo WHERE id IN (`+placeholders+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NodeRepoEntry
	for rows.Next() {
		n, err := scanNodeRepo(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, nil
}
