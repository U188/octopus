package tgbot

import (
	"context"
	"testing"
	"time"

	dbpkg "github.com/U188/octopus/internal/db"
	"github.com/U188/octopus/internal/model"
)

func setupReportTestDB(t *testing.T) context.Context {
	t.Helper()
	ctx := context.Background()
	dbPath := t.TempDir() + "/octopus-report-test.db"
	if err := dbpkg.InitDB("sqlite", dbPath, false); err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	return ctx
}

func TestLoadUsageExcludesSiteTestConversationAndGroupsChannelsByID(t *testing.T) {
	ctx := setupReportTestDB(t)
	start := time.Unix(1000, 0)
	rows := []model.RelayLog{
		{ID: 1, Time: start.Add(1 * time.Minute).Unix(), RequestModelName: "gpt-5.5", ChannelId: 9, ChannelName: "hlool / old", InputTokens: 10, OutputTokens: 2, Cost: 0.1, Success: true},
		{ID: 2, Time: start.Add(2 * time.Minute).Unix(), RequestModelName: "gpt-5.5", ChannelId: 9, ChannelName: "hlool", InputTokens: 20, OutputTokens: 3, Cost: 0.2, Success: true},
		{ID: 3, Time: start.Add(3 * time.Minute).Unix(), RequestModelName: "claude", ChannelId: 10, ChannelName: "other", InputTokens: 30, OutputTokens: 4, Cost: 0.3, Success: false},
		{ID: 4, Time: start.Add(4 * time.Minute).Unix(), RequestModelName: "claude", ChannelId: 10, ChannelName: "other", InputTokens: 30, OutputTokens: 4, Cost: 0.3, Success: false},
		{ID: 5, Time: start.Add(5 * time.Minute).Unix(), RequestModelName: "claude", ChannelId: 10, ChannelName: "other", InputTokens: 30, OutputTokens: 4, Cost: 0.3, Success: false},
		{ID: 6, Time: start.Add(6 * time.Minute).Unix(), RequestModelName: "test-model", ChannelName: "Site Test Conversation / hlool / key-a", InputTokens: 100, OutputTokens: 50, Cost: 9, Success: true},
		{ID: 7, Time: start.Add(7 * time.Minute).Unix(), RequestModelName: "test-model", ChannelName: "Site Test Conversation / hlool / key-b", InputTokens: 100, OutputTokens: 50, Cost: 9, Success: true},
	}
	if err := dbpkg.GetDB().WithContext(ctx).Create(&rows).Error; err != nil {
		t.Fatalf("create relay logs failed: %v", err)
	}

	usage, models, channels, err := loadUsage(ctx, reportWindow{
		Label: "test",
		Start: start,
		End:   start.Add(10 * time.Minute),
	})
	if err != nil {
		t.Fatalf("loadUsage failed: %v", err)
	}

	if usage.Requests != 5 || usage.Success != 2 || usage.Failed != 3 {
		t.Fatalf("test conversation logs should be excluded from usage, got %+v", usage)
	}
	if len(channels) != 2 {
		t.Fatalf("expected 2 channel groups, got %+v", channels)
	}
	if channels[0].Name != "hlool" || channels[0].Requests != 2 || channels[0].Success != 2 {
		t.Fatalf("expected hlool rows to be grouped by channel id and rank first by final success count, got %+v", channels)
	}
	for _, item := range models {
		if item.Name == "test-model" {
			t.Fatalf("test conversation model entered ranking: %+v", models)
		}
	}
}
