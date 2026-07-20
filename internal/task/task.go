package task

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/U188/octopus/internal/utils/log"
	"github.com/U188/octopus/internal/utils/safe"
)

type taskEntry struct {
	name       string
	interval   time.Duration
	fn         func()
	runOnStart bool
	ticker     *time.Ticker
	stopCh     chan struct{}
	updateCh   chan time.Duration
	running    atomic.Bool
}

var (
	tasks   = make(map[string]*taskEntry)
	tasksMu sync.RWMutex
)

// Register 注册一个定时任务
// runOnStart: 是否在启动时立即执行一次
// interval <= 0 时注册为暂停状态，后续可通过 Update 启用。
func Register(name string, interval time.Duration, runOnStart bool, fn func()) {
	tasksMu.Lock()
	defer tasksMu.Unlock()

	if _, exists := tasks[name]; exists {
		log.Warnf("task %s already registered, skipping", name)
		return
	}

	tasks[name] = &taskEntry{
		name:       name,
		interval:   interval,
		fn:         fn,
		runOnStart: runOnStart,
		stopCh:     make(chan struct{}),
		updateCh:   make(chan time.Duration, 1),
	}
	if interval <= 0 {
		log.Debugf("task %s registered as paused: interval is 0", name)
		return
	}
	log.Debugf("task %s registered with interval %v, runOnStart: %v", name, interval, runOnStart)
}

// Update 更新任务的执行间隔
// 当 interval <= 0 时暂停任务（可再次 Update 恢复），不会删除任务。
func Update(name string, interval time.Duration) {
	tasksMu.RLock()
	entry, exists := tasks[name]
	tasksMu.RUnlock()
	if !exists {
		log.Warnf("task %s not found", name)
		return
	}

	select {
	case entry.updateCh <- interval:
		if interval > 0 {
			log.Infof("task %s interval updated to %v", name, interval)
		} else {
			log.Infof("task %s paused: interval is 0", name)
		}
	default:
		log.Warnf("task %s update channel full, skipping", name)
	}
}

// RUN 启动所有注册的任务
func RUN() {
	tasksMu.RLock()
	for _, entry := range tasks {
		safe.Go("task-loop:"+entry.name, func() {
			runTask(entry)
		})
	}
	tasksMu.RUnlock()

	// 阻塞主协程
	select {}
}

func runTask(entry *taskEntry) {
	// 根据配置决定是否在启动时立即执行；暂停状态不触发
	if entry.runOnStart && entry.interval > 0 {
		triggerTask(entry, "startup")
	}

	// interval <= 0 表示暂停：ticker 保持停止，等待 Update 恢复
	entry.ticker = time.NewTicker(time.Hour)
	if entry.interval > 0 {
		entry.ticker.Reset(entry.interval)
	} else {
		entry.ticker.Stop()
	}
	defer entry.ticker.Stop()

	for {
		select {
		case <-entry.ticker.C:
			triggerTask(entry, "ticker")
		case newInterval := <-entry.updateCh:
			entry.interval = newInterval
			if newInterval > 0 {
				entry.ticker.Reset(newInterval)
			} else {
				entry.ticker.Stop()
			}
		case <-entry.stopCh:
			return
		}
	}
}

func triggerTask(entry *taskEntry, trigger string) {
	if entry == nil {
		return
	}
	if !entry.running.CompareAndSwap(false, true) {
		log.Warnf("task %s skipped: previous run still in progress (trigger=%s)", entry.name, trigger)
		return
	}
	safe.Go("task-exec:"+entry.name+":"+trigger, func() {
		defer entry.running.Store(false)
		entry.fn()
	})
}
