package daemon

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"sync"
)

// Daemon supervises a `livesync-cli <db> daemon --vault <vault>` subprocess.
type Daemon struct {
	bin   string
	args  []string // command arguments (default: CLI daemon invocation)
	cmd   *exec.Cmd
	mu    sync.Mutex
	alive bool
	done  chan struct{} // closed when the watcher observes process exit
}

// New returns a Daemon configured to run the LiveSync CLI daemon.
func New(cliPath, dbDir, vaultDir string) *Daemon {
	return &Daemon{
		bin:  cliPath,
		args: []string{dbDir, "daemon", "--vault", vaultDir},
	}
}

// Start launches the subprocess and begins watching it. Start returns an error
// if the process cannot be spawned. When the process exits on its own, the
// daemon is marked unhealthy.
func (d *Daemon) Start(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, d.bin, d.args...)
	cmd.Stdout = os.Stderr // CLI logs → our stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan struct{})
	d.mu.Lock()
	d.cmd = cmd
	d.alive = true
	d.done = done
	d.mu.Unlock()

	go func() {
		_ = cmd.Wait()
		d.mu.Lock()
		d.alive = false
		d.mu.Unlock()
		close(done)
	}()
	return nil
}

// Healthy reports whether the supervised process is currently running.
func (d *Daemon) Healthy() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.alive
}

// Stop terminates the supervised process and waits for the watcher to mark it
// stopped, so Healthy() reliably reports false once Stop returns.
func (d *Daemon) Stop() error {
	d.mu.Lock()
	cmd := d.cmd
	done := d.done
	d.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	// On graceful shutdown the process may already be reaped (e.g. via a
	// cancelled CommandContext); treat that as success and still wait for the
	// watcher so Healthy() reliably reports false once Stop returns.
	if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	<-done
	return nil
}
