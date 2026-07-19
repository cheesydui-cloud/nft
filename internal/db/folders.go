package db

import (
	"database/sql"
	"fmt"
	"strings"
)

// Folder is a single-level admin folder for users or node-repo entries.
type Folder struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	SortOrder int    `json:"sort_order"`
	CreatedAt int64  `json:"created_at"`
	// Count is filled by list helpers; not a DB column.
	Count int `json:"count"`
}

func scanFolder(r rowScanner) (*Folder, error) {
	f := &Folder{}
	if err := r.Scan(&f.ID, &f.Name, &f.SortOrder, &f.CreatedAt); err != nil {
		return nil, err
	}
	return f, nil
}

// MigrateLegacyGroupNames creates folders from legacy free-text group_name
// labels and points group_id at them. Safe to re-run: only rows with
// group_id=0 and a non-empty group_name are considered.
func MigrateLegacyGroupNames(d *sql.DB) error {
	if err := migrateLegacyUserGroups(d); err != nil {
		return err
	}
	return migrateLegacyNodeRepoGroups(d)
}

func migrateLegacyUserGroups(d *sql.DB) error {
	rows, err := d.Query(`SELECT DISTINCT TRIM(group_name) FROM users WHERE group_id=0 AND TRIM(group_name) != ''`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return err
		}
		if n != "" {
			names = append(names, n)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, name := range names {
		f, err := EnsureUserFolder(d, name)
		if err != nil {
			return err
		}
		if _, err := d.Exec(`UPDATE users SET group_id=? WHERE group_id=0 AND TRIM(group_name)=?`, f.ID, name); err != nil {
			return err
		}
	}
	return nil
}

func migrateLegacyNodeRepoGroups(d *sql.DB) error {
	rows, err := d.Query(`SELECT DISTINCT TRIM(group_name) FROM node_repo WHERE group_id=0 AND TRIM(group_name) != ''`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return err
		}
		if n != "" {
			names = append(names, n)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, name := range names {
		f, err := EnsureNodeRepoFolder(d, name)
		if err != nil {
			return err
		}
		if _, err := d.Exec(`UPDATE node_repo SET group_id=? WHERE group_id=0 AND TRIM(group_name)=?`, f.ID, name); err != nil {
			return err
		}
	}
	return nil
}

// --- user folders ---

func ListUserFolders(d *sql.DB) ([]*Folder, error) {
	rows, err := d.Query(`SELECT id, name, sort_order, created_at FROM user_folders ORDER BY sort_order, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Folder
	for rows.Next() {
		f, err := scanFolder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Fill counts
	for _, f := range out {
		_ = d.QueryRow(`SELECT COUNT(*) FROM users WHERE group_id=?`, f.ID).Scan(&f.Count)
	}
	return out, nil
}

func CreateUserFolder(d *sql.DB, name string) (*Folder, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("文件夹名称不能为空")
	}
	var maxOrd int
	_ = d.QueryRow(`SELECT COALESCE(MAX(sort_order),0) FROM user_folders`).Scan(&maxOrd)
	res, err := d.Exec(`INSERT INTO user_folders (name, sort_order, created_at) VALUES (?,?,?)`, name, maxOrd+1, now())
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return nil, fmt.Errorf("文件夹「%s」已存在", name)
		}
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &Folder{ID: id, Name: name, SortOrder: maxOrd + 1, CreatedAt: now()}, nil
}

func EnsureUserFolder(d *sql.DB, name string) (*Folder, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("文件夹名称不能为空")
	}
	f, err := GetUserFolderByName(d, name)
	if err == nil {
		return f, nil
	}
	if err != sql.ErrNoRows {
		return nil, err
	}
	return CreateUserFolder(d, name)
}

func GetUserFolder(d *sql.DB, id int64) (*Folder, error) {
	row := d.QueryRow(`SELECT id, name, sort_order, created_at FROM user_folders WHERE id=?`, id)
	return scanFolder(row)
}

func GetUserFolderByName(d *sql.DB, name string) (*Folder, error) {
	row := d.QueryRow(`SELECT id, name, sort_order, created_at FROM user_folders WHERE name=?`, strings.TrimSpace(name))
	return scanFolder(row)
}

func RenameUserFolder(d *sql.DB, id int64, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("文件夹名称不能为空")
	}
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`UPDATE user_folders SET name=? WHERE id=?`, name, id); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return fmt.Errorf("文件夹「%s」已存在", name)
		}
		return err
	}
	if _, err := tx.Exec(`UPDATE users SET group_name=? WHERE group_id=?`, name, id); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteUserFolder removes the folder and moves its members to ungrouped (group_id=0).
func DeleteUserFolder(d *sql.DB, id int64) error {
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`UPDATE users SET group_id=0, group_name='' WHERE group_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM user_folders WHERE id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func SetUserFolder(d *sql.DB, userID, folderID int64) error {
	name := ""
	if folderID > 0 {
		f, err := GetUserFolder(d, folderID)
		if err != nil {
			return fmt.Errorf("文件夹不存在")
		}
		name = f.Name
	}
	_, err := d.Exec(`UPDATE users SET group_id=?, group_name=? WHERE id=?`, folderID, name, userID)
	return err
}

func SetUsersFolderBatch(d *sql.DB, ids []int64, folderID int64) error {
	if len(ids) == 0 {
		return nil
	}
	name := ""
	if folderID > 0 {
		f, err := GetUserFolder(d, folderID)
		if err != nil {
			return fmt.Errorf("文件夹不存在")
		}
		name = f.Name
	}
	ph := make([]string, len(ids))
	args := make([]any, 0, len(ids)+2)
	args = append(args, folderID, name)
	for i, id := range ids {
		ph[i] = "?"
		args = append(args, id)
	}
	_, err := d.Exec(`UPDATE users SET group_id=?, group_name=? WHERE id IN (`+strings.Join(ph, ",")+`)`, args...)
	return err
}

// --- node_repo folders ---

func ListNodeRepoFolders(d *sql.DB) ([]*Folder, error) {
	rows, err := d.Query(`SELECT id, name, sort_order, created_at FROM node_repo_folders ORDER BY sort_order, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Folder
	for rows.Next() {
		f, err := scanFolder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, f := range out {
		_ = d.QueryRow(`SELECT COUNT(*) FROM node_repo WHERE group_id=?`, f.ID).Scan(&f.Count)
	}
	return out, nil
}

func CreateNodeRepoFolder(d *sql.DB, name string) (*Folder, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("文件夹名称不能为空")
	}
	var maxOrd int
	_ = d.QueryRow(`SELECT COALESCE(MAX(sort_order),0) FROM node_repo_folders`).Scan(&maxOrd)
	res, err := d.Exec(`INSERT INTO node_repo_folders (name, sort_order, created_at) VALUES (?,?,?)`, name, maxOrd+1, now())
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return nil, fmt.Errorf("文件夹「%s」已存在", name)
		}
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &Folder{ID: id, Name: name, SortOrder: maxOrd + 1, CreatedAt: now()}, nil
}

func EnsureNodeRepoFolder(d *sql.DB, name string) (*Folder, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("文件夹名称不能为空")
	}
	f, err := GetNodeRepoFolderByName(d, name)
	if err == nil {
		return f, nil
	}
	if err != sql.ErrNoRows {
		return nil, err
	}
	return CreateNodeRepoFolder(d, name)
}

func GetNodeRepoFolder(d *sql.DB, id int64) (*Folder, error) {
	row := d.QueryRow(`SELECT id, name, sort_order, created_at FROM node_repo_folders WHERE id=?`, id)
	return scanFolder(row)
}

func GetNodeRepoFolderByName(d *sql.DB, name string) (*Folder, error) {
	row := d.QueryRow(`SELECT id, name, sort_order, created_at FROM node_repo_folders WHERE name=?`, strings.TrimSpace(name))
	return scanFolder(row)
}

func RenameNodeRepoFolder(d *sql.DB, id int64, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("文件夹名称不能为空")
	}
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`UPDATE node_repo_folders SET name=? WHERE id=?`, name, id); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return fmt.Errorf("文件夹「%s」已存在", name)
		}
		return err
	}
	// Keep denormalized label in sync for list display.
	if _, err := tx.Exec(`UPDATE node_repo SET group_name=? WHERE group_id=?`, name, id); err != nil {
		return err
	}
	return tx.Commit()
}

func DeleteNodeRepoFolder(d *sql.DB, id int64) error {
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`UPDATE node_repo SET group_id=0, group_name='' WHERE group_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM node_repo_folders WHERE id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func SetNodeRepoFolder(d *sql.DB, entryID, folderID int64) error {
	name := ""
	if folderID > 0 {
		f, err := GetNodeRepoFolder(d, folderID)
		if err != nil {
			return fmt.Errorf("文件夹不存在")
		}
		name = f.Name
	}
	_, err := d.Exec(`UPDATE node_repo SET group_id=?, group_name=? WHERE id=?`, folderID, name, entryID)
	return err
}

func SetNodeRepoFoldersBatch(d *sql.DB, ids []int64, folderID int64) error {
	if len(ids) == 0 {
		return nil
	}
	name := ""
	if folderID > 0 {
		f, err := GetNodeRepoFolder(d, folderID)
		if err != nil {
			return fmt.Errorf("文件夹不存在")
		}
		name = f.Name
	}
	ph := make([]string, len(ids))
	args := make([]any, 0, len(ids)+2)
	args = append(args, folderID, name)
	for i, id := range ids {
		ph[i] = "?"
		args = append(args, id)
	}
	_, err := d.Exec(`UPDATE node_repo SET group_id=?, group_name=? WHERE id IN (`+strings.Join(ph, ",")+`)`, args...)
	return err
}
