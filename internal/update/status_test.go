package update

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// resetUpdateStateForTest restores the package-level update wiring after a test
// mutates the injectable indirection points.
func resetUpdateStateForTest(t *testing.T) {
	t.Helper()
	origRunner := updateRunner
	origRestart := updateRestart
	origAudit := updateAuditWriter
	origDelay := updateRestartDelay
	t.Cleanup(func() {
		updateMu.Lock()
		updateStatus = UpdateStatus{State: UpdateStateIdle}
		updateMu.Unlock()
		updateRunner = origRunner
		updateRestart = origRestart
		updateAuditWriter = origAudit
		updateRestartDelay = origDelay
	})
	updateMu.Lock()
	updateStatus = UpdateStatus{State: UpdateStateIdle}
	updateMu.Unlock()
}

func waitForState(t *testing.T, want UpdateState) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if CurrentStatus().State == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for state %q, got %q", want, CurrentStatus().State)
}

func TestStartUpdateReturnsImmediatelyAndTracksFailure(t *testing.T) {
	resetUpdateStateForTest(t)
	updateRestartDelay = 0
	release := make(chan struct{})
	updateRunner = func() (string, error) {
		<-release // hold the update "in progress"
		return "", fmt.Errorf("download failed: boom")
	}
	var auditStatus string
	var auditErrText string
	updateAuditWriter = func(_ AuditMeta, status string, err error) {
		auditStatus = status
		if err != nil {
			auditErrText = err.Error()
		}
	}
	updateRestart = func(string) { t.Error("restart must not run on failure") }

	// StartUpdate must return without blocking on the (still-held) runner.
	done := make(chan UpdateStatus, 1)
	go func() { done <- StartUpdate(AuditMeta{Actor: "tester"}) }()
	select {
	case s := <-done:
		if s.State != UpdateStateRunning {
			t.Fatalf("expected running state on start, got %q", s.State)
		}
	case <-time.After(time.Second):
		t.Fatal("StartUpdate blocked instead of returning immediately")
	}

	// A second call while running must not launch another runner.
	if s := StartUpdate(AuditMeta{Actor: "tester2"}); s.State != UpdateStateRunning {
		t.Fatalf("expected running while in progress, got %q", s.State)
	}

	close(release)
	waitForState(t, UpdateStateFailed)
	if got := CurrentStatus().Message; got != "download failed: boom" {
		t.Fatalf("expected failure message surfaced, got %q", got)
	}
	if auditStatus != "failed" || auditErrText != "download failed: boom" {
		t.Fatalf("expected failed audit, got status=%q err=%q", auditStatus, auditErrText)
	}
}

func TestStartUpdateSuccessRestarts(t *testing.T) {
	resetUpdateStateForTest(t)
	updateRestartDelay = 0
	updateRunner = func() (string, error) { return "/tmp/octopus", nil }
	var restarted sync.WaitGroup
	restarted.Add(1)
	var restartPath string
	updateRestart = func(p string) { restartPath = p; restarted.Done() }
	var auditStatus string
	updateAuditWriter = func(_ AuditMeta, status string, _ error) { auditStatus = status }

	StartUpdate(AuditMeta{Actor: "tester"})
	restarted.Wait()
	if restartPath != "/tmp/octopus" {
		t.Fatalf("expected restart with installed exec path, got %q", restartPath)
	}
	if auditStatus != "success" {
		t.Fatalf("expected success audit, got %q", auditStatus)
	}
}
