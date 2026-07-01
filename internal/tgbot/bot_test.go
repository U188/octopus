package tgbot

import (
	"testing"

	"github.com/U188/octopus/internal/model"
)

func TestParseAdminIDs(t *testing.T) {
	ids, err := parseAdminIDs("123, 456\n789，123")
	if err != nil {
		t.Fatalf("parseAdminIDs failed: %v", err)
	}
	for _, id := range []int64{123, 456, 789} {
		if _, ok := ids[id]; !ok {
			t.Fatalf("missing id %d in %+v", id, ids)
		}
	}
	if len(ids) != 3 {
		t.Fatalf("expected 3 unique ids, got %d", len(ids))
	}
}

func TestParseAdminIDsRejectsInvalid(t *testing.T) {
	if _, err := parseAdminIDs("123,abc"); err == nil {
		t.Fatalf("expected invalid admin id to fail")
	}
}

func TestBuildAPIURL(t *testing.T) {
	got, err := buildAPIURL("https://api.telegram.org", "123:abc", "sendMessage")
	if err != nil {
		t.Fatalf("buildAPIURL failed: %v", err)
	}
	want := "https://api.telegram.org/bot123:abc/sendMessage"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestBuildAPIURLTokenTemplate(t *testing.T) {
	got, err := buildAPIURL("https://tg.example.com/{token}", "123:abc", "/getUpdates")
	if err != nil {
		t.Fatalf("buildAPIURL failed: %v", err)
	}
	want := "https://tg.example.com/123:abc/getUpdates"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestNormalizeCommandDropsBotUsername(t *testing.T) {
	if got := normalizeCommand("/start@octopus_bot"); got != "/start" {
		t.Fatalf("got %q", got)
	}
}

func TestParsePair(t *testing.T) {
	siteID, accountID, err := parsePair("acct:12:34", "acct")
	if err != nil {
		t.Fatalf("parsePair failed: %v", err)
	}
	if siteID != 12 || accountID != 34 {
		t.Fatalf("got %d %d", siteID, accountID)
	}
}

func TestParseTriple(t *testing.T) {
	first, second, third, err := parseTriple("mg:item:prio:12:34:-1", "mg:item:prio")
	if err != nil {
		t.Fatalf("parseTriple failed: %v", err)
	}
	if first != 12 || second != 34 || third != -1 {
		t.Fatalf("got %d %d %d", first, second, third)
	}
}

func TestParseGroupTarget(t *testing.T) {
	siteID, accountID, groupKey, err := parseGroupTarget("group:12:34:vip", "group")
	if err != nil {
		t.Fatalf("parseGroupTarget failed: %v", err)
	}
	if siteID != 12 || accountID != 34 || groupKey != "vip" {
		t.Fatalf("got %d %d %q", siteID, accountID, groupKey)
	}
}

func TestParseModelTarget(t *testing.T) {
	siteID, accountID, groupKey, modelName, err := parseModelTarget("model:view:12:34:vip:gpt-5.5", "model:view")
	if err != nil {
		t.Fatalf("parseModelTarget failed: %v", err)
	}
	if siteID != 12 || accountID != 34 || groupKey != "vip" || modelName != "gpt-5.5" {
		t.Fatalf("got %d %d %q %q", siteID, accountID, groupKey, modelName)
	}
}

func TestParseRouteTarget(t *testing.T) {
	siteID, accountID, groupKey, routeType, err := parseRouteTarget("model:addroute:12:34:vip:anthropic", "model:addroute")
	if err != nil {
		t.Fatalf("parseRouteTarget failed: %v", err)
	}
	if siteID != 12 || accountID != 34 || groupKey != "vip" || routeType != model.SiteModelRouteTypeAnthropic {
		t.Fatalf("got %d %d %q %q", siteID, accountID, groupKey, routeType)
	}
}

func TestParseModelGroupModeTarget(t *testing.T) {
	groupID, mode, err := parseModelGroupModeTarget("mg:mode:set:12:4", "mg:mode:set")
	if err != nil {
		t.Fatalf("parseModelGroupModeTarget failed: %v", err)
	}
	if groupID != 12 || mode != model.GroupModeWeighted {
		t.Fatalf("got %d %v", groupID, mode)
	}
}

func TestParseModelGroupModelTarget(t *testing.T) {
	groupID, channelID, modelName, err := parseModelGroupModelTarget("mg:addmodel:12:34:claude:sonnet", "mg:addmodel")
	if err != nil {
		t.Fatalf("parseModelGroupModelTarget failed: %v", err)
	}
	if groupID != 12 || channelID != 34 || modelName != "claude:sonnet" {
		t.Fatalf("got %d %d %q", groupID, channelID, modelName)
	}
}

func TestParseModelGroupMode(t *testing.T) {
	mode, ok := parseModelGroupMode("failover")
	if !ok || mode != model.GroupModeFailover {
		t.Fatalf("got %v %t", mode, ok)
	}
}

func TestBuildInlineKeyboardSkipsEmptyItems(t *testing.T) {
	keyboard := buildInlineKeyboard([][]inlineButton{
		{{Text: "A", Data: "a"}, {Text: "", Data: "b"}},
		{},
	})
	rows, ok := keyboard["inline_keyboard"].([][]map[string]string)
	if !ok {
		t.Fatalf("unexpected keyboard: %#v", keyboard)
	}
	if len(rows) != 1 || len(rows[0]) != 1 || rows[0][0]["callback_data"] != "a" {
		t.Fatalf("unexpected rows: %#v", rows)
	}
}

func TestParseNonEmptyLines(t *testing.T) {
	lines := parseNonEmptyLines("gpt-4o\n\nclaude-sonnet anthropic\r\n  ds-chat  ")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}
	if lines[0] != "gpt-4o" || lines[1] != "claude-sonnet anthropic" || lines[2] != "ds-chat" {
		t.Fatalf("unexpected lines: %#v", lines)
	}
}
