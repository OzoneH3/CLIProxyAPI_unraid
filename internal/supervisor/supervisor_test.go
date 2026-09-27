//go:build linux

package supervisor

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestSupervisorTermination(t *testing.T) {
	signals := make(chan os.Signal, 1)
	started := make(chan struct{})
	done := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() { done <- Run(ctx, exec.Command("/bin/sleep", "60"), signals, func() { close(started) }) }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("not started")
	}
	signals <- syscall.SIGTERM
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-ctx.Done():
		t.Fatal("not reaped")
	}
}
func TestCrash(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if e := Run(ctx, exec.Command("/bin/sh", "-c", "exit 17"), make(chan os.Signal), func() {}); e == nil || !strings.Contains(e.Error(), "exit status 17") {
		t.Fatalf("unexpected crash result: %v", e)
	}
}

func TestManagedRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sig := make(chan os.Signal, 1)
	restarts := make(chan chan error)
	events := make(chan bool, 10)
	done := make(chan error, 1)
	go func() {
		done <- RunManaged(ctx, func() *exec.Cmd { return exec.Command("/bin/sleep", "60") }, sig, restarts, func(alive bool) { events <- alive })
	}()
	if !<-events {
		t.Fatal("did not start")
	}
	ack := make(chan error, 1)
	restarts <- ack
	if e := <-ack; e != nil {
		t.Fatal(e)
	}
	if <-events {
		t.Fatal("missing stop event")
	}
	if !<-events {
		t.Fatal("missing restart event")
	}
	sig <- syscall.SIGINT
	if e := <-done; e != nil {
		t.Fatal(e)
	}
}
