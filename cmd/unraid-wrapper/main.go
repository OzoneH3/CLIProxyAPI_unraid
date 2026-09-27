package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/OzoneH3/CLIProxyAPI_unraid/internal/bootstrap"
	"github.com/OzoneH3/CLIProxyAPI_unraid/internal/cliproxy"
	"github.com/OzoneH3/CLIProxyAPI_unraid/internal/security"
	"github.com/OzoneH3/CLIProxyAPI_unraid/internal/supervisor"
	ui "github.com/OzoneH3/CLIProxyAPI_unraid/internal/web"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "healthcheck" {
		c := http.Client{Timeout: 4 * time.Second}
		r, e := c.Get("http://127.0.0.1:8318/healthz")
		if e != nil {
			os.Exit(1)
		}
		r.Body.Close()
		if r.StatusCode != 200 {
			os.Exit(1)
		}
		return
	}
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e.Error())
		os.Exit(1)
	}
}
func run() error {
	syscall.Umask(0077)
	dir := os.Getenv("DATA_DIR")
	if dir == "" {
		dir = "/data"
	}
	var e error
	dir, e = filepath.Abs(dir)
	if e != nil {
		return e
	}
	if e = bootstrap.PrivateDir(dir); e != nil {
		return fmt.Errorf("cannot prepare private appdata")
	}
	// Refuse concurrent wrappers on the same storage. Lock is held until process exit.
	lock, e := os.OpenFile(filepath.Join(dir, ".wrapper.lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return fmt.Errorf("cannot lock appdata")
	}
	defer lock.Close()
	if e = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		return fmt.Errorf("appdata is already in use")
	}
	result, e := bootstrap.Init(dir, os.Getenv("WEBUI_PASSWORD"))
	_ = os.Unsetenv("WEBUI_PASSWORD")
	if e != nil {
		return e
	}
	port, e := ui.ParsePort(os.Getenv("API_PUBLIC_PORT"))
	if e != nil {
		return fmt.Errorf("invalid API_PUBLIC_PORT")
	}
	client := cliproxy.New("http://127.0.0.1:8317", result.LocalSecret, result.ClientKey, result.ConfigPath)
	server := ui.New(client, security.New(result.PasswordHash, os.Getenv("WEBUI_SECURE_COOKIE") == "true"))
	server.APIPort = port
	httpServer := &http.Server{Addr: ":8318", Handler: server.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second, WriteTimeout: 45 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	signals := make(chan os.Signal, 8)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(signals)
	webErrors := make(chan error, 1)

	restarts := make(chan chan error)
	client.Reload = func(ctx context.Context) error {
		ack := make(chan error, 1)
		select {
		case restarts <- ack:
		case <-ctx.Done():
			return ctx.Err()
		}
		select {
		case e := <-ack:
			return e
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	factory := func() *exec.Cmd {
		cmd := exec.Command("/CLIProxyAPI/CLIProxyAPI", "--config", result.ConfigPath, "--password", result.LocalSecret)
		cmd.Dir = dir
		// Preserve actionable child diagnostics without allowing OAuth values or
		// credentials into Docker logs.
		cmd.Stdout = security.NewRedactingWriter(os.Stdout)
		cmd.Stderr = security.NewRedactingWriter(os.Stderr)
		for _, entry := range os.Environ() {
			key := strings.SplitN(entry, "=", 2)[0]
			switch key {
			case "PATH", "TZ", "SSL_CERT_FILE", "SSL_CERT_DIR":
				cmd.Env = append(cmd.Env, entry)
			}
		}
		cmd.Env = append(cmd.Env, "HOME=/root")
		return cmd
	}
	go func() {
		e := httpServer.ListenAndServe()
		if e != nil && e != http.ErrServerClosed {
			webErrors <- fmt.Errorf("WebUI listener failed")
			cancel()
		}
	}()
	// Upstream enables a 10-second /keep-alive watchdog whenever --password is
	// used. Retry through the fixed loopback endpoint while the child is alive.
	go func() {
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for {
			ping, done := context.WithTimeout(ctx, 2*time.Second)
			_ = client.KeepAlive(ping)
			done()
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	fmt.Println("Starting CLIProxyAPI and WebUI")
	e = supervisor.RunManaged(ctx, factory, signals, restarts, func(alive bool) { server.Alive.Store(alive) })
	server.Alive.Store(false)
	shut, done := context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	_ = httpServer.Shutdown(shut)
	select {
	case we := <-webErrors:
		return we
	default:
	}
	return e
}
