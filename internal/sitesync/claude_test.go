package sitesync

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/U188/octopus/internal/model"
)

func TestDetectClaudeOfficialPlatform(t *testing.T) {
	platform, routeType, err := DetectPlatform(context.Background(), "https://api.anthropic.com/v1")
	if err != nil {
		t.Fatalf("DetectPlatform returned error: %v", err)
	}
	if platform != model.SitePlatformClaude {
		t.Fatalf("expected Claude platform, got %q", platform)
	}
	if routeType != model.SiteModelRouteTypeAnthropic {
		t.Fatalf("expected Anthropic route, got %q", routeType)
	}
}

func TestFetchClaudeOfficialModelsUsesAnthropicAuth(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		if r.Header.Get("X-Api-Key") != "sk-ant-test" {
			t.Errorf("expected x-api-key header, got %q", r.Header.Get("X-Api-Key"))
		}
		if r.Header.Get("Anthropic-Version") != "2023-06-01" {
			t.Errorf("expected anthropic-version header, got %q", r.Header.Get("Anthropic-Version"))
		}
		switch r.URL.Path {
		case "/models":
			http.NotFound(w, r)
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data":     []map[string]any{{"id": "claude-sonnet-4-5"}},
				"has_more": false,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	models, err := fetchModelsForSiteToken(context.Background(), &model.Site{
		Platform: model.SitePlatformClaude,
		BaseURL:  server.URL,
	}, nil, model.SiteToken{Token: "sk-ant-test", Enabled: true})
	if err != nil {
		t.Fatalf("fetchModelsForSiteToken returned error: %v", err)
	}
	if requestCount != 2 {
		t.Fatalf("expected root and /v1 model requests, got %d", requestCount)
	}
	if len(models) != 1 || models[0] != "claude-sonnet-4-5" {
		t.Fatalf("unexpected models: %+v", models)
	}
}

func TestBuildClaudeOfficialConversationRequestUsesAPIKeyOnly(t *testing.T) {
	requestURL, _, headers := buildTestConversationRequest(
		&model.Site{Platform: model.SitePlatformClaude, BaseURL: "https://api.anthropic.com"},
		model.SiteToken{Token: "sk-ant-test"},
		"claude-sonnet-4-5",
		TestConversationModeAnthropic,
		"hi",
		TestConversationClientDefault,
		false,
	)

	if requestURL != "https://api.anthropic.com/v1/messages" {
		t.Fatalf("unexpected request URL: %q", requestURL)
	}
	if headers["X-API-Key"] != "sk-ant-test" {
		t.Fatalf("expected x-api-key header, got %q", headers["X-API-Key"])
	}
	if _, ok := headers["Authorization"]; ok {
		t.Fatalf("Claude official request must not include Authorization: %+v", headers)
	}
}
