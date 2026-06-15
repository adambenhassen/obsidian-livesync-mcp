package daemon

import (
	"context"
	"testing"
	"time"
)

func TestStartRunsCommandAndReportsHealthy(t *testing.T) {
	// Use a long-lived fake command instead of the real CLI.
	d := New("sleep", "/tmp/db", "/tmp/vault")
	d.args = []string{"5"} // override: `sleep 5` instead of CLI args

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := d.Start(ctx); err != nil {
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
	d := New("true", "/tmp/db", "/tmp/vault")
	d.args = []string{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := d.Start(ctx); err != nil {
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

func TestStartFailsForMissingBinary(t *testing.T) {
	d := New("definitely-not-a-real-binary-xyz", "/tmp/db", "/tmp/vault")
	if err := d.Start(context.Background()); err == nil {
		t.Fatal("expected error starting missing binary")
		_ = d.Stop()
	}
}
