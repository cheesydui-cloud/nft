package db

import "database/sql"

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
	// GroupName is a free-form admin label for filtering the pool; empty = ungrouped.
	GroupName string `json:"group_name"`
}

const nodeRepoCols = `id, name, protocol, host, port, uri, remark, expires_at, created_at, group_name`

func scanNodeRepo(rows *sql.Rows) (NodeRepoEntry, error) {
	var n NodeRepoEntry
	err := rows.Scan(&n.ID, &n.Name, &n.Protocol, &n.Host, &n.Port, &n.URI, &n.Remark, &n.ExpiresAt, &n.CreatedAt, &n.GroupName)
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
func CreateNodeRepoEntry(d *sql.DB, name, protocol, host string, port int, uri, remark string, expiresAt int64, groupName string) (NodeRepoEntry, error) {
	n := NodeRepoEntry{Name: name, Protocol: protocol, Host: host, Port: port, URI: uri, Remark: remark, ExpiresAt: expiresAt, CreatedAt: now(), GroupName: groupName}
	res, err := d.Exec(`INSERT INTO node_repo (name, protocol, host, port, uri, remark, expires_at, created_at, group_name) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		n.Name, n.Protocol, n.Host, n.Port, n.URI, n.Remark, n.ExpiresAt, n.CreatedAt, n.GroupName)
	if err != nil {
		return n, err
	}
	n.ID, _ = res.LastInsertId()
	return n, nil
}

// UpdateNodeRepoEntry updates an existing node in the repository.
func UpdateNodeRepoEntry(d *sql.DB, id int64, name, protocol, host string, port int, uri, remark string, expiresAt int64, groupName string) error {
	_, err := d.Exec(`UPDATE node_repo SET name=?, protocol=?, host=?, port=?, uri=?, remark=?, expires_at=?, group_name=? WHERE id=?`,
		name, protocol, host, port, uri, remark, expiresAt, groupName, id)
	return err
}

// SetNodeRepoGroup assigns a free-form group label to a repo entry (empty = ungrouped).
func SetNodeRepoGroup(d *sql.DB, id int64, groupName string) error {
	_, err := d.Exec(`UPDATE node_repo SET group_name=? WHERE id=?`, groupName, id)
	return err
}

// SetNodeRepoGroupsBatch assigns the same group label to many repo entries.
func SetNodeRepoGroupsBatch(d *sql.DB, ids []int64, groupName string) error {
	if len(ids) == 0 {
		return nil
	}
	args := make([]any, 0, len(ids)+1)
	args = append(args, groupName)
	ph := ""
	for i, id := range ids {
		if i > 0 {
			ph += ","
		}
		ph += "?"
		args = append(args, id)
	}
	_, err := d.Exec(`UPDATE node_repo SET group_name=? WHERE id IN (`+ph+`)`, args...)
	return err
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
