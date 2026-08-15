package sitesync

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/U188/octopus/internal/model"
	"github.com/U188/octopus/internal/transformer/outbound"
)

func TestDetectGrokOfficialPlatform(t *testing.T) {
	platform, routeType, err := DetectPlatform(context.Background(), "https://api.x.ai/v1")
	if err != nil {
		t.Fatalf("DetectPlatform returned error: %v", err)
	}
	if platform != model.SitePlatformGrok {
		t.Fatalf("expected Grok platform, got %q", platform)
	}
	if routeType != model.SiteModelRouteTypeOpenAIChat {
		t.Fatalf("expected OpenAI Chat route, got %q", routeType)
	}
}

func TestSyncGrokOfficialUsesVerbatimBearerKey(t *testing.T) {
	var observedAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		observedAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data": []map[string]any{
				{"id": "grok-4", "object": "model"},
			},
		})
	}))
	defer server.Close()

	site := &model.Site{Platform: model.SitePlatformGrok, BaseURL: server.URL + "/v1"}
	account := &model.SiteAccount{Name: "grok", CredentialType: model.SiteCredentialTypeAPIKey, APIKey: "xai-test"}
	snapshot, err := syncAccountState(context.Background(), site, account)
	if err != nil {
		t.Fatalf("syncAccountState returned error: %v", err)
	}
	if observedAuth != "Bearer xai-test" {
		t.Fatalf("expected verbatim bearer API key, got %q", observedAuth)
	}
	if len(snapshot.models) != 1 || snapshot.models[0].ModelName != "grok-4" {
		t.Fatalf("unexpected synced models: %+v", snapshot.models)
	}
	if actual := platformOutboundType(site); actual != outbound.OutboundTypeOpenAIChat {
		t.Fatalf("expected OpenAI Chat outbound type, got %v", actual)
	}
}
