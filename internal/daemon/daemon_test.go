package daemon

import (
	"testing"
	"time"
)

func TestNewArgsWithoutInterval(t *testing.T) {
	d := New("livesync-cli", "/db", "/vault", 0)
	want := []string{"/db", "daemon", "--vault", "/vault"}
	if !equalArgs(d.args, want) {
		t.Errorf("args = %v, want %v", d.args, want)
	}
}

func TestNewArgsWithInterval(t *testing.T) {
	d := New("livesync-cli", "/db", "/vault", 5)
	want := []string{"/db", "daemon", "--vault", "/vault", "--interval", "5"}
	if !equalArgs(d.args, want) {
		t.Errorf("args = %v, want %v", d.args, want)
	}
}

func equalArgs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestStartRunsCommandAndReportsHealthy(t *testing.T) {
	// Use a long-lived fake command instead of the real CLI.
	d := New("sleep", "/tmp/db", "/tmp/vault", 0)
	d.args = []string{"5"} // override: `sleep 5` instead of CLI args

	if err := d.Start(t.Context()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	// Give it a moment to be marked running.
	time.Sleep(50 * time.Millisecond)
	if !d.Healthy() {
		t.Fatal("daemon should be healthy while command runs")
	}
	if err := d.Stop(); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if d.Healthy() {
		t.Fatal("daemon should not be healthy after Stop()")
	}
}

func TestStopAfterProcessAlreadyExited(t *testing.T) {
	// Process exits on its own; Stop() must still return nil (treating the
	// "already finished" Kill error as non-fatal) and not hang on <-done.
	d := New("true", "/tmp/db", "/tmp/vault", 0)
	d.args = []string{}

	if err := d.Start(t.Context()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	// Wait for the process to exit and the watcher to mark it unhealthy.
	deadline := time.Now().Add(2 * time.Second)
	for d.Healthy() {
		if time.Now().After(deadline) {
			t.Fatal("process did not exit in time")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := d.Stop(); err != nil {
		t.Fatalf("Stop() after natural exit = %v, want nil", err)
	}
}

func TestDoneClosesWhenProcessExits(t *testing.T) {
	// A short-lived process: Done() must close once it exits on its own.
	d := New("true", "/tmp/db", "/tmp/vault", 0)
	d.args = []string{}
	if err := d.Start(t.Context()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	select {
	case <-d.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("Done() did not close after the process exited")
	}
	if d.Healthy() {
		t.Fatal("daemon should not be healthy after its process exited")
	}
}

func TestStartFailsForMissingBinary(t *testing.T) {
	d := New("definitely-not-a-real-binary-xyz", "/tmp/db", "/tmp/vault", 0)
	if err := d.Start(t.Context()); err == nil {
		t.Fatal("expected error starting missing binary")
	}
}
