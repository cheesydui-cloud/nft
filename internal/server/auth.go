package server

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"nft/internal/db"
)

const (
	sessionCookie = "nft_session"
	flashCookie   = "nft_flash"
	sessionTTL    = 12 * time.Hour
)

type ctxKey int

const userKey ctxKey = iota

func userFromCtx(ctx context.Context) *db.User {
	v, _ := ctx.Value(userKey).(*db.User)
	return v
}

func (s *Server) requireRole(role string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u := userFromCtx(r.Context())
			if u == nil || u.Role != role {
				http.Error(w, "权限不足", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil || c.Value == "" {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		u, err := db.GetSessionUser(s.DB, c.Value)
		if err != nil || u == nil {
			http.SetCookie(w, newSessionCookie(r, "", -1))
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		if u.Disabled {
			http.Error(w, "账号已被禁用", http.StatusForbidden)
			return
		}
		ctx := context.WithValue(r.Context(), userKey, u)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// isSecureRequest reports whether the request reached us over HTTPS, either
// directly (r.TLS set) or via a trusted reverse proxy terminating TLS
// (X-Forwarded-Proto: https). Used to decide whether to set the Secure flag
// on cookies so we don't break plain-HTTP local/dev setups.
func isSecureRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip != nil && isTrustedProxy(ip) {
		if proto := r.Header.Get("X-Forwarded-Proto"); strings.EqualFold(proto, "https") {
			return true
		}
	}
	return false
}

// sessionCookie value helpers keep the Secure/HttpOnly/SameSite attributes
// consistent across login, logout and expiry paths.
func newSessionCookie(r *http.Request, value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     sessionCookie,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   isSecureRequest(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   maxAge,
	}
}

// sameOrigin enforces that a state-changing request originates from the same
// host it targets, mitigating CSRF for cookie-authenticated requests. It
// compares the Origin (or Referer as fallback) host against the request Host.
func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		origin = r.Header.Get("Referer")
	}
	if origin == "" {
		// A browser always attaches Origin (or at least Referer) to a
		// cross-origin state-changing fetch; its absence is anomalous, so
		// fail closed.
		return false
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	if strings.EqualFold(u.Host, r.Host) {
		return true
	}
	// Behind a proxy that rewrites Host, accept the forwarded host too.
	if xfh := r.Header.Get("X-Forwarded-Host"); xfh != "" && strings.EqualFold(u.Host, xfh) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		if ip := net.ParseIP(host); ip != nil && isTrustedProxy(ip) {
			return true
		}
	}
	return false
}

// csrfProtect rejects cross-site state-changing requests. Safe methods and
// Bearer-token authenticated requests (which the browser can't forge because
// it won't auto-attach the token) are exempt.
func (s *Server) csrfProtect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			next.ServeHTTP(w, r)
			return
		}
		if !sameOrigin(r) {
			jsonErr(w, http.StatusForbidden, "跨站请求被拒绝")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func setFlash(w http.ResponseWriter, msg string) {
	http.SetCookie(w, &http.Cookie{Name: flashCookie, Value: url.QueryEscape(msg), Path: "/", MaxAge: 30})
}

func flashFromCookie(w http.ResponseWriter, r *http.Request) string {
	c, err := r.Cookie(flashCookie)
	if err != nil {
		return ""
	}
	http.SetCookie(w, &http.Cookie{Name: flashCookie, Value: "", Path: "/", MaxAge: -1})
	msg, err := url.QueryUnescape(c.Value)
	if err != nil {
		return ""
	}
	return msg
}

func HashPassword(p string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(p), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(h), nil
}

// dummyPwHash is a valid bcrypt hash of a random password, computed once. When
// a login targets a non-existent user we still run a bcrypt comparison against
// it so the response time doesn't reveal whether the username exists.
var dummyPwHash = func() []byte {
	h, err := bcrypt.GenerateFromPassword([]byte("nft-nonexistent-user-placeholder"), bcrypt.DefaultCost)
	if err != nil {
		// bcrypt only errors on absurd cost; DefaultCost is fine. Fall back to
		// an empty slice — CompareHashAndPassword will just return an error,
		// which still burns comparable time on the malformed-hash path.
		return nil
	}
	return h
}()
