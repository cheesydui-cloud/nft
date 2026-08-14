package db

import (
	"database/sql"
	"fmt"
)

// UserProxyCred is one user's inbound secret for a published proxy service.
type UserProxyCred struct {
	UserID    int64
	ServiceID int64
	UUID      string
	Username  string
	Password  string
	CreatedAt int64
}

// InboundClient is the live inbound identity for one active user.
type InboundClient struct {
	UserID   int64
	UUID     string
	Username string
	Password string
}

// GetUserProxyCred loads a stored cred row.
func GetUserProxyCred(d *sql.DB, userID, serviceID int64) (*UserProxyCred, error) {
	c := &UserProxyCred{}
	err := d.QueryRow(`SELECT user_id, service_id, uuid, username, password, created_at
		FROM user_proxy_service_creds WHERE user_id=? AND service_id=?`,
		userID, serviceID).Scan(&c.UserID, &c.ServiceID, &c.UUID, &c.Username, &c.Password, &c.CreatedAt)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// CountUserProxyCreds returns how many per-user secrets exist for a service.
func CountUserProxyCreds(d *sql.DB, serviceID int64) (int, error) {
	var n int
	err := d.QueryRow(`SELECT COUNT(*) FROM user_proxy_service_creds WHERE service_id=?`, serviceID).Scan(&n)
	return n, err
}

// InsertUserProxyCred stores a new per-user secret. Existing rows are left intact.
func InsertUserProxyCred(d *sql.DB, userID, serviceID int64, uuid, username, password string) error {
	if userID <= 0 || serviceID <= 0 {
		return fmt.Errorf("invalid user/service")
	}
	_, err := d.Exec(`INSERT INTO user_proxy_service_creds(user_id, service_id, uuid, username, password, created_at)
		VALUES (?,?,?,?,?,?)
		ON CONFLICT(user_id, service_id) DO NOTHING`,
		userID, serviceID, uuid, username, password, now())
	return err
}

// EnsureUserProxyCred returns the stored secret, inserting minted values on first use.
func EnsureUserProxyCred(d *sql.DB, userID, serviceID int64, uuid, username, password string) (*UserProxyCred, error) {
	if c, err := GetUserProxyCred(d, userID, serviceID); err == nil && c != nil {
		return c, nil
	} else if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	if err := InsertUserProxyCred(d, userID, serviceID, uuid, username, password); err != nil {
		if c, gerr := GetUserProxyCred(d, userID, serviceID); gerr == nil && c != nil {
			return c, nil
		}
		return nil, err
	}
	return GetUserProxyCred(d, userID, serviceID)
}

// ListActiveProxyClients returns inbound clients for users who still have an
// enabled rule on serviceID and whose account is enabled and not expired.
func ListActiveProxyClients(d *sql.DB, serviceID int64) ([]InboundClient, error) {
	if serviceID <= 0 {
		return nil, nil
	}
	rows, err := d.Query(`
		SELECT DISTINCT u.id, COALESCE(c.uuid,''), COALESCE(c.username,''), COALESCE(c.password,'')
		FROM rules r
		JOIN users u ON u.id = r.owner_id
		LEFT JOIN user_proxy_service_creds c ON c.user_id = u.id AND c.service_id = r.proxy_service_id
		WHERE r.proxy_service_id = ?
		  AND COALESCE(r.disabled, 0) = 0
		  AND u.disabled = 0
		  AND (u.expires_at IS NULL OR u.expires_at = 0 OR u.expires_at >= strftime('%s','now'))
		ORDER BY u.id`, serviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []InboundClient
	for rows.Next() {
		var cl InboundClient
		if err := rows.Scan(&cl.UserID, &cl.UUID, &cl.Username, &cl.Password); err != nil {
			return nil, err
		}
		out = append(out, cl)
	}
	return out, rows.Err()
}

// UserHasActiveProxyRule reports whether the user has an enabled rule on serviceID.
func UserHasActiveProxyRule(d *sql.DB, userID, serviceID int64) bool {
	if userID <= 0 || serviceID <= 0 {
		return false
	}
	var n int
	err := d.QueryRow(`
		SELECT 1 FROM rules r
		JOIN users u ON u.id = r.owner_id
		WHERE r.owner_id=? AND r.proxy_service_id=?
		  AND COALESCE(r.disabled,0)=0
		  AND u.disabled=0
		  AND (u.expires_at IS NULL OR u.expires_at=0 OR u.expires_at >= strftime('%s','now'))
		LIMIT 1`, userID, serviceID).Scan(&n)
	return err == nil
}

// ListProxyServiceIDsForUserRules returns distinct proxy_service_id values on
// the user's rules (including disabled). Used to republish after delete/disable.
func ListProxyServiceIDsForUserRules(d *sql.DB, userID int64) ([]int64, error) {
	if userID <= 0 {
		return nil, nil
	}
	return queryInt64s(d, `SELECT DISTINCT proxy_service_id FROM rules
		WHERE owner_id=? AND COALESCE(proxy_service_id,0)>0`, userID)
}

// ListExpiredUserProxyServiceIDs returns services still referenced by enabled
// rules whose owner is past expires_at (account not already disabled).
func ListExpiredUserProxyServiceIDs(d *sql.DB) ([]int64, error) {
	return queryInt64s(d, `
		SELECT DISTINCT r.proxy_service_id
		FROM rules r
		JOIN users u ON u.id = r.owner_id
		WHERE COALESCE(r.proxy_service_id,0) > 0
		  AND COALESCE(r.disabled,0) = 0
		  AND u.disabled = 0
		  AND u.expires_at IS NOT NULL
		  AND u.expires_at > 0
		  AND u.expires_at < strftime('%s','now')`)
}
