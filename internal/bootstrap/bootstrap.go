// Package bootstrap owns private storage and conservative, backed-up migrations.
package bootstrap

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/bcrypt"
	"gopkg.in/yaml.v3"
)

func Secret(prefix string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(b), nil
}

// AtomicWrite never exposes a partially written file. Callers serialize updates.
func AtomicWrite(path string, data []byte) error {
	if fi, err := os.Lstat(path); err == nil && !fi.Mode().IsRegular() {
		return errors.New("destination is not a regular file")
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".cpa-write-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err = f.Chmod(0600); err != nil {
		return err
	}
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func PrivateDir(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		return errors.New("storage directory must not be a symlink")
	}
	return os.Chmod(path, 0700)
}
func ReadPrivate(path string) ([]byte, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, errors.New("private file must be regular")
	}
	if err := os.Chmod(path, 0600); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

type Result struct {
	ConfigPath, LocalSecret, ClientKey string
	PasswordHash                       []byte
	Migrated                           bool
}

func Init(dir, password string) (*Result, error) {
	if len(password) < 12 || len(password) > 72 {
		return nil, errors.New("WEBUI_PASSWORD must be 12 to 72 bytes")
	}
	for _, sub := range []string{"", "auths", "logs", "plugins", "state"} {
		if err := PrivateDir(filepath.Join(dir, sub)); err != nil {
			return nil, err
		}
	}
	r := &Result{ConfigPath: filepath.Join(dir, "config.yaml")}
	secretPath := filepath.Join(dir, "state", "local-management.key")
	secret, err := ReadPrivate(secretPath)
	if errors.Is(err, os.ErrNotExist) {
		s, e := Secret("mgmt-")
		if e != nil {
			return nil, e
		}
		secret = []byte(s)
		err = AtomicWrite(secretPath, secret)
	}
	if err != nil {
		return nil, err
	}
	r.LocalSecret = string(secret)
	if len(r.LocalSecret) < 32 {
		return nil, errors.New("invalid local management state")
	}
	hashPath := filepath.Join(dir, "state", "password.hash")
	hash, err := ReadPrivate(hashPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err == nil {
		if _, e := bcrypt.Cost(hash); e != nil {
			return nil, errors.New("invalid password hash")
		}
	}
	// Explicitly changing WEBUI_PASSWORD rotates the hash; unchanged values do not rewrite it.
	if err != nil || bcrypt.CompareHashAndPassword(hash, []byte(password)) != nil {
		hash, err = bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			return nil, err
		}
		if err = AtomicWrite(hashPath, hash); err != nil {
			return nil, err
		}
	}
	r.PasswordHash = hash
	data, err := ReadPrivate(r.ConfigPath)
	if errors.Is(err, os.ErrNotExist) {
		key, e := Secret("sk-cpa-")
		if e != nil {
			return nil, e
		}
		mgmtHash, e := bcrypt.GenerateFromPassword([]byte(r.LocalSecret), bcrypt.DefaultCost)
		if e != nil {
			return nil, e
		}
		cfg := map[string]any{
			"config-version": 8,
			"server":         map[string]any{"host": "", "port": 8317},
			"oauth":          map[string]any{"auth-dir": filepath.Join(dir, "auths")},
			"access":         map[string]any{"api-keys": []string{key}},
			"observability":  map[string]any{"logs": map[string]any{"debug": false, "logging-to-file": true, "logs-max-total-size-mb": 100}},
			"management":     map[string]any{"allow-remote": false, "secret-key": string(mgmtHash), "disable-control-panel": true},
			"plugins":        map[string]any{"enabled": false, "dir": filepath.Join(dir, "plugins")},
		}
		data, err = yaml.Marshal(cfg)
		if err != nil {
			return nil, err
		}
		if err = AtomicWrite(r.ConfigPath, data); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	var doc yaml.Node
	if err = yaml.Unmarshal(data, &doc); err != nil {
		return nil, errors.New("invalid config.yaml; existing file preserved")
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("config.yaml must be a mapping")
	}
	// Node editing retains unknown fields and comments; formatting may normalize.
	changed := false
	auth := Effective(doc.Content[0], []string{"oauth", "auth-dir"}, "auth-dir")
	if auth != nil && (auth.Value == "/root/.cli-proxy-api" || auth.Value == "~/.cli-proxy-api") {
		auth.Value = filepath.Join(dir, "auths")
		changed = true
	}
	plugins := Field(doc.Content[0], "plugins")
	if plugins != nil {
		if p := Field(plugins, "dir"); p != nil && p.Value == "/CLIProxyAPI/plugins" {
			p.Value = filepath.Join(dir, "plugins")
			changed = true
		}
	}
	remote := Field(doc.Content[0], "management")
	remoteName := "management"
	if remote == nil {
		remote = Field(doc.Content[0], "remote-management")
		remoteName = "remote-management"
	}
	if remote == nil {
		remote = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		doc.Content[0].Content = append(doc.Content[0].Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: remoteName}, remote)
	}
	if remote.Kind != yaml.MappingNode {
		return nil, errors.New("remote-management must be a mapping")
	}
	secretNode := Field(remote, "secret-key")
	if secretNode == nil || secretNode.Value == "" {
		hash, e := bcrypt.GenerateFromPassword([]byte(r.LocalSecret), bcrypt.DefaultCost)
		if e != nil {
			return nil, e
		}
		if secretNode == nil {
			secretNode = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str"}
			remote.Content = append(remote.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "secret-key"}, secretNode)
		}
		secretNode.Value = string(hash)
		changed = true
	}
	root := doc.Content[0]
	var host string
	var port int
	var tls bool
	var keys []string
	for _, pair := range []struct {
		node *yaml.Node
		out  any
	}{
		{Effective(root, []string{"server", "host"}, "host"), &host},
		{Effective(root, []string{"server", "port"}, "port"), &port},
		{Effective(root, []string{"server", "tls", "enable"}, "tls", "enable"), &tls},
		{KeyNode(root), &keys},
	} {
		if pair.node != nil {
			if e := pair.node.Decode(pair.out); e != nil {
				return nil, errors.New("invalid configuration types")
			}
		}
	}
	if port != 8317 || tls || (host != "" && host != "0.0.0.0" && host != "127.0.0.1") {
		return nil, errors.New("existing config requires port 8317, HTTP, and a loopback-reachable host; file preserved")
	}
	if len(keys) == 0 || strings.TrimSpace(keys[0]) == "" {
		return nil, fmt.Errorf("existing config needs a client API key; file preserved")
	}
	if changed {
		backup := filepath.Join(dir, "state", "config.before-wrapper-migration.yaml")
		if _, e := os.Lstat(backup); errors.Is(e, os.ErrNotExist) {
			if e = AtomicWrite(backup, data); e != nil {
				return nil, e
			}
		} else if e != nil {
			return nil, e
		}
		data, err = yaml.Marshal(&doc)
		if err != nil {
			return nil, err
		}
		if err = AtomicWrite(r.ConfigPath, data); err != nil {
			return nil, err
		}
		r.Migrated = true
	}
	r.ClientKey = keys[0]
	return r, nil
}
func Field(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

// At and Effective honor v8 precedence without rewriting legacy documents.
func At(root *yaml.Node, path ...string) *yaml.Node {
	n := root
	for _, key := range path {
		n = Field(n, key)
	}
	return n
}
func Effective(root *yaml.Node, current []string, legacy ...string) *yaml.Node {
	if n := At(root, current...); n != nil {
		return n
	}
	return At(root, legacy...)
}
func KeyNode(root *yaml.Node) *yaml.Node {
	return Effective(root, []string{"access", "api-keys"}, "api-keys")
}
