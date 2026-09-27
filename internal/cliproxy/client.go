// Package cliproxy is a bounded localhost-only management client, not a reverse proxy.
package cliproxy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/OzoneH3/CLIProxyAPI_unraid/internal/bootstrap"
	"gopkg.in/yaml.v3"
)

var ErrUnauthorized = errors.New("upstream authentication rejected")

type Client struct {
	Reload               func(context.Context) error
	Base, Secret, Config string
	HTTP                 *http.Client
	mu                   sync.Mutex
	key, version         string
}

func New(base, secret, key, config string) *Client {
	return &Client{Base: base, Secret: secret, key: key, Config: config, HTTP: &http.Client{Timeout: 12 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (c *Client) Key() string     { c.mu.Lock(); defer c.mu.Unlock(); return c.key }
func (c *Client) Version() string { c.mu.Lock(); defer c.mu.Unlock(); return c.version }
func (c *Client) Do(ctx context.Context, method, path string, body any, out any) error {
	if !strings.HasPrefix(path, "/v8/management/") {
		return errors.New("unsupported management path")
	}
	version, err := c.request(ctx, method, path, c.Secret, body, out)
	if version != "" {
		c.mu.Lock()
		c.version = version
		c.mu.Unlock()
	}
	return err
}

// KeepAlive satisfies the upstream local-management watchdog enabled by
// --password. It always uses the fixed loopback endpoint.
func (c *Client) KeepAlive(ctx context.Context) error {
	_, err := c.request(ctx, "GET", "/keep-alive", c.Secret, nil, nil)
	return err
}
func (c *Client) request(ctx context.Context, method, path, key string, body, out any) (string, error) {
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			return "", err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Base+path, &buf)
	if err != nil {
		return "", errors.New("upstream request unavailable")
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", errors.New("upstream unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 {
		return "", ErrUnauthorized
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", errors.New("upstream rejected request")
	}
	if out != nil {
		if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(out); err != nil {
			return "", errors.New("invalid upstream response")
		}
	}
	return resp.Header.Get("X-CPA-VERSION"), nil
}
func (c *Client) Models(ctx context.Context) ([]string, error) { return c.models(ctx, c.Key()) }
func (c *Client) models(ctx context.Context, key string) ([]string, error) {
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	_, e := c.request(ctx, "GET", "/v1/models", key, nil, &out)
	ids := []string{}
	for _, m := range out.Data {
		if len(m.ID) < 256 {
			ids = append(ids, m.ID)
		}
	}
	return ids, e
}

type Account struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
	Email    string `json:"email,omitempty"`
	Plan     string `json:"plan,omitempty"`
	Disabled bool   `json:"disabled"`
	Status   string `json:"status"`
	Name     string `json:"-"`
	Index    string `json:"-"`
}

func accountID(name, index string) string {
	s := sha256.Sum256([]byte(name + "\x00" + index))
	return hex.EncodeToString(s[:])
}
func ValidName(s string) bool {
	return s != "" && s != "." && s != ".." && len(s) < 512 && !strings.ContainsAny(s, "/\\\x00\r\n") && filepath.Base(s) == s
}
func (c *Client) Accounts(ctx context.Context) ([]Account, error) {
	var out struct {
		Files []struct {
			Name, Provider, Type, Email, Status string
			Disabled                            bool
			AuthIndex                           string `json:"auth_index"`
			IDToken                             struct {
				Plan string `json:"plan_type"`
			} `json:"id_token"`
		} `json:"files"`
	}
	if e := c.Do(ctx, "GET", "/v8/management/credentials", nil, &out); e != nil {
		return nil, e
	}
	a := []Account{}
	for _, v := range out.Files {
		if !ValidName(v.Name) {
			continue
		}
		p := v.Provider
		if p == "" {
			p = v.Type
		}
		status := "unknown"
		switch v.Status {
		case "active", "ready", "disabled", "error", "pending":
			status = v.Status
		}
		plan := ""
		switch strings.ToLower(v.IDToken.Plan) {
		case "free", "plus", "pro", "team", "business", "enterprise", "edu":
			plan = v.IDToken.Plan
		}
		email := ""
		if len(v.Email) <= 254 && strings.Contains(v.Email, "@") && !strings.ContainsAny(v.Email, "\r\n") {
			email = v.Email
		}
		a = append(a, Account{ID: accountID(v.Name, v.AuthIndex), Provider: p, Email: email, Plan: plan, Disabled: v.Disabled, Status: status, Name: v.Name, Index: v.AuthIndex})
	}
	return a, nil
}
func (c *Client) ChangeAccount(ctx context.Context, id string, remove, disabled bool) error {
	list, e := c.Accounts(ctx)
	if e != nil {
		return e
	}
	for _, a := range list {
		if a.ID != id {
			continue
		}
		if remove {
			return c.Do(ctx, "DELETE", "/v8/management/credentials?"+url.Values{"name": {a.Name}, "auth_index": {a.Index}}.Encode(), nil, nil)
		}
		return c.Do(ctx, "PATCH", "/v8/management/credentials/status", map[string]any{"name": a.Name, "auth_index": a.Index, "disabled": disabled}, nil)
	}
	return errors.New("unknown account")
}
func (c *Client) Logs(ctx context.Context) ([]string, error) {
	var out struct {
		Lines []string `json:"lines"`
	}
	if e := c.Do(ctx, "GET", "/v8/management/observability/logs?limit=100", nil, &out); e != nil {
		return nil, e
	}
	// Upstream log messages are unstructured and can contain arbitrary credentials.
	// Return severity-only event summaries, never raw payloads or query strings.
	result := []string{}
	for _, l := range out.Lines {
		level := "INFO"
		lower := strings.ToLower(l)
		if strings.Contains(lower, "error") {
			level = "ERROR"
		} else if strings.Contains(lower, "warn") {
			level = "WARN"
		}
		result = append(result, level+": upstream event (message withheld to protect credentials)")
	}
	return result, nil
}

// Rotate atomically edits YAML because current upstream PUT /api-keys truncates
// config in place. Stage both keys, verify hot reload, then retire only the first.
func (c *Client) Rotate(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	data, e := bootstrap.ReadPrivate(c.Config)
	if e != nil {
		return "", e
	}
	var doc yaml.Node
	if e = yaml.Unmarshal(data, &doc); e != nil || len(doc.Content) != 1 {
		return "", errors.New("invalid configuration")
	}
	n := bootstrap.KeyNode(doc.Content[0])
	if n == nil || n.Kind != yaml.SequenceNode || len(n.Content) == 0 {
		return "", errors.New("no client key")
	}
	old := n.Content[0].Value
	if old != c.key {
		return "", errors.New("configuration changed; restart wrapper before rotating")
	}
	key, e := bootstrap.Secret("sk-cpa-")
	if e != nil {
		return "", e
	}
	if e = bootstrap.AtomicWrite(filepath.Join(filepath.Dir(c.Config), "state", "config.before-key-rotation.yaml"), data); e != nil {
		return "", e
	}
	n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key})
	stage, e := yaml.Marshal(&doc)
	if e != nil {
		return "", e
	}
	if e = bootstrap.AtomicWrite(c.Config, stage); e != nil {
		return "", e
	}
	rollback := func() error {
		if e := bootstrap.AtomicWrite(c.Config, data); e != nil {
			return errors.New("rotation failed and rollback requires recovery from backup")
		}
		if c.Reload != nil {
			recovery, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if e := c.Reload(recovery); e != nil {
				return errors.New("rollback saved; restart needed")
			}
		}
		return errors.New("rotation failed; original configuration restored")
	}
	if c.Reload != nil {
		if e = c.Reload(ctx); e != nil {
			return "", rollback()
		}
	}
	if e = c.waitKey(ctx, key); e != nil {
		return "", rollback()
	}
	n.Content[0].Value = key
	n.Content = n.Content[:len(n.Content)-1]
	final, e := yaml.Marshal(&doc)
	if e != nil {
		return "", rollback()
	}
	if e = bootstrap.AtomicWrite(c.Config, final); e != nil {
		return "", rollback()
	}
	if c.Reload != nil {
		if e = c.Reload(ctx); e != nil {
			return "", rollback()
		}
	}
	if e = c.waitKey(ctx, key); e != nil {
		return "", rollback()
	}
	// Wait until the retired key is actually rejected by the runtime.
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if _, e = c.models(ctx, old); errors.Is(e, ErrUnauthorized) {
			c.key = key
			return key, nil
		}
		select {
		case <-ctx.Done():
			return "", rollback()
		case <-time.After(150 * time.Millisecond):
		}
	}
	return "", rollback()
}
func (c *Client) waitKey(ctx context.Context, key string) error {
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if _, e := c.models(ctx, key); e == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
	}
	return os.ErrDeadlineExceeded
}

func (c *Client) AccountModels(ctx context.Context, id string) ([]string, error) {
	accounts, e := c.Accounts(ctx)
	if e != nil {
		return nil, e
	}
	for _, a := range accounts {
		if a.ID != id {
			continue
		}
		var out struct {
			Models []struct {
				ID string `json:"id"`
			} `json:"models"`
		}
		if e = c.Do(ctx, "GET", "/v8/management/credentials/models?"+url.Values{"name": {a.Name}}.Encode(), nil, &out); e != nil {
			return nil, e
		}
		models := []string{}
		for _, m := range out.Models {
			if len(m.ID) < 256 {
				models = append(models, m.ID)
			}
		}
		return models, nil
	}
	return nil, errors.New("unknown account")
}
