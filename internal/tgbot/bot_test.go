package tgbot

import (
	"strconv"
	"strings"
	"testing"
	"time"

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

func TestParsePageCallback(t *testing.T) {
	page, err := parsePageCallback("group_mgmt:p:3", "group_mgmt:p")
	if err != nil {
		t.Fatalf("parsePageCallback failed: %v", err)
	}
	if page != 3 {
		t.Fatalf("got %d", page)
	}
}

func TestParseGroupPageTargetKeepsColonGroupKey(t *testing.T) {
	siteID, accountID, groupKey, page, err := parseGroupPageTarget("model:listp:12:34:vip:cn:5", "model:listp")
	if err != nil {
		t.Fatalf("parseGroupPageTarget failed: %v", err)
	}
	if siteID != 12 || accountID != 34 || groupKey != "vip:cn" || page != 5 {
		t.Fatalf("got %d %d %q %d", siteID, accountID, groupKey, page)
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

func TestPaginateItemsClampsPage(t *testing.T) {
	items := []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20}
	visible, window := paginateItems(items, 99)
	if window.Page != 3 || window.TotalPages != 3 || window.Start != 16 || window.End != 20 {
		t.Fatalf("unexpected window: %+v", window)
	}
	if len(visible) != 4 || visible[0] != 17 || visible[3] != 20 {
		t.Fatalf("unexpected visible items: %#v", visible)
	}
}

func TestAppendPaginationButtons(t *testing.T) {
	window := newPageWindow(32, 2, 8)
	buttons := appendPaginationButtons(nil, window, func(page int) string {
		return "page:" + strconv.Itoa(page)
	})
	if len(buttons) != 1 {
		t.Fatalf("expected one pagination row, got %#v", buttons)
	}
	row := buttons[0]
	if len(row) != 5 {
		t.Fatalf("expected 5 buttons, got %#v", row)
	}
	if row[0].Data != "page:1" || row[1].Data != "page:1" || row[2].Text != "2/4" || row[3].Data != "page:3" || row[4].Data != "page:4" {
		t.Fatalf("unexpected row: %#v", row)
	}
}

func TestPrepareResponseAliasesLongCallbackData(t *testing.T) {
	r := &Runner{}
	longData := "model:view:1:2:vip:" + strings.Repeat("x", 80)
	resp := r.prepareResponse(response{
		Text: "models",
		Buttons: [][]inlineButton{{
			{Text: "Long", Data: longData},
			{Text: "Short", Data: "home"},
		}},
	})
	if len(resp.Buttons) != 1 || len(resp.Buttons[0]) != 2 {
		t.Fatalf("unexpected buttons: %#v", resp.Buttons)
	}
	alias := resp.Buttons[0][0].Data
	if alias == longData {
		t.Fatalf("expected long callback data to be aliased")
	}
	if len(alias) > maxCallbackDataBytes {
		t.Fatalf("alias too long: %q", alias)
	}
	resolved, ok := r.resolveCallbackData(alias)
	if !ok {
		t.Fatalf("expected alias to resolve")
	}
	if resolved != longData {
		t.Fatalf("got %q, want %q", resolved, longData)
	}
	if got := resp.Buttons[0][1].Data; got != "home" {
		t.Fatalf("short callback should be unchanged, got %q", got)
	}
}

func TestResolveCallbackDataRejectsExpiredAlias(t *testing.T) {
	r := &Runner{
		callbackAliases: map[string]callbackAlias{
			"cb:old": {Data: "home", ExpiresAt: time.Now().Add(-time.Second)},
		},
	}
	if _, ok := r.resolveCallbackData("cb:old"); ok {
		t.Fatalf("expected expired alias to fail")
	}
	if _, ok := r.callbackAliases["cb:old"]; ok {
		t.Fatalf("expected expired alias to be removed")
	}
}

func TestGetPendingExpiresAction(t *testing.T) {
	r := &Runner{
		pending: map[int64]pendingAction{
			1: {Kind: pendingAddSite, ExpiresAt: time.Now().Add(-time.Second)},
		},
	}
	if _, ok := r.getPending(1); ok {
		t.Fatalf("expected expired pending action to be ignored")
	}
	if _, ok := r.pending[1]; ok {
		t.Fatalf("expected expired pending action to be removed")
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
