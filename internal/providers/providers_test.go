package providers

import (
	"context"
	"encoding/json"
	"github.com/OzoneH3/CLIProxyAPI_unraid/internal/cliproxy"
	"github.com/OzoneH3/CLIProxyAPI_unraid/internal/mock"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T) (*Manager, *mock.Server) {
	t.Helper()
	m := mock.New()
	s := httptest.NewServer(m.Handler())
	t.Cleanup(s.Close)
	return New(cliproxy.New(s.URL, m.Secret, m.Key, "")), m
}
func TestOAuth(t *testing.T) {
	m, _ := fixture(t)
	ctx := context.Background()
	f, e := m.Start(ctx, "codex", "owner")
	if e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(f)
	if strings.Contains(string(b), `"state"`) || strings.Contains(string(b), "mock-management-only") {
		t.Fatal("private fields leaked")
	}
	status, e := m.Status(ctx, f.ID, "owner", "codex")
	if e != nil || status != "waiting" {
		t.Fatal(status, e)
	}
	if _, e = m.Status(ctx, f.ID, "other", "codex"); e == nil {
		t.Fatal("flow not owner bound")
	}
	for _, raw := range []string{"https://localhost:1455/auth/callback?state=mock-state&code=x", "http://evil.test:1455/auth/callback?state=mock-state&code=x", "http://localhost:1455/wrong?state=mock-state&code=x", "http://localhost:1455/auth/callback?state=wrong&code=x", "http://localhost:1455/auth/callback?state=mock-state&state=other&code=x"} {
		if e = m.Callback(ctx, f.ID, "owner", "codex", raw); e == nil {
			t.Fatal("accepted", raw)
		}
	}
	if e = m.Callback(ctx, f.ID, "owner", "codex", "http://localhost:1455/auth/callback?state=mock-state&code=private"); e != nil {
		t.Fatal(e)
	}
	if e = m.Callback(ctx, f.ID, "owner", "codex", "http://localhost:1455/auth/callback?state=mock-state&code=private"); e == nil {
		t.Fatal("replay")
	}
	status, e = m.Status(ctx, f.ID, "owner", "codex")
	if e != nil || status != "connected" {
		t.Fatal(status, e)
	}
}
func TestFailureAndExpiry(t *testing.T) {
	m, up := fixture(t)
	ctx := context.Background()
	f, _ := m.Start(ctx, "codex", "owner")
	up.Status = "error"
	status, e := m.Status(ctx, f.ID, "owner", "codex")
	if e != nil || status != "error" {
		t.Fatal(status, e)
	}
	f, _ = m.Start(ctx, "codex", "owner")
	m.Now = func() time.Time { return time.Now().Add(time.Hour) }
	if _, e = m.Status(ctx, f.ID, "owner", "codex"); e == nil {
		t.Fatal("expired accepted")
	}
}
func TestDeviceFlow(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v8/management/oauth/auth-url" || r.URL.Query().Get("provider") != "xai" {
			t.Error("wrong endpoint")
		}
		if r.URL.Query().Get("is_webui") != "true" {
			t.Error("missing webui flag")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"url": "https://accounts.x.ai/device", "state": "device-state", "flow": "device", "user_code": "TEST-CODE", "expires_in": 900})
	}))
	defer s.Close()
	m := New(cliproxy.New(s.URL, "secret", "key", ""))
	f, e := m.Start(context.Background(), "xai", "owner")
	if e != nil || !f.Device || f.UserCode != "TEST-CODE" {
		t.Fatal(f, e)
	}
	if e = m.Callback(context.Background(), f.ID, "owner", "xai", "http://localhost/"); e == nil {
		t.Fatal("device accepts callback")
	}
}

func TestCancelAndRotationGuard(t *testing.T) {
	m, _ := fixture(t)
	ctx := context.Background()
	f, e := m.Start(ctx, "codex", "owner")
	if e != nil {
		t.Fatal(e)
	}
	called := false
	if e = m.Exclusive(func() error { called = true; return nil }); e == nil || called {
		t.Fatal("interrupted active flow")
	}
	if e = m.Cancel(ctx, f.ID, "other", "codex"); e == nil {
		t.Fatal("cancelled someone else's flow")
	}
	if e = m.Cancel(ctx, f.ID, "owner", "codex"); e != nil {
		t.Fatal(e)
	}
	if e = m.Exclusive(func() error { called = true; return nil }); e != nil || !called {
		t.Fatal("guard not released")
	}
}
func TestAllProviderEndpoints(t *testing.T) {
	for _, spec := range Catalog {
		t.Run(spec.ID, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v8/management/oauth/auth-url" || r.URL.Query().Get("provider") != spec.ID || r.URL.Query().Get("is_webui") != "true" {
					t.Error("incorrect provider endpoint")
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"url": "https://provider.example/authorize", "state": "test-state", "flow": "device", "user_code": "TEST", "expires_in": 300})
			}))
			defer s.Close()
			m := New(cliproxy.New(s.URL, "private-management", "key", ""))
			f, e := m.Start(context.Background(), spec.ID, "owner")
			if e != nil || f.Device != spec.Device {
				t.Fatal(f, e)
			}
		})
	}
}
