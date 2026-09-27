package security

import (
	"golang.org/x/crypto/bcrypt"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAuthLifecycle(t *testing.T) {
	h, _ := bcrypt.GenerateFromPassword([]byte("correct-password"), bcrypt.MinCost)
	a := New(h, false)
	now := time.Now()
	a.Now = func() time.Time { return now }
	for i := 0; i < 5; i++ {
		_, _, status := a.Login("bad", "wrong")
		if status != 401 {
			t.Fatal(status)
		}
	}
	if _, _, status := a.Login("bad", "correct-password"); status != 429 {
		t.Fatal("no rate limit")
	}
	token, s, status := a.Login("good", "correct-password")
	if status != 200 {
		t.Fatal(status)
	}
	r := httptest.NewRequest("POST", "http://localhost/api/key/rotate", nil)
	w := httptest.NewRecorder()
	a.Cookie(w, token, s.Expires)
	cookie := w.Result().Cookies()[0]
	if !cookie.HttpOnly || cookie.SameSite == 0 {
		t.Fatal("cookie flags")
	}
	r.AddCookie(cookie)
	if _, ok := a.Get(r); !ok {
		t.Fatal("session missing")
	}
	if CSRF(r, s) {
		t.Fatal("missing CSRF accepted")
	}
	r.Header.Set("Origin", "http://localhost")
	r.Header.Set("X-CSRF-Token", s.CSRF)
	if !CSRF(r, s) {
		t.Fatal("valid CSRF rejected")
	}
	r.Header.Set("Origin", "http://evil.example")
	if CSRF(r, s) {
		t.Fatal("cross origin")
	}
	now = now.Add(9 * time.Hour)
	if _, ok := a.Get(r); ok {
		t.Fatal("expired session")
	}
	if _, _, status := a.Login("bad", "correct-password"); status != 200 {
		t.Fatal("limit did not expire")
	}
}
func TestSecureCookie(t *testing.T) {
	a := New(nil, true)
	w := httptest.NewRecorder()
	a.Cookie(w, "token", time.Now())
	if !w.Result().Cookies()[0].Secure {
		t.Fatal("missing secure flag")
	}
}
func TestRedact(t *testing.T) {
	for _, line := range []string{"Authorization: Bearer secret", "refresh_token=secret", "http://localhost?code=secret", "password secret", "sk-cpa-secret"} {
		if Redact(line) == line {
			t.Fatal("leaked secret")
		}
	}
}
