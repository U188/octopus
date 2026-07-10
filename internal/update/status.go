package update

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/U188/octopus/internal/model"
	"github.com/U188/octopus/internal/op"
	"github.com/U188/octopus/internal/utils/log"
)

// UpdateState is the lifecycle of an asynchronous self-update.
type UpdateState string

const (
	UpdateStateIdle    UpdateState = "idle"
	UpdateStateRunning UpdateState = "running"
	UpdateStateSuccess UpdateState = "success"
	UpdateStateFailed  UpdateState = "failed"
)

// UpdateStatus is a snapshot of the current self-update, polled by the UI.
type UpdateStatus struct {
	State     UpdateState `json:"state"`
	Message   string      `json:"message,omitempty"`
	StartedAt int64       `json:"started_at,omitempty"`
	UpdatedAt int64       `json:"updated_at,omitempty"`
}

// AuditMeta carries request-scoped audit attribution across the async boundary,
// so the final outcome can be recorded even though the update completes after
// the HTTP request that triggered it has already returned.
type AuditMeta struct {
	Actor       string
	IP          string
	UserAgent   string
	Method      string
	Path        string
	FromVersion string
	Commit      string
}

var (
	updateMu     sync.Mutex
	updateStatus = UpdateStatus{State: UpdateStateIdle}

	// Indirection points so the async state machine can be exercised in tests
	// without downloading a release, replacing the binary, or restarting the
	// test process. Production wiring is the package's real implementations.
	updateRunner      = runUpdateCore
	updateRestart     = restartExecutable
	updateAuditWriter = writeUpdateAudit
)

// StartUpdate begins an asynchronous self-update unless one is already running,
// returning the current status immediately. The long download/verify/install no
// longer blocks the HTTP request, which was the root cause of the browser
// reporting "update failed" (client/proxy timeout) even though the server
// updated successfully. On success the process restarts shortly after; on
// failure the status records the error for the UI to surface via polling.
func StartUpdate(meta AuditMeta) UpdateStatus {
	updateMu.Lock()
	if updateStatus.State == UpdateStateRunning {
		running := updateStatus
		updateMu.Unlock()
		return running
	}
	updateStatus = UpdateStatus{State: UpdateStateRunning, StartedAt: time.Now().Unix()}
	started := updateStatus
	updateMu.Unlock()

	go runAsyncUpdate(meta)
	return started
}

// CurrentStatus returns a snapshot of the current self-update status.
func CurrentStatus() UpdateStatus {
	updateMu.Lock()
	defer updateMu.Unlock()
	return updateStatus
}

func setUpdateStatus(state UpdateState, message string) {
	updateMu.Lock()
	updateStatus = UpdateStatus{
		State:     state,
		Message:   message,
		StartedAt: updateStatus.StartedAt,
		UpdatedAt: time.Now().Unix(),
	}
	updateMu.Unlock()
}

func runAsyncUpdate(meta AuditMeta) {
	execPath, err := updateRunner()
	if err != nil {
		setUpdateStatus(UpdateStateFailed, err.Error())
		updateAuditWriter(meta, op.AuditStatusFailed, err)
		log.Warnf("async update failed: %v", err)
		return
	}
	setUpdateStatus(UpdateStateSuccess, "")
	updateAuditWriter(meta, op.AuditStatusSuccess, nil)
	log.Infof("update core success; restarting in %s", updateRestartDelay)
	time.Sleep(updateRestartDelay)
	updateRestart(execPath)
}

func writeUpdateAudit(meta AuditMeta, status string, auditErr error) {
	detail := ""
	if data, err := json.Marshal(map[string]any{"version": meta.FromVersion, "commit": meta.Commit}); err == nil {
		detail = string(data)
	}
	errText := ""
	if auditErr != nil {
		errText = auditErr.Error()
	}
	entry := &model.AuditLog{
		Action:    "system.update",
		Status:    status,
		Actor:     meta.Actor,
		IP:        meta.IP,
		UserAgent: meta.UserAgent,
		Method:    meta.Method,
		Path:      meta.Path,
		Detail:    detail,
		Error:     errText,
	}
	if err := op.AuditCreate(context.Background(), entry); err != nil {
		log.Warnf("failed to write update audit log: %v", err)
	}
}
