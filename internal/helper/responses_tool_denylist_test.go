package helper

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestApplyResponsesToolDenylistRemovesConfiguredTools(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "https://example.com/v1/responses", strings.NewReader(`{
		"model":"gpt-5.5",
		"tools":[
			{"type":"function","name":"run"},
			{"type":"image_generation"},
			{"type":"web_search"}
		],
		"tool_choice":"auto"
	}`))
	if err != nil {
		t.Fatalf("NewRequest failed: %v", err)
	}

	if err := ApplyResponsesToolDenylist(req, []string{" image_generation ", "WEB_SEARCH"}); err != nil {
		t.Fatalf("ApplyResponsesToolDenylist failed: %v", err)
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}
	got := string(body)
	if strings.Contains(got, "image_generation") || strings.Contains(got, "web_search") {
		t.Fatalf("expected denied tools to be removed, got %s", got)
	}
	if !strings.Contains(got, `"function"`) || !strings.Contains(got, `"tool_choice":"auto"`) {
		t.Fatalf("expected allowed tool and auto tool_choice to remain, got %s", got)
	}
}

func TestApplyResponsesToolDenylistRemovesToolChoiceWhenNoToolsRemain(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "https://example.com/v1/responses", strings.NewReader(`{
		"model":"gpt-5.5",
		"tools":[{"type":"image_generation"}],
		"tool_choice":{"type":"image_generation"}
	}`))
	if err != nil {
		t.Fatalf("NewRequest failed: %v", err)
	}

	if err := ApplyResponsesToolDenylist(req, []string{"image_generation"}); err != nil {
		t.Fatalf("ApplyResponsesToolDenylist failed: %v", err)
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}
	got := string(body)
	if strings.Contains(got, "tools") || strings.Contains(got, "tool_choice") {
		t.Fatalf("expected tools and tool_choice to be removed, got %s", got)
	}
}

func TestApplyResponsesToolDenylistWithReportReturnsRemovedTools(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "https://example.com/v1/responses", strings.NewReader(`{
		"model":"gpt-5.5",
		"tools":[
			{"type":"image_generation"},
			{"type":"image_generation"},
			{"type":"function","name":"run"}
		],
		"tool_choice":{"type":"image_generation"}
	}`))
	if err != nil {
		t.Fatalf("NewRequest failed: %v", err)
	}

	removed, toolChoiceRemoved, err := ApplyResponsesToolDenylistWithReport(req, []string{"image_generation"})
	if err != nil {
		t.Fatalf("ApplyResponsesToolDenylistWithReport failed: %v", err)
	}
	if len(removed) != 1 || removed[0] != "image_generation" {
		t.Fatalf("expected one removed image_generation tool, got %#v", removed)
	}
	if !toolChoiceRemoved {
		t.Fatalf("expected tool_choice removal to be reported")
	}
}
