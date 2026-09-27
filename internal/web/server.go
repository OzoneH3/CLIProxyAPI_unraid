package web

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/OzoneH3/CLIProxyAPI_unraid/internal/cliproxy"
	"github.com/OzoneH3/CLIProxyAPI_unraid/internal/providers"
	"github.com/OzoneH3/CLIProxyAPI_unraid/internal/security"
	assets "github.com/OzoneH3/CLIProxyAPI_unraid/web"
)

type Server struct {
	Client    *cliproxy.Client
	Auth      *security.Auth
	Providers *providers.Manager
	Alive     atomic.Bool
	APIPort   int
	Started   time.Time
}

func New(c *cliproxy.Client, a *security.Auth) *Server {
	return &Server{Client: c, Auth: a, Providers: providers.New(c), APIPort: 8317, Started: time.Now()}
}
func reply(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}
func problem(w http.ResponseWriter, status int, msg string) {
	reply(w, status, map[string]string{"error": msg})
}
func decode(w http.ResponseWriter, r *http.Request, out any) bool {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		problem(w, 415, "JSON required")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16384)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(out) != nil {
		problem(w, 400, "Invalid request")
		return false
	}
	if d.Decode(&struct{}{}) != io.EOF {
		problem(w, 400, "Invalid request")
		return false
	}
	return true
}
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if !s.Alive.Load() {
			problem(w, 503, "Starting")
			return
		}
		if _, e := s.Client.Models(r.Context()); e != nil {
			problem(w, 503, "Upstream not ready")
			return
		}
		reply(w, 200, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if !s.Alive.Load() {
			problem(w, 503, "Starting")
			return
		}
		var out []string
		if e := s.Client.Do(r.Context(), "GET", "/v8/management/config/access/api-keys", nil, &out); e != nil {
			problem(w, 503, "Management unavailable")
			return
		}
		reply(w, 200, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("POST /api/login", func(w http.ResponseWriter, r *http.Request) {
		if !security.SameOrigin(r) {
			problem(w, 403, "Origin rejected")
			return
		}
		var in struct {
			Password string `json:"password"`
		}
		if !decode(w, r, &in) {
			return
		}
		token, session, status := s.Auth.Login(security.IP(r), in.Password)
		if status != 200 {
			problem(w, status, "Login failed or temporarily rate limited")
			return
		}
		s.Auth.Cookie(w, token, session.Expires)
		reply(w, 200, map[string]string{"csrf": session.CSRF})
	})
	mux.HandleFunc("/api/", s.api)
	files := http.FileServer(http.FS(assets.Static))
	mux.Handle("/", files)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { security.Headers(w); mux.ServeHTTP(w, r) })
}
func (s *Server) api(w http.ResponseWriter, r *http.Request) {
	session, ok := s.Auth.Get(r)
	if !ok {
		problem(w, 401, "Sign in required")
		return
	}
	if r.Method != "GET" && !security.CSRF(r, session) {
		problem(w, 403, "CSRF rejected")
		return
	}
	path := r.URL.Path
	switch {
	case path == "/api/session" && r.Method == "GET":
		reply(w, 200, map[string]string{"csrf": session.CSRF})
	case path == "/api/logout" && r.Method == "POST":
		s.Auth.Logout(r)
		s.Auth.Cookie(w, "", time.Unix(0, 0))
		reply(w, 200, map[string]bool{"ok": true})
	case path == "/api/dashboard" && r.Method == "GET":
		status := "Running"
		models, e := s.Client.Models(r.Context())
		if e != nil || !s.Alive.Load() {
			status = "Error"
			if time.Since(s.Started) < 45*time.Second {
				status = "Starting"
			}
		}
		// The browser constructs the hostname from its own URL; no trusted Host-header URL generation.
		key := s.Client.Key()
		suffix := ""
		if len(key) > 4 {
			suffix = key[len(key)-4:]
		}
		reply(w, 200, map[string]any{"status": status, "version": s.Client.Version(), "api_port": s.APIPort, "models": models, "key_hint": "sk-cpa-••••••••" + suffix})
	case path == "/api/key" && r.Method == "GET":
		reply(w, 200, map[string]string{"key": s.Client.Key()})
	case path == "/api/key/rotate" && r.Method == "POST":
		var in struct {
			Confirm bool `json:"confirm"`
		}
		if !decode(w, r, &in) {
			return
		}
		if !in.Confirm {
			problem(w, 400, "Confirmation required")
			return
		}
		e := s.Providers.Exclusive(func() error { _, err := s.Client.Rotate(r.Context()); return err })
		if e != nil {
			problem(w, 502, "Key rotation unavailable; finish or cancel provider logins first. If it still fails, see recovery instructions")
			return
		}
		reply(w, 200, map[string]bool{"ok": true})
	case path == "/api/providers" && r.Method == "GET":
		type card struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			Description string `json:"description"`
			Device      bool   `json:"device"`
		}
		cards := []card{}
		for _, p := range providers.Catalog {
			cards = append(cards, card{p.ID, p.Name, p.Description, p.Device})
		}
		reply(w, 200, cards)
	case path == "/api/accounts" && r.Method == "GET":
		a, e := s.Client.Accounts(r.Context())
		if e != nil {
			problem(w, 502, "Accounts unavailable")
			return
		}
		reply(w, 200, a)
	case strings.HasPrefix(path, "/api/accounts/") && strings.HasSuffix(path, "/models") && r.Method == "GET":
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/api/accounts/"), "/models")
		if len(id) != 64 {
			problem(w, 400, "Invalid account")
			return
		}
		models, e := s.Client.AccountModels(r.Context(), id)
		if e != nil {
			problem(w, 502, "Account models unavailable")
			return
		}
		reply(w, 200, models)
	case path == "/api/accounts" && (r.Method == "DELETE" || r.Method == "PATCH"):
		var in struct {
			ID       string `json:"id"`
			Confirm  bool   `json:"confirm"`
			Disabled bool   `json:"disabled"`
		}
		if !decode(w, r, &in) {
			return
		}
		if r.Method == "DELETE" && !in.Confirm {
			problem(w, 400, "Confirm disconnect first")
			return
		}
		if len(in.ID) != 64 {
			problem(w, 400, "Invalid account")
			return
		}
		if e := s.Client.ChangeAccount(r.Context(), in.ID, r.Method == "DELETE", in.Disabled); e != nil {
			problem(w, 502, "Account operation failed")
			return
		}
		reply(w, 200, map[string]bool{"ok": true})
	case path == "/api/logs" && r.Method == "GET":
		logs, e := s.Client.Logs(r.Context())
		if e != nil {
			problem(w, 502, "Logs unavailable")
			return
		}
		reply(w, 200, logs)
	case strings.HasPrefix(path, "/api/providers/"):
		parts := strings.Split(strings.TrimPrefix(path, "/api/providers/"), "/")
		if len(parts) < 2 {
			problem(w, 404, "Not found")
			return
		}
		provider := parts[0]
		if _, ok := providers.Find(provider); !ok {
			problem(w, 404, "Unknown provider")
			return
		}
		if len(parts) == 2 && parts[1] == "connect" && r.Method == "POST" {
			flow, e := s.Providers.Start(r.Context(), provider, session.ID)
			if e != nil {
				problem(w, 502, "Login unavailable or already pending; wait for expiry before retrying")
				return
			}
			reply(w, 200, flow)
			return
		}
		if len(parts) == 3 && parts[1] == "status" && r.Method == "GET" {
			status, e := s.Providers.Status(r.Context(), parts[2], session.ID, provider)
			if e != nil {
				problem(w, 400, "Login expired or unavailable")
				return
			}
			reply(w, 200, map[string]string{"status": status})
			return
		}
		if len(parts) == 3 && parts[1] == "cancel" && r.Method == "POST" {
			if e := s.Providers.Cancel(r.Context(), parts[2], session.ID, provider); e != nil {
				problem(w, 400, "Could not cancel this login")
				return
			}
			reply(w, 200, map[string]bool{"ok": true})
			return
		}
		if len(parts) == 3 && parts[1] == "callback" && r.Method == "POST" {
			var in struct {
				URL string `json:"url"`
			}
			if !decode(w, r, &in) {
				return
			}
			if e := s.Providers.Callback(r.Context(), parts[2], session.ID, provider, in.URL); e != nil {
				problem(w, 400, "Callback rejected; use the complete URL for this login")
				return
			}
			reply(w, 200, map[string]bool{"ok": true})
			return
		}
		problem(w, 404, "Not found")
	default:
		problem(w, 404, "Not found")
	}
}
func ParsePort(raw string) (int, error) {
	if raw == "" {
		return 8317, nil
	}
	n, e := strconv.Atoi(raw)
	if e != nil || n < 1 || n > 65535 {
		return 0, strconv.ErrSyntax
	}
	return n, nil
}
