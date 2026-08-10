package db

import "database/sql"

// SubToken is a dedicated credential for public client subscription URLs
// (/sub/{token}/…). Plaintext is only returned on create/refresh.
type SubToken struct {
	ID          int64         `json:"id"`
	UserID      int64         `json:"user_id"`
	Token       string        `json:"-"` // SHA-256 at rest
	TokenPrefix string        `json:"token_prefix"`
	Disabled    bool          `json:"disabled"`
	CreatedAt   int64         `json:"created_at"`
	LastUsedAt  sql.NullInt64 `json:"last_used_at"`
}

func CreateSubToken(d *sql.DB, userID int64) (string, error) {
	token := RandToken(32)
	_, err := d.Exec(
		`INSERT INTO sub_tokens(user_id, token, token_prefix, created_at) VALUES (?,?,?,?)`,
		userID, HashToken(token), tokenPrefix(token), now())
	if err != nil {
		return "", err
	}
	return token, nil
}

func GetSubTokenByUser(d *sql.DB, userID int64) (*SubToken, error) {
	t := &SubToken{}
	var disabled int
	err := d.QueryRow(
		`SELECT id, user_id, token, token_prefix, disabled, created_at, last_used_at FROM sub_tokens WHERE user_id=?`,
		userID).Scan(&t.ID, &t.UserID, &t.Token, &t.TokenPrefix, &disabled, &t.CreatedAt, &t.LastUsedAt)
	if err != nil {
		return nil, err
	}
	t.Disabled = disabled == 1
	return t, nil
}

// GetUserBySubToken resolves a plaintext sub token to user + row.
func GetUserBySubToken(d *sql.DB, token string) (*User, *SubToken, error) {
	t := &SubToken{}
	var disabled int
	err := d.QueryRow(
		`SELECT t.id, t.user_id, t.token, t.token_prefix, t.disabled, t.created_at, t.last_used_at
		 FROM sub_tokens t WHERE t.token=?`, HashToken(token),
	).Scan(&t.ID, &t.UserID, &t.Token, &t.TokenPrefix, &disabled, &t.CreatedAt, &t.LastUsedAt)
	if err != nil {
		return nil, nil, err
	}
	t.Disabled = disabled == 1
	u, err := GetUserByID(d, t.UserID)
	if err != nil {
		return nil, nil, err
	}
	return u, t, nil
}

func DeleteSubToken(d *sql.DB, userID int64) error {
	_, err := d.Exec(`DELETE FROM sub_tokens WHERE user_id=?`, userID)
	return err
}

func RefreshSubToken(d *sql.DB, userID int64) (string, error) {
	token := RandToken(32)
	res, err := d.Exec(
		`UPDATE sub_tokens SET token=?, token_prefix=?, created_at=?, last_used_at=NULL, disabled=0 WHERE user_id=?`,
		HashToken(token), tokenPrefix(token), now(), userID)
	if err != nil {
		return "", err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		// No row yet — create.
		return CreateSubToken(d, userID)
	}
	return token, nil
}

// EnsureSubToken returns an existing row without plaintext, or creates one and
// returns the new plaintext. created=true when plaintext is valid.
func EnsureSubToken(d *sql.DB, userID int64) (t *SubToken, plaintext string, created bool, err error) {
	existing, err := GetSubTokenByUser(d, userID)
	if err == nil {
		return existing, "", false, nil
	}
	if err != sql.ErrNoRows {
		return nil, "", false, err
	}
	plain, err := CreateSubToken(d, userID)
	if err != nil {
		return nil, "", false, err
	}
	row, err := GetSubTokenByUser(d, userID)
	if err != nil {
		return nil, plain, true, nil
	}
	return row, plain, true, nil
}

func ToggleSubToken(d *sql.DB, userID int64) (disabled bool, err error) {
	var dis int
	err = d.QueryRow(`SELECT disabled FROM sub_tokens WHERE user_id=?`, userID).Scan(&dis)
	if err != nil {
		return false, err
	}
	newVal := 1 - dis
	_, err = d.Exec(`UPDATE sub_tokens SET disabled=? WHERE user_id=?`, newVal, userID)
	return newVal == 1, err
}

func TouchSubTokenUsage(d *sql.DB, tokenID int64) error {
	_, err := d.Exec(`UPDATE sub_tokens SET last_used_at=? WHERE id=?`, now(), tokenID)
	return err
}
