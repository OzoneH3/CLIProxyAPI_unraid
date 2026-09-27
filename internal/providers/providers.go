// Package providers maps verified upstream flows to session-bound opaque IDs.
package providers

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/OzoneH3/CLIProxyAPI_unraid/internal/bootstrap"
	"github.com/OzoneH3/CLIProxyAPI_unraid/internal/cliproxy"
)

type Spec struct {
	ID                                           string `json:"id"`
	Name                                         string `json:"name"`
	Description                                  string `json:"description"`
	CallbackProvider, CallbackHost, CallbackPath string
	Device                                       bool `json:"device"`
}

var Catalog = []Spec{
	{"codex", "OpenAI / ChatGPT", "Use your ChatGPT account through Codex OAuth", "codex", "localhost:1455", "/auth/callback", false},
	{"claude", "Claude / Anthropic", "Claude OAuth", "anthropic", "localhost:54545", "/callback", false},
	{"antigravity", "Antigravity", "Google account OAuth", "antigravity", "localhost:51121", "/oauth-callback", false},
	{"xai", "Grok / xAI", "Device-code sign-in", "xai", "", "", true},
	{"kimi", "Kimi", "Device-code sign-in", "kimi", "", "", true},
}

type Flow struct {
	ID                     string    `json:"id"`
	URL                    string    `json:"url"`
	UserCode               string    `json:"user_code,omitempty"`
	Device                 bool      `json:"device"`
	State, Owner, Provider string    `json:"-"`
	Expires                time.Time `json:"expires"`
	Submitted              bool      `json:"-"`
}
type Manager struct {
	Client *cliproxy.Client
	mu     sync.Mutex
	flows  map[string]*Flow
	Now    func() time.Time
}

func New(c *cliproxy.Client) *Manager {
	return &Manager{Client: c, flows: map[string]*Flow{}, Now: time.Now}
}
func Find(id string) (Spec, bool) {
	for _, s := range Catalog {
		if s.ID == id {
			return s, true
		}
	}
	return Spec{}, false
}
func (m *Manager) Start(ctx context.Context, provider, owner string) (Flow, error) {
	s, ok := Find(provider)
	if !ok {
		return Flow{}, errors.New("unsupported provider")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.Now()
	for id, f := range m.flows {
		if !now.Before(f.Expires) {
			delete(m.flows, id)
		}
	}
	for _, f := range m.flows {
		if f.Provider == provider {
			return Flow{}, errors.New("a login is already pending for this provider")
		}
	}
	if len(m.flows) >= 20 {
		return Flow{}, errors.New("too many login flows")
	}
	var out struct {
		URL, State, Flow string
		UserCode         string `json:"user_code"`
		Expires          int    `json:"expires_in"`
	}
	if e := m.Client.Do(ctx, "GET", "/v8/management/oauth/auth-url?"+url.Values{"provider": {s.ID}, "is_webui": {"true"}}.Encode(), nil, &out); e != nil {
		return Flow{}, e
	}
	u, e := url.Parse(out.URL)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || out.State == "" || len(out.State) > 512 {
		return Flow{}, errors.New("invalid authorization response")
	}
	id, e := bootstrap.Secret("")
	if e != nil {
		return Flow{}, e
	}
	ttl := 10 * time.Minute
	if s.Device && out.Expires > 0 && out.Expires <= 1800 {
		ttl = time.Duration(out.Expires) * time.Second
	}
	f := Flow{ID: id, URL: out.URL, UserCode: out.UserCode, Device: s.Device, State: out.State, Owner: owner, Provider: provider, Expires: now.Add(ttl)}
	m.flows[id] = &f
	return f, nil
}
func (m *Manager) get(id, owner, provider string) (*Flow, error) {
	f, ok := m.flows[id]
	if !ok || f.Owner != owner || f.Provider != provider || !m.Now().Before(f.Expires) {
		return nil, errors.New("unknown or expired login")
	}
	return f, nil
}
func (m *Manager) Status(ctx context.Context, id, owner, provider string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, e := m.get(id, owner, provider)
	if e != nil {
		return "", e
	}
	var out struct{ Status string }
	if e = m.Client.Do(ctx, "GET", "/v8/management/oauth/status?"+url.Values{"state": {f.State}}.Encode(), nil, &out); e != nil {
		return "", e
	}
	switch out.Status {
	case "wait":
		return "waiting", nil
	case "ok":
		delete(m.flows, id)
		return "connected", nil
	case "error":
		delete(m.flows, id)
		return "error", nil
	}
	return "", errors.New("invalid authentication status")
}
func (m *Manager) Callback(ctx context.Context, id, owner, provider, raw string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, e := m.get(id, owner, provider)
	if e != nil {
		return e
	}
	s, _ := Find(provider)
	if f.Submitted || s.Device || len(raw) > 8192 {
		return errors.New("invalid callback")
	}
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "http" || u.Host != s.CallbackHost || u.Path != s.CallbackPath || u.User != nil || u.Fragment != "" || u.RawPath != "" {
		return errors.New("invalid callback URL")
	}
	q, e := url.ParseQuery(u.RawQuery)
	if e != nil {
		return errors.New("invalid callback query")
	}
	for k, v := range q {
		if len(v) != 1 {
			return errors.New("duplicate callback parameter")
		}
		switch k {
		case "state", "code", "error", "error_description", "scope", "authuser", "prompt":
		default:
			return errors.New("unexpected callback parameter")
		}
	}
	if q.Get("state") != f.State || strings.TrimSpace(q.Get("code")) == "" && q.Get("error") == "" {
		return errors.New("callback does not match this login")
	}
	// This URL is data sent to a fixed local endpoint. It is never fetched.
	if e = m.Client.Do(ctx, "POST", "/v8/management/oauth/callback", map[string]string{"provider": s.CallbackProvider, "redirect_url": raw}, nil); e != nil {
		return e
	}
	f.Submitted = true
	return nil
}

// Exclusive prevents configuration restarts from interrupting active OAuth sessions.
func (m *Manager) Exclusive(fn func() error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, f := range m.flows {
		if !m.Now().Before(f.Expires) {
			delete(m.flows, id)
		} else {
			return errors.New("finish or cancel the pending provider login first")
		}
	}
	return fn()
}
func (m *Manager) Cancel(ctx context.Context, id, owner, provider string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, e := m.get(id, owner, provider)
	if e != nil {
		return e
	}
	if e = m.Client.Do(ctx, "DELETE", "/v8/management/oauth/session?"+url.Values{"state": {f.State}}.Encode(), nil, nil); e != nil {
		return e
	}
	delete(m.flows, id)
	return nil
}
