package server

import (
	"io"
	"net/http"
	"net/http/httptest"
)

// newTestRequest mirrors httptest.NewRequest but attaches an Origin header that
// matches the default test Host ("example.com"), so state-changing requests
// pass the same-origin CSRF check enforced by csrfProtect. It's harmless on
// safe-method requests.
func newTestRequest(method, target string, body io.Reader) *http.Request {
	req := httptest.NewRequest(method, target, body)
	req.Header.Set("Origin", "http://"+req.Host)
	return req
}
