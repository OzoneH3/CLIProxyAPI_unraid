package security

import (
	"crypto/sha256"
	"crypto/subtle"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/OzoneH3/CLIProxyAPI_unraid/internal/bootstrap"
	"golang.org/x/crypto/bcrypt"
)

type Session struct {
	ID, CSRF string
	Expires  time.Time
}
type attempt struct {
	Count int
	Until time.Time
}
type Auth struct {
	mu       sync.Mutex
	hash     []byte
	sessions map[[32]byte]Session
	attempts map[string]attempt
	Now      func() time.Time
	TTL      time.Duration
	Secure   bool
}

func New(hash []byte, secure bool) *Auth {
	return &Auth{hash: hash, sessions: map[[32]byte]Session{}, attempts: map[string]attempt{}, Now: time.Now, TTL: 8 * time.Hour, Secure: secure}
}
func (a *Auth) Login(ip, password string) (string, Session, int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.Now()
	for k, v := range a.attempts {
		if !now.Before(v.Until) {
			delete(a.attempts, k)
		}
	}
	v := a.attempts[ip]
	if v.Count >= 5 && now.Before(v.Until) {
		return "", Session{}, 429
	}
	if len(a.attempts) >= 4096 {
		return "", Session{}, 429
	}
	v.Count++
	if v.Until.IsZero() {
		v.Until = now.Add(15 * time.Minute)
	}
	a.attempts[ip] = v
	if len(password) > 72 || bcrypt.CompareHashAndPassword(a.hash, []byte(password)) != nil {
		return "", Session{}, 401
	}
	delete(a.attempts, ip)
	for k, s := range a.sessions {
		if !now.Before(s.Expires) {
			delete(a.sessions, k)
		}
	}
	if len(a.sessions) >= 128 {
		return "", Session{}, 429
	}
	token, e := bootstrap.Secret("")
	if e != nil {
		return "", Session{}, 500
	}
	csrf, e := bootstrap.Secret("")
	if e != nil {
		return "", Session{}, 500
	}
	s := Session{ID: token, CSRF: csrf, Expires: now.Add(a.TTL)}
	a.sessions[sha256.Sum256([]byte(token))] = s
	return token, s, 200
}
func (a *Auth) Get(r *http.Request) (Session, bool) {
	c, e := r.Cookie("cpa_session")
	if e != nil {
		return Session{}, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	key := sha256.Sum256([]byte(c.Value))
	s, ok := a.sessions[key]
	if !ok || !a.Now().Before(s.Expires) {
		delete(a.sessions, key)
		return Session{}, false
	}
	return s, true
}
func (a *Auth) Logout(r *http.Request) {
	c, e := r.Cookie("cpa_session")
	if e == nil {
		a.mu.Lock()
		delete(a.sessions, sha256.Sum256([]byte(c.Value)))
		a.mu.Unlock()
	}
}
func (a *Auth) Cookie(w http.ResponseWriter, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{Name: "cpa_session", Value: token, Path: "/", HttpOnly: true, Secure: a.Secure, SameSite: http.SameSiteStrictMode, Expires: expires})
}
func SameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	u, e := url.Parse(origin)
	return e == nil && u.Host == r.Host && (u.Scheme == "http" || u.Scheme == "https") && u.User == nil && u.Path == ""
}
func CSRF(r *http.Request, s Session) bool {
	return SameOrigin(r) && subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(s.CSRF)) == 1
}
func IP(r *http.Request) string {
	h, _, e := net.SplitHostPort(r.RemoteAddr)
	if e != nil {
		return r.RemoteAddr
	}
	return h
}
func Headers(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Frame-Options", "DENY")
}

var sensitive = regexp.MustCompile(`(?i)authorization|bearer|token|secret|password|api.?key|code[=:]|state[=:]|https?://|sk-[a-z0-9]|eyJ[a-z0-9]`)

// Suppress whole lines, including unknown token shapes, rather than attempt partial masking.
func Redact(line string) string {
	if sensitive.MatchString(line) {
		return "[sensitive log line omitted]"
	}
	if len(line) > 1000 {
		return "[long log line omitted]"
	}
	return strings.Map(func(r rune) rune {
		if r < ' ' && r != '\t' {
			return -1
		}
		return r
	}, line)
}
