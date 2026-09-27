// Local development serves both mock upstream and the real wrapper UI on loopback.
package main

import (
	"context"
	"fmt"
	"github.com/OzoneH3/CLIProxyAPI_unraid/internal/bootstrap"
	"github.com/OzoneH3/CLIProxyAPI_unraid/internal/cliproxy"
	"github.com/OzoneH3/CLIProxyAPI_unraid/internal/mock"
	"github.com/OzoneH3/CLIProxyAPI_unraid/internal/security"
	ui "github.com/OzoneH3/CLIProxyAPI_unraid/internal/web"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, "Development server failed:", e)
		os.Exit(1)
	}
}
func run() error {
	dir, e := os.MkdirTemp("", "cpa-dev-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(dir)
	r, e := bootstrap.Init(dir, "development-only-password")
	if e != nil {
		return e
	}
	m := mock.New()
	m.Secret = r.LocalSecret
	m.Key = r.ClientKey
	m.Config = r.ConfigPath
	backend := &http.Server{Addr: "127.0.0.1:18317", Handler: m.Handler(), ReadHeaderTimeout: 5 * time.Second}
	c := cliproxy.New("http://127.0.0.1:18317", r.LocalSecret, r.ClientKey, r.ConfigPath)
	web := ui.New(c, security.New(r.PasswordHash, false))
	web.Alive.Store(true)
	web.APIPort = 18317
	frontend := &http.Server{Addr: "127.0.0.1:18318", Handler: web.Handler(), ReadHeaderTimeout: 5 * time.Second}
	errs := make(chan error, 2)
	go func() { errs <- backend.ListenAndServe() }()
	go func() { errs <- frontend.ListenAndServe() }()
	fmt.Println("Mock only: http://127.0.0.1:18318 — password: development-only-password")
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	select {
	case <-ctx.Done():
	case e = <-errs:
	}
	end, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = frontend.Shutdown(end)
	_ = backend.Shutdown(end)
	return e
}
