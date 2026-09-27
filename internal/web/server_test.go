package web

import (
	"bytes"
	"encoding/json"
	"github.com/OzoneH3/CLIProxyAPI_unraid/internal/cliproxy"
	"github.com/OzoneH3/CLIProxyAPI_unraid/internal/mock"
	"github.com/OzoneH3/CLIProxyAPI_unraid/internal/security"
	"golang.org/x/crypto/bcrypt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAuthenticatedBoundary(t *testing.T) {
	m := mock.New()
	m.Connected = true
	up := httptest.NewServer(m.Handler())
	defer up.Close()
	h, _ := bcrypt.GenerateFromPassword([]byte("correct-password"), bcrypt.MinCost)
	s := New(cliproxy.New(up.URL, m.Secret, m.Key, ""), security.New(h, false))
	s.Alive.Store(true)
	handler := s.Handler()
	do := func(method, path, body string, cookie *http.Cookie, csrf string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://localhost"+path, strings.NewReader(body))
		r.Header.Set("Origin", "http://localhost")
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-CSRF-Token", csrf)
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if w := do("GET", "/api/key", "", nil, ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	w := do("POST", "/api/login", `{"password":"correct-password"}`, nil, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	cookie := w.Result().Cookies()[0]
	var login map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &login)
	csrf := login["csrf"]
	for _, path := range []string{"/api/dashboard", "/api/accounts", "/api/logs", "/api/providers"} {
		w = do("GET", path, "", cookie, "")
		if w.Code != 200 {
			t.Fatal(path, w.Code)
		}
		if bytes.Contains(w.Body.Bytes(), []byte(m.Secret)) || bytes.Contains(w.Body.Bytes(), []byte("MUST-NOT-LEAK")) {
			t.Fatal("secret response")
		}
		if w.Header().Get("Content-Security-Policy") == "" {
			t.Fatal("missing CSP")
		}
	}
	if w = do("DELETE", "/api/accounts", `{"id":"bad","confirm":true}`, cookie, ""); w.Code != 403 {
		t.Fatal("missing CSRF accepted")
	}
	a, _ := s.Client.Accounts(httptest.NewRequest("GET", "/", nil).Context())
	payload, _ := json.Marshal(map[string]any{"id": a[0].ID, "confirm": false})
	if w = do("DELETE", "/api/accounts", string(payload), cookie, csrf); w.Code != 400 {
		t.Fatal("missing confirmation accepted")
	}
	if w = do("GET", "/api/auth-files/download?name=mock.json", "", cookie, ""); w.Code != 404 {
		t.Fatal("raw file route exposed")
	}
	if w = do("GET", "/healthz", "", nil, ""); w.Code != 200 {
		t.Fatal("health fails without OAuth")
	}
	if w = do("POST", "/api/logout", `{}`, cookie, csrf); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w = do("GET", "/api/key", "", cookie, ""); w.Code != 401 {
		t.Fatal("logout did not revoke")
	}
}
