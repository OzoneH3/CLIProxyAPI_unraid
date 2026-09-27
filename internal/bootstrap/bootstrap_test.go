package bootstrap

import (
	"golang.org/x/crypto/bcrypt"
	"gopkg.in/yaml.v3"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitializeAndPreserve(t *testing.T) {
	dir := t.TempDir()
	r, e := Init(dir, "very-long-test-password")
	if e != nil {
		t.Fatal(e)
	}
	if len(r.ClientKey) != 71 || !strings.HasPrefix(r.ClientKey, "sk-cpa-") || r.ClientKey == r.LocalSecret {
		t.Fatal("invalid keys")
	}
	b, e := os.ReadFile(r.ConfigPath)
	if e != nil {
		t.Fatal(e)
	}
	var c map[string]any
	if e = yaml.Unmarshal(b, &c); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"auths", "logs", "plugins", "state"} {
		fi, e := os.Stat(filepath.Join(dir, name))
		if e != nil || fi.Mode().Perm() != 0700 {
			t.Fatal("directory permissions", name)
		}
	}
	for _, name := range []string{"config.yaml", "state/password.hash", "state/local-management.key"} {
		fi, e := os.Stat(filepath.Join(dir, name))
		if e != nil || fi.Mode().Perm() != 0600 {
			t.Fatal("file permissions", name)
		}
	}
	if strings.Contains(string(b), "very-long-test-password") {
		t.Fatal("password leaked")
	}
	beforeHash, _ := os.ReadFile(filepath.Join(dir, "state/password.hash"))
	r2, e := Init(dir, "very-long-test-password")
	if e != nil {
		t.Fatal(e)
	}
	after, _ := os.ReadFile(r.ConfigPath)
	afterHash, _ := os.ReadFile(filepath.Join(dir, "state/password.hash"))
	if string(b) != string(after) || string(beforeHash) != string(afterHash) || r2.ClientKey != r.ClientKey {
		t.Fatal("non-idempotent bootstrap")
	}
	r3, e := Init(dir, "changed-test-password")
	if e != nil || bcrypt.CompareHashAndPassword(r3.PasswordHash, []byte("changed-test-password")) != nil {
		t.Fatal("rotation failed")
	}
}
func TestMigrationKeepsUnknownAndCredentials(t *testing.T) {
	dir := t.TempDir()
	original := "# my comment\nhost: ''\nport: 8317\nauth-dir: /root/.cli-proxy-api\napi-keys: [keep-me]\nremote-management:\n  secret-key: existing-secret\nplugins:\n  dir: /CLIProxyAPI/plugins\ncustom-future-field: keep-this # important\n"
	_ = os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(original), 0600)
	_ = os.Mkdir(filepath.Join(dir, "auths"), 0700)
	_ = os.WriteFile(filepath.Join(dir, "auths", "account.json"), []byte("private"), 0600)
	r, e := Init(dir, "very-long-test-password")
	if e != nil {
		t.Fatal(e)
	}
	if !r.Migrated || r.ClientKey != "keep-me" {
		t.Fatal("migration failed")
	}
	b, _ := os.ReadFile(r.ConfigPath)
	for _, s := range []string{"my comment", "keep-this", "important", "existing-secret", filepath.Join(dir, "auths")} {
		if !strings.Contains(string(b), s) {
			t.Fatal("lost", s)
		}
	}
	backup, _ := os.ReadFile(filepath.Join(dir, "state", "config.before-wrapper-migration.yaml"))
	if string(backup) != original {
		t.Fatal("backup not exact")
	}
	auth, _ := os.ReadFile(filepath.Join(dir, "auths", "account.json"))
	if string(auth) != "private" {
		t.Fatal("credential changed")
	}
}
func TestExistingUnchanged(t *testing.T) {
	dir := t.TempDir()
	b := []byte("host: ''\nport: 8317\nauth-dir: /data/auths\napi-keys: [existing-key]\nunknown: value\nremote-management: {secret-key: preserve}\n")
	_ = os.WriteFile(filepath.Join(dir, "config.yaml"), b, 0600)
	_, e := Init(dir, "very-long-test-password")
	if e != nil {
		t.Fatal(e)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "config.yaml"))
	if string(b) != string(after) {
		t.Fatal("overwrote config")
	}
}
func TestRejectNonregularAndInvalid(t *testing.T) {
	for _, kind := range []string{"directory", "symlink", "yaml"} {
		t.Run(kind, func(t *testing.T) {
			d := t.TempDir()
			p := filepath.Join(d, "config.yaml")
			switch kind {
			case "directory":
				_ = os.Mkdir(p, 0700)
			case "symlink":
				_ = os.Symlink("/etc/passwd", p)
			case "yaml":
				_ = os.WriteFile(p, []byte("[invalid"), 0600)
			}
			if _, e := Init(d, "very-long-test-password"); e == nil {
				t.Fatal("accepted unsafe config")
			}
		})
	}
}
func TestSecretUniqueness(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		s, e := Secret("")
		if e != nil || len(s) != 64 || seen[s] {
			t.Fatal("bad entropy/format")
		}
		seen[s] = true
	}
}

func TestV8PrecedenceAndNoRewrite(t *testing.T) {
	d := t.TempDir()
	content := []byte("# v8 wins\nconfig-version: 8\nserver: {host: '', port: 8317}\nport: 9999\noauth: {auth-dir: /data/auths}\nauth-dir: ignored-legacy\naccess: {api-keys: [v8-key]}\napi-keys: [legacy-key]\nmanagement: {secret-key: existing}\n")
	_ = os.WriteFile(filepath.Join(d, "config.yaml"), content, 0600)
	r, e := Init(d, "very-long-test-password")
	if e != nil {
		t.Fatal(e)
	}
	if r.ClientKey != "v8-key" {
		t.Fatal("ignored precedence")
	}
	after, _ := os.ReadFile(r.ConfigPath)
	if string(content) != string(after) {
		t.Fatal("rewrote valid v8 config")
	}
}
func TestMissingManagementSecretProvisionedWithBackup(t *testing.T) {
	d := t.TempDir()
	content := []byte("host: ''\nport: 8317\nauth-dir: /data/auths\napi-keys: [preserved]\nremote-management: {secret-key: '', allow-remote: false}\n")
	_ = os.WriteFile(filepath.Join(d, "config.yaml"), content, 0600)
	r, e := Init(d, "very-long-test-password")
	if e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(r.ConfigPath)
	var doc yaml.Node
	_ = yaml.Unmarshal(b, &doc)
	h := At(doc.Content[0], "remote-management", "secret-key")
	if bcrypt.CompareHashAndPassword([]byte(h.Value), []byte(r.LocalSecret)) != nil {
		t.Fatal("management bootstrap failed")
	}
	backup, _ := os.ReadFile(filepath.Join(d, "state", "config.before-wrapper-migration.yaml"))
	if string(backup) != string(content) || r.ClientKey != "preserved" {
		t.Fatal("migration destroyed data")
	}
}

func TestRejectedConfigIsNotMigrated(t *testing.T) {
	dir := t.TempDir()
	original := []byte("port: 9999\nauth-dir: /root/.cli-proxy-api\napi-keys: [existing-key]\n")
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Init(dir, "test-password-long-enough"); err == nil {
		t.Fatal("expected unsupported port rejection")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatal("rejected configuration was rewritten")
	}
}
