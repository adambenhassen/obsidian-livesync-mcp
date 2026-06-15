package daemon

import (
	"context"
	"errors"
	"log"
	"os"
	"os/exec"
	"strconv"
	"sync"
)

// Daemon supervises a `livesync-cli <db> daemon --vault <vault>` subprocess.
type Daemon struct {
	bin      string
	args     []string // command arguments (default: CLI daemon invocation)
	cmd      *exec.Cmd
	mu       sync.Mutex
	alive    bool
	stopping bool          // set by Stop so an intentional kill isn't logged as a crash
	done     chan struct{} // closed when the watcher observes process exit
}

// New returns a Daemon configured to run the LiveSync CLI daemon. When interval
// is > 0, the daemon polls CouchDB every interval seconds (`--interval`), which
// is the reliable way to drive bidirectional sync: the CLI resets the liveSync
// settings flag during its startup migration, but the CLI flag is honoured
// regardless. interval <= 0 omits the flag (continuous mode per settings).
func New(cliPath, dbDir, vaultDir string, interval int) *Daemon {
	args := []string{dbDir, "daemon", "--vault", vaultDir}
	if interval > 0 {
		args = append(args, "--interval", strconv.Itoa(interval))
	}
	return &Daemon{
		bin:  cliPath,
		args: args,
	}
}

// Start launches the subprocess and begins watching it. Start returns an error
// if the process cannot be spawned. When the process exits on its own, the
// daemon is marked unhealthy.
func (d *Daemon) Start(ctx context.Context) error {
	// bin and args originate from operator configuration (env), not request
	// input; supervising the configured CLI is the daemon's whole purpose.
	cmd := exec.CommandContext(ctx, d.bin, d.args...) //nolint:gosec // G204: configured CLI, not user input
	cmd.Stdout = os.Stderr                            // CLI logs → our stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan struct{})
	d.mu.Lock()
	d.cmd = cmd
	d.alive = true
	d.stopping = false
	d.done = done
	d.mu.Unlock()

	go func() {
		waitErr := cmd.Wait()
		d.mu.Lock()
		d.alive = false
		stopping := d.stopping
		d.mu.Unlock()
		if waitErr != nil && !stopping {
			log.Printf("livesync-cli daemon exited unexpectedly: %v", waitErr)
		}
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
	d.stopping = true
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
