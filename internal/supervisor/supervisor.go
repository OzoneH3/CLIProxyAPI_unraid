//go:build linux

package supervisor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
)

func Run(ctx context.Context, cmd *exec.Cmd, signals <-chan os.Signal, started func()) error {
	return RunManaged(ctx, func() *exec.Cmd { return cmd }, signals, nil, func(alive bool) {
		if alive {
			started()
		}
	})
}

// RunManaged owns all child waits, including orphaned grandchildren when PID 1.
// Restart requests are trusted in-process operations after atomic config writes.
func RunManaged(ctx context.Context, factory func() *exec.Cmd, signals <-chan os.Signal, restarts <-chan chan error, alive func(bool)) error {
	var cmd *exec.Cmd
	start := func() error {
		cmd = factory()
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if e := cmd.Start(); e != nil {
			return errors.New("could not start CLIProxyAPI")
		}
		alive(true)
		return nil
	}
	if e := start(); e != nil {
		return e
	}
	defer alive(false)
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	stopping := false
	var deadline time.Time
	var pending chan error
	done := ctx.Done()
	stop := func(sig syscall.Signal) {
		if !stopping {
			stopping = true
			deadline = time.Now().Add(10 * time.Second)
			_ = syscall.Kill(-cmd.Process.Pid, sig)
		}
	}
	for {
		select {
		case <-done:
			done = nil
			if pending != nil {
				pending <- errors.New("shutting down")
				pending = nil
			}
			stop(syscall.SIGTERM)
		case sig := <-signals:
			if sig == syscall.SIGHUP {
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGHUP)
			} else {
				done = nil
				if pending != nil {
					pending <- errors.New("shutting down")
					pending = nil
				}
				stop(sig.(syscall.Signal))
			}
		case ack := <-restarts:
			if stopping {
				ack <- errors.New("restart already in progress")
			} else {
				pending = ack
				stop(syscall.SIGTERM)
			}
		case <-tick.C:
			for {
				var status syscall.WaitStatus
				pid, err := syscall.Wait4(-1, &status, syscall.WNOHANG, nil)
				if err != nil || pid <= 0 {
					break
				}
				if pid != cmd.Process.Pid {
					continue
				}
				alive(false)
				_ = syscall.Kill(-pid, syscall.SIGKILL)
				_ = cmd.Process.Release()
				// Drain process-group descendants before restarting or exiting.
				until := time.Now().Add(time.Second)
				for time.Now().Before(until) {
					var st syscall.WaitStatus
					p, e := syscall.Wait4(-pid, &st, syscall.WNOHANG, nil)
					if e == syscall.ECHILD {
						break
					}
					if p == 0 {
						time.Sleep(10 * time.Millisecond)
					}
				}
				if pending != nil {
					e := start()
					pending <- e
					pending = nil
					if e != nil {
						return e
					}
					stopping = false
					break
				}
				if stopping {
					return nil
				}
				if status.Signaled() {
					return fmt.Errorf("CLIProxyAPI exited unexpectedly (signal %s)", status.Signal())
				}
				return fmt.Errorf("CLIProxyAPI exited unexpectedly (exit status %d)", status.ExitStatus())
			}
		}
		if stopping && time.Now().After(deadline) {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
	}
}
