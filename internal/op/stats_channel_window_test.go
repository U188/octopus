package op

import (
	"testing"
	"time"

	"github.com/U188/octopus/internal/db"
	"github.com/U188/octopus/internal/model"
)

func TestStatsChannel24hGetAggregatesTokenAndCacheUsage(t *testing.T) {
	ctx := setupSiteOpTestDB(t)
	if err := settingRefreshCache(ctx); err != nil {
		t.Fatalf("settingRefreshCache failed: %v", err)
	}
	resetRelayLogStateForTest()

	now := time.Unix(1_700_000_000, 0)
	reported := model.TokenCountSourceReported
	estimated := model.TokenCountSourceEstimated
	legacy := model.TokenCountSourceLegacy
	readZero := 0
	read500 := 500
	write100 := 100
	writeZero := 0
	bill400 := 400
	bill500 := 500
	bill200 := 200
	legacyCacheRead := 700
	rows := []model.RelayLog{
		{ID: 1, Time: now.Unix() - 60, ChannelId: 7, InputTokens: 1000, InputTokenSource: reported, OutputTokens: 100, BillInputTokens: &bill400, CacheReadTokens: &read500, CacheWriteTokens: &write100, Cost: 1, UseTime: 1000, Ftut: 200, Success: true},
		{ID: 2, Time: now.Unix() - 120, ChannelId: 7, InputTokens: 500, InputTokenSource: reported, OutputTokens: 50, BillInputTokens: &bill500, CacheReadTokens: &readZero, CacheWriteTokens: &writeZero, Cost: 2, UseTime: 3000, Ftut: 0, Success: false},
		{ID: 3, Time: now.Unix() - 180, ChannelId: 7, InputTokens: 200, InputTokenSource: estimated, OutputTokens: 20, BillInputTokens: &bill200, Cost: 0.5, UseTime: 2000, Ftut: 500, Success: true},
		{ID: 6, Time: now.Unix() - 240, ChannelId: 7, InputTokens: 100, InputTokenSource: legacy, CacheReadTokens: &legacyCacheRead, Success: true},
		{ID: 4, Time: now.Unix() - 60, ChannelId: 8, InputTokens: 999, InputTokenSource: reported, OutputTokens: 999, Success: true},
		{ID: 5, Time: now.Unix() - int64(25*time.Hour/time.Second), ChannelId: 7, InputTokens: 999, InputTokenSource: reported, OutputTokens: 999, Success: true},
	}
	if err := db.GetDB().WithContext(ctx).Create(&rows).Error; err != nil {
		t.Fatalf("create relay logs failed: %v", err)
	}

	stats, err := StatsChannel24hGet(ctx, 7, now)
	if err != nil {
		t.Fatalf("StatsChannel24hGet failed: %v", err)
	}
	if stats.RequestTotal != 4 || stats.RequestSuccess != 3 || stats.RequestFailed != 1 {
		t.Fatalf("unexpected request totals: %+v", stats)
	}
	if stats.InputToken != 1800 || stats.OutputToken != 170 || stats.BillInputToken != 1100 || stats.CacheReadToken != 500 || stats.CacheWriteToken != 100 {
		t.Fatalf("unexpected token totals: %+v", stats)
	}
	if stats.TotalCost != 3.5 || stats.AverageLatencyMs != 1500 || stats.AverageFTUTMs != 350 {
		t.Fatalf("unexpected cost/latency totals: %+v", stats)
	}
	if stats.SuccessRate == nil || *stats.SuccessRate != 3.0/4.0 {
		t.Fatalf("unexpected success rate: %v", stats.SuccessRate)
	}
	if stats.CacheReadRatio == nil || *stats.CacheReadRatio != 1.0/3.0 {
		t.Fatalf("unexpected cache read ratio: %v", stats.CacheReadRatio)
	}
	if stats.CacheReadRequestRate == nil || *stats.CacheReadRequestRate != 0.5 || stats.UsageCoverage == nil || *stats.UsageCoverage != 0.5 {
		t.Fatalf("unexpected cache/coverage ratios: cache=%v coverage=%v", stats.CacheReadRequestRate, stats.UsageCoverage)
	}
	if !stats.RelayLogRetentionEnabled || stats.RelayLogDroppedTotal != 0 {
		t.Fatalf("unexpected relay log completeness metadata: retention=%t dropped=%d", stats.RelayLogRetentionEnabled, stats.RelayLogDroppedTotal)
	}
}

func TestStatsChannel24hGetUsesHalfOpenWindowAndNullRatios(t *testing.T) {
	ctx := setupSiteOpTestDB(t)
	if err := settingRefreshCache(ctx); err != nil {
		t.Fatalf("settingRefreshCache failed: %v", err)
	}
	resetRelayLogStateForTest()

	now := time.Unix(1_700_000_000, 0)
	reported := model.TokenCountSourceReported
	rows := []model.RelayLog{
		{ID: 11, Time: now.Unix() - int64(24*time.Hour/time.Second), ChannelId: 9, InputTokens: 10, InputTokenSource: reported, Success: true},
		{ID: 12, Time: now.Unix(), ChannelId: 9, InputTokens: 20, InputTokenSource: reported, Success: true},
	}
	if err := db.GetDB().WithContext(ctx).Create(&rows).Error; err != nil {
		t.Fatalf("create relay logs failed: %v", err)
	}

	stats, err := StatsChannel24hGet(ctx, 9, now)
	if err != nil {
		t.Fatalf("StatsChannel24hGet failed: %v", err)
	}
	if stats.RequestTotal != 1 || stats.InputToken != 10 {
		t.Fatalf("expected half-open window to include only the lower boundary, got %+v", stats)
	}
	if stats.CacheReadRatio != nil || stats.CacheReadRequestRate != nil {
		t.Fatalf("expected cache ratios to be null without cache reads, got token=%v request=%v", stats.CacheReadRatio, stats.CacheReadRequestRate)
	}

	empty, err := StatsChannel24hGet(ctx, 999, now)
	if err != nil {
		t.Fatalf("empty StatsChannel24hGet failed: %v", err)
	}
	if empty.SuccessRate != nil || empty.UsageCoverage != nil {
		t.Fatalf("expected null ratios for an empty window, got success=%v coverage=%v", empty.SuccessRate, empty.UsageCoverage)
	}
}
