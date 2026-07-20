package task

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func resetTasks() {
	tasksMu.Lock()
	tasks = make(map[string]*taskEntry)
	tasksMu.Unlock()
}

// 回归：任务被 Update 到 interval=0 暂停后，再次 Update 到正值应恢复执行，
// 而不是像旧实现那样被删除后永久无法恢复。
func TestUpdatePauseThenResume(t *testing.T) {
	resetTasks()
	t.Cleanup(resetTasks)

	var runs atomic.Int32
	Register("t-resume", 20*time.Millisecond, false, func() {
		runs.Add(1)
	})

	entry := tasks["t-resume"]
	stopped := make(chan struct{})
	go func() {
		runTask(entry)
		close(stopped)
	}()
	t.Cleanup(func() {
		close(entry.stopCh)
		<-stopped
	})

	// 暂停
	Update("t-resume", 0)
	time.Sleep(60 * time.Millisecond)
	runs.Store(0)
	time.Sleep(60 * time.Millisecond)
	if got := runs.Load(); got != 0 {
		t.Fatalf("paused task must not run, got %d executions", got)
	}

	// 恢复
	Update("t-resume", 20*time.Millisecond)
	time.Sleep(120 * time.Millisecond)
	if got := runs.Load(); got == 0 {
		t.Fatalf("resumed task must run again, got 0 executions")
	}

	// 任务仍在表中，未被删除
	tasksMu.RLock()
	_, exists := tasks["t-resume"]
	tasksMu.RUnlock()
	if !exists {
		t.Fatalf("task must remain registered after pause/resume")
	}
}

// 回归：以 interval=0 注册的任务应保留在表中（暂停态），后续可被 Update 启用。
func TestRegisterZeroIntervalRegistersPaused(t *testing.T) {
	resetTasks()
	t.Cleanup(resetTasks)

	Register("t-paused", 0, true, func() {})

	tasksMu.RLock()
	_, exists := tasks["t-paused"]
	tasksMu.RUnlock()
	if !exists {
		t.Fatalf("zero-interval task must be registered as paused, not dropped")
	}
}

// 暂停态注册的任务，Update 启用后应开始执行。
func TestRegisterPausedThenEnable(t *testing.T) {
	resetTasks()
	t.Cleanup(resetTasks)

	var mu sync.Mutex
	var runs int
	Register("t-enable", 0, true, func() {
		mu.Lock()
		runs++
		mu.Unlock()
	})

	entry := tasks["t-enable"]
	stopped := make(chan struct{})
	go func() {
		runTask(entry)
		close(stopped)
	}()
	t.Cleanup(func() {
		close(entry.stopCh)
		<-stopped
	})

	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	before := runs
	mu.Unlock()
	if before != 0 {
		t.Fatalf("paused task (even runOnStart) must not run before enabled, got %d", before)
	}

	Update("t-enable", 20*time.Millisecond)
	time.Sleep(120 * time.Millisecond)
	mu.Lock()
	after := runs
	mu.Unlock()
	if after == 0 {
		t.Fatalf("enabled task must run, got 0")
	}
}
