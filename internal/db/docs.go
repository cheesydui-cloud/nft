package db

import (
	"database/sql"
	"fmt"
	"strings"
)

// Doc is an admin-maintained usage article (Markdown body).
type Doc struct {
	ID        int64  `json:"id"`
	Title     string `json:"title"`
	Content   string `json:"content"`
	SortOrder int    `json:"sort_order"`
	Published bool   `json:"published"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

func scanDoc(rows *sql.Rows, d *Doc) error {
	var published int
	if err := rows.Scan(&d.ID, &d.Title, &d.Content, &d.SortOrder, &published, &d.CreatedAt, &d.UpdatedAt); err != nil {
		return err
	}
	d.Published = published != 0
	return nil
}

func scanDocRow(row *sql.Row, d *Doc) error {
	var published int
	if err := row.Scan(&d.ID, &d.Title, &d.Content, &d.SortOrder, &published, &d.CreatedAt, &d.UpdatedAt); err != nil {
		return err
	}
	d.Published = published != 0
	return nil
}

const docCols = `id, title, content, sort_order, published, created_at, updated_at`

// ListDocs returns every document (admin view), ordered for display.
func ListDocs(d *sql.DB) ([]Doc, error) {
	rows, err := d.Query(`SELECT ` + docCols + ` FROM docs ORDER BY sort_order ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Doc
	for rows.Next() {
		var doc Doc
		if err := scanDoc(rows, &doc); err != nil {
			return nil, err
		}
		out = append(out, doc)
	}
	return out, rows.Err()
}

// ListPublishedDocs returns docs visible to end users.
func ListPublishedDocs(d *sql.DB) ([]Doc, error) {
	rows, err := d.Query(`SELECT ` + docCols + ` FROM docs WHERE published = 1 ORDER BY sort_order ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Doc
	for rows.Next() {
		var doc Doc
		if err := scanDoc(rows, &doc); err != nil {
			return nil, err
		}
		out = append(out, doc)
	}
	return out, rows.Err()
}

// GetDoc returns a document by id.
func GetDoc(d *sql.DB, id int64) (Doc, error) {
	var doc Doc
	err := scanDocRow(d.QueryRow(`SELECT `+docCols+` FROM docs WHERE id = ?`, id), &doc)
	return doc, err
}

// GetPublishedDoc returns a published document by id.
func GetPublishedDoc(d *sql.DB, id int64) (Doc, error) {
	var doc Doc
	err := scanDocRow(d.QueryRow(`SELECT `+docCols+` FROM docs WHERE id = ? AND published = 1`, id), &doc)
	return doc, err
}

// nextDocSortOrder returns max(sort_order)+1 for appending new docs.
func nextDocSortOrder(d *sql.DB) (int, error) {
	var max sql.NullInt64
	if err := d.QueryRow(`SELECT MAX(sort_order) FROM docs`).Scan(&max); err != nil {
		return 0, err
	}
	if !max.Valid {
		return 0, nil
	}
	return int(max.Int64) + 1, nil
}

// CreateDoc inserts a new document. Empty title is rejected by the API layer.
func CreateDoc(d *sql.DB, title, content string, published bool) (Doc, error) {
	order, err := nextDocSortOrder(d)
	if err != nil {
		return Doc{}, err
	}
	n := now()
	pub := 0
	if published {
		pub = 1
	}
	doc := Doc{
		Title:     strings.TrimSpace(title),
		Content:   content,
		SortOrder: order,
		Published: published,
		CreatedAt: n,
		UpdatedAt: n,
	}
	res, err := d.Exec(
		`INSERT INTO docs (title, content, sort_order, published, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		doc.Title, doc.Content, doc.SortOrder, pub, doc.CreatedAt, doc.UpdatedAt,
	)
	if err != nil {
		return doc, err
	}
	doc.ID, _ = res.LastInsertId()
	return doc, nil
}

// UpdateDoc replaces title/content/published for an existing document.
func UpdateDoc(d *sql.DB, id int64, title, content string, published bool) (Doc, error) {
	pub := 0
	if published {
		pub = 1
	}
	n := now()
	res, err := d.Exec(
		`UPDATE docs SET title = ?, content = ?, published = ?, updated_at = ? WHERE id = ?`,
		strings.TrimSpace(title), content, pub, n, id,
	)
	if err != nil {
		return Doc{}, err
	}
	aff, _ := res.RowsAffected()
	if aff == 0 {
		return Doc{}, sql.ErrNoRows
	}
	return GetDoc(d, id)
}

// SetDocPublished toggles the published flag.
func SetDocPublished(d *sql.DB, id int64, published bool) (Doc, error) {
	pub := 0
	if published {
		pub = 1
	}
	res, err := d.Exec(`UPDATE docs SET published = ?, updated_at = ? WHERE id = ?`, pub, now(), id)
	if err != nil {
		return Doc{}, err
	}
	aff, _ := res.RowsAffected()
	if aff == 0 {
		return Doc{}, sql.ErrNoRows
	}
	return GetDoc(d, id)
}

// DeleteDoc removes a document by id.
func DeleteDoc(d *sql.DB, id int64) error {
	res, err := d.Exec(`DELETE FROM docs WHERE id = ?`, id)
	if err != nil {
		return err
	}
	aff, _ := res.RowsAffected()
	if aff == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// SwapDocOrder swaps sort_order between two docs (for up/down moves).
func SwapDocOrder(d *sql.DB, aID, bID int64) error {
	if aID == 0 || bID == 0 || aID == bID {
		return fmt.Errorf("invalid doc ids")
	}
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var aOrder, bOrder int
	if err := tx.QueryRow(`SELECT sort_order FROM docs WHERE id = ?`, aID).Scan(&aOrder); err != nil {
		return err
	}
	if err := tx.QueryRow(`SELECT sort_order FROM docs WHERE id = ?`, bID).Scan(&bOrder); err != nil {
		return err
	}
	n := now()
	if _, err := tx.Exec(`UPDATE docs SET sort_order = ?, updated_at = ? WHERE id = ?`, bOrder, n, aID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE docs SET sort_order = ?, updated_at = ? WHERE id = ?`, aOrder, n, bID); err != nil {
		return err
	}
	return tx.Commit()
}

// ReorderDocs assigns sort_order 0..n-1 from the given id sequence.
func ReorderDocs(d *sql.DB, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	n := now()
	for i, id := range ids {
		if _, err := tx.Exec(`UPDATE docs SET sort_order = ?, updated_at = ? WHERE id = ?`, i, n, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}
