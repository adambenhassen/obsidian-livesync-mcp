package daemon

import (
	"context"
	"errors"
	"log"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"time"
)

// stopGrace bounds how long Stop (and a cancelled context) waits for the CLI to
// exit after SIGTERM before escalating to SIGKILL. The CLI restores its settings
// file during a graceful (SIGTERM/SIGINT) shutdown; a hard kill skips that.
const stopGrace = 5 * time.Second

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
// is the reliable way to drive bidirectional sync: the current CLI resets the
// liveSync settings flag during its startup migration (observed behaviour), but
// the CLI flag is honoured regardless. interval <= 0 omits the flag, leaving
// replication to the settings file — which, given that migration, does not
// reliably sync; set a positive interval for actual syncing.
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
	// On context cancellation, terminate the CLI gracefully (SIGTERM) instead of
	// the os/exec default SIGKILL, so it can restore its settings file on exit;
	// WaitDelay escalates to SIGKILL if it doesn't exit within stopGrace.
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = stopGrace
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
		// Log every unexpected exit, including a clean (exit 0) self-termination
		// — otherwise a dead daemon shows only as /healthz 503 with no reason.
		if !stopping {
			log.Printf("livesync-cli daemon exited unexpectedly (waitErr=%v)", waitErr)
		}
		close(done)
	}()
	return nil
}

// Done returns a channel that is closed when the supervised process exits (for
// any reason, including an intentional Stop). It is valid only after Start.
// Callers can select on it to react to an unexpected daemon death.
func (d *Daemon) Done() <-chan struct{} {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.done
}

// Healthy reports whether the supervised process is currently running.
func (d *Daemon) Healthy() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.alive
}

// Stop terminates the supervised process and waits for the watcher to mark it
// stopped, so Healthy() reliably reports false once Stop returns. It first asks
// the CLI to exit gracefully (SIGTERM) so it can restore its settings file, then
// escalates to SIGKILL if the process does not exit within stopGrace.
func (d *Daemon) Stop() error {
	d.mu.Lock()
	cmd := d.cmd
	done := d.done
	d.stopping = true
	d.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	// The process may already be reaped (natural exit, or a cancelled
	// CommandContext that already fired cmd.Cancel); a signal then returns
	// ErrProcessDone — treat that as success and just wait for the watcher.
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		if errors.Is(err, os.ErrProcessDone) {
			<-done
			return nil
		}
		return err
	}
	select {
	case <-done:
		return nil
	case <-time.After(stopGrace):
	}
	if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	<-done
	return nil
}
