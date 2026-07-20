package op

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/U188/octopus/internal/conf"
	"github.com/U188/octopus/internal/utils/log"
)

// cacheInitialized distinguishes the first startup load (when write-behind
// caches are still empty) from a runtime refresh. Runtime refreshes must flush
// pending values before channelRefreshCache replaces and clears those caches.
var cacheInitialized atomic.Bool

func InitCache() error {
	ctx, cancel := context.WithTimeout(context.Background(), conf.CacheInitTimeout())
	defer cancel()
	if cacheInitialized.Load() {
		if err := SaveCache(); err != nil {
			log.Warnw("save cache before runtime refresh failed; in-memory runtime state may be overwritten",
				"operation", "init_cache",
				"error", err,
			)
		}
	}
	if err := settingRefreshCache(ctx); err != nil {
		return fmt.Errorf("setting refresh cache error: %v", err)
	}
	if err := channelRefreshCache(ctx); err != nil {
		return fmt.Errorf("channel refresh cache error: %v", err)
	}
	if err := proxyConfigurationRefreshCache(ctx); err != nil {
		return fmt.Errorf("proxy configuration refresh cache error: %v", err)
	}
	if err := groupRefreshCache(ctx); err != nil {
		return fmt.Errorf("group refresh cache error: %v", err)
	}
	if err := apiKeyRefreshCache(ctx); err != nil {
		return fmt.Errorf("api key refresh cache error: %v", err)
	}
	if err := llmRefreshCache(ctx); err != nil {
		return fmt.Errorf("llm refresh cache error: %v", err)
	}
	if err := statsRefreshCache(ctx); err != nil {
		return fmt.Errorf("stats refresh cache error: %v", err)
	}
	cacheInitialized.Store(true)
	return nil
}

func SaveCache() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// 三类数据独立落库：一处失败不应连带丢掉其余两类（尤其在关机钩子里），最后汇总错误。
	return errors.Join(
		StatsSaveDB(ctx),
		ChannelKeySaveDB(ctx),
		RelayLogSaveDBTask(ctx),
	)
}
