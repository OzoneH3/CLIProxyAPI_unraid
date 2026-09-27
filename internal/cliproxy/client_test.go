package cliproxy

import (
	"context"
	"encoding/json"
	"github.com/OzoneH3/CLIProxyAPI_unraid/internal/bootstrap"
	"github.com/OzoneH3/CLIProxyAPI_unraid/internal/mock"
	"gopkg.in/yaml.v3"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAccountsAndLogsDoNotLeak(t *testing.T) {
	m := mock.New()
	m.Connected = true
	s := httptest.NewServer(m.Handler())
	defer s.Close()
	c := New(s.URL, m.Secret, m.Key, "")
	if e := c.KeepAlive(context.Background()); e != nil {
		t.Fatal(e)
	}
	a, e := c.Accounts(context.Background())
	if e != nil || len(a) != 1 {
		t.Fatal(a, e)
	}
	b, _ := json.Marshal(a)
	if strings.Contains(string(b), "MUST-NOT-LEAK") || strings.Contains(string(b), "mock.json") {
		t.Fatal("credential data leaked")
	}
	if a[0].Email != "demo@example.invalid" || a[0].Plan != "plus" {
		t.Fatal("missing safe metadata")
	}
	for _, bad := range []string{"../secret.json", "/etc/passwd", "..", "a\\b", "a\x00b"} {
		if ValidName(bad) {
			t.Fatal("unsafe name")
		}
		if e = c.ChangeAccount(context.Background(), bad, true, false); e == nil {
			t.Fatal("unsafe deletion")
		}
	}
	if e = c.ChangeAccount(context.Background(), a[0].ID, false, true); e != nil {
		t.Fatal(e)
	}
	a, _ = c.Accounts(context.Background())
	if !a[0].Disabled {
		t.Fatal("disable failed")
	}
	logs, e := c.Logs(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(strings.Join(logs, ""), "MUST-NOT-LEAK") {
		t.Fatal("log leaked")
	}
	if e = c.ChangeAccount(context.Background(), a[0].ID, true, false); e != nil {
		t.Fatal(e)
	}
	a, _ = c.Accounts(context.Background())
	if len(a) != 0 {
		t.Fatal("delete failed")
	}
}
func TestRotation(t *testing.T) {
	dir := t.TempDir()
	r, e := bootstrap.Init(dir, "very-long-test-password")
	if e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(r.ConfigPath)
	b = append(b, []byte("unknown-setting: preserved # comment\n")...)
	_ = os.WriteFile(r.ConfigPath, b, 0600)
	m := mock.New()
	m.Config = r.ConfigPath
	s := httptest.NewServer(m.Handler())
	defer s.Close()
	c := New(s.URL, m.Secret, r.ClientKey, r.ConfigPath)
	key, e := c.Rotate(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if key == r.ClientKey || !strings.HasPrefix(key, "sk-cpa-") {
		t.Fatal("invalid rotated key")
	}
	after, _ := os.ReadFile(r.ConfigPath)
	var cfg map[string]any
	if e = yaml.Unmarshal(after, &cfg); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(after), r.ClientKey) || !strings.Contains(string(after), "unknown-setting: preserved # comment") {
		t.Fatal("unsafe update")
	}
	backup, _ := os.ReadFile(filepath.Join(dir, "state", "config.before-key-rotation.yaml"))
	if string(backup) != string(b) {
		t.Fatal("missing exact backup")
	}
}
func TestRotationRollsBack(t *testing.T) {
	dir := t.TempDir()
	r, _ := bootstrap.Init(dir, "very-long-test-password")
	original, _ := os.ReadFile(r.ConfigPath)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer s.Close()
	c := New(s.URL, "secret", r.ClientKey, r.ConfigPath)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, e := c.Rotate(ctx); e == nil {
		t.Fatal("reported success")
	}
	after, _ := os.ReadFile(r.ConfigPath)
	if string(after) != string(original) {
		t.Fatal("rollback lost config")
	}
}
