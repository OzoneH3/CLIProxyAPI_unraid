// Package mock provides synthetic fixtures only; no provider credentials are used.
package mock

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"sync"

	"gopkg.in/yaml.v3"
)

type Server struct {
	mu                          sync.Mutex
	Secret, Key, Config, Status string
	Connected, Disabled         bool
}

func New() *Server {
	return &Server{Secret: "mock-management-only", Key: "mock-client-only", Status: "wait"}
}
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-CPA-VERSION", "mock")
		send := func(v any) { _ = json.NewEncoder(w).Encode(v) }
		if r.URL.Path == "/v1/models" {
			keys := []string{s.Key}
			if s.Config != "" {
				b, _ := os.ReadFile(s.Config)
				var c struct {
					Keys   []string `yaml:"api-keys"`
					Access struct {
						Keys []string `yaml:"api-keys"`
					} `yaml:"access"`
				}
				_ = yaml.Unmarshal(b, &c)
				keys = c.Keys
				if c.Access.Keys != nil {
					keys = c.Access.Keys
				}
			}
			ok := false
			for _, k := range keys {
				if r.Header.Get("Authorization") == "Bearer "+k {
					ok = true
				}
			}
			if !ok {
				w.WriteHeader(401)
				send(map[string]string{"error": "unauthorized"})
				return
			}
			send(map[string]any{"data": []any{map[string]string{"id": "mock-model"}}})
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+s.Secret {
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/keep-alive":
			send(map[string]string{"status": "ok"})
		case "/v8/management/config/access/api-keys":
			send([]string{s.Key})
		case "/v8/management/oauth/auth-url":
			s.Status = "wait"
			send(map[string]string{"status": "ok", "url": "https://auth.openai.com/authorize?state=mock-state", "state": "mock-state"})
		case "/v8/management/oauth/session":
			s.Status = "error"
			send(map[string]any{"status": "ok", "cancelled": true})
		case "/v8/management/oauth/status":
			send(map[string]string{"status": s.Status})
		case "/v8/management/oauth/callback":
			var v struct {
				Provider string `json:"provider"`
				URL      string `json:"redirect_url"`
			}
			_ = json.NewDecoder(r.Body).Decode(&v)
			u, e := url.Parse(v.URL)
			if e != nil || v.Provider != "codex" || u.Query().Get("state") != "mock-state" {
				w.WriteHeader(400)
				return
			}
			s.Status = "ok"
			s.Connected = true
			send(map[string]string{"status": "ok"})
		case "/v8/management/credentials":
			if r.Method == "DELETE" {
				s.Connected = false
				send(map[string]bool{"ok": true})
				return
			}
			files := []any{}
			if s.Connected {
				files = append(files, map[string]any{"name": "mock.json", "provider": "codex", "email": "demo@example.invalid", "disabled": s.Disabled, "status": "active", "refresh_token": "MUST-NOT-LEAK", "id_token": map[string]string{"plan_type": "plus"}})
			}
			send(map[string]any{"files": files})
		case "/v8/management/credentials/models":
			send(map[string]any{"models": []any{map[string]string{"id": "mock-model"}}})
		case "/v8/management/credentials/status":
			var v struct{ Disabled bool }
			_ = json.NewDecoder(r.Body).Decode(&v)
			s.Disabled = v.Disabled
			send(map[string]bool{"ok": true})
		case "/v8/management/observability/logs":
			send(map[string]any{"lines": []string{"INFO mock started", "ERROR Authorization: Bearer MUST-NOT-LEAK"}})
		default:
			w.WriteHeader(404)
			send(map[string]string{"error": "not implemented in mock"})
		}
	})
}
