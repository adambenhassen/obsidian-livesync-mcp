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

func TestStartFailsForMissingBinary(t *testing.T) {
	d := New("definitely-not-a-real-binary-xyz", "/tmp/db", "/tmp/vault")
	if err := d.Start(context.Background()); err == nil {
		t.Fatal("expected error starting missing binary")
		_ = d.Stop()
	}
}
