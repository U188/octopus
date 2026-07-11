package sitesync

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/U188/octopus/internal/model"
)

func TestSyncSiteModelsByGroupTreatsInvalidTokenAsMissingKey(t *testing.T) {
	tokens := []model.SiteToken{{
		Name:        "expired",
		Token:       "sk-expired",
		GroupKey:    "vip",
		GroupName:   "VIP",
		Enabled:     true,
		ValueStatus: model.SiteTokenValueStatusReady,
		Source:      "sync",
	}}

	models, results := syncSiteModelsByGroup(nil, nil, nil, "", tokens, 0, "sync", func(token model.SiteToken, allowGlobalFallback bool) (siteModelFetchResult, error) {
		return siteModelFetchResult{}, errors.New("http 401: 无效的令牌")
	})

	if len(models) != 0 {
		t.Fatalf("expected no synced models for invalid key, got %+v", models)
	}
	if len(results) != 1 {
		t.Fatalf("expected one group result, got %+v", results)
	}
	if results[0].Status != siteGroupSyncStatusMissingKey {
		t.Fatalf("expected invalid key to be treated as missing_key, got %+v", results[0])
	}
	if !results[0].HasKey {
		t.Fatalf("expected result to remember that upstream returned a key")
	}
}

func TestFetchModelsForSiteTokenKeepsSuccessfulEmptyCandidate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte("<html><head><title>Hlool API</title></head><body>ok</body></html>"))
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	models, err := fetchModelsForSiteToken(context.Background(), &model.Site{
		Platform: model.SitePlatformAPI,
		BaseURL:  server.URL,
	}, nil, model.SiteToken{
		Token:       "sk-test",
		GroupKey:    model.SiteDefaultGroupKey,
		GroupName:   model.SiteDefaultGroupName,
		Enabled:     true,
		ValueStatus: model.SiteTokenValueStatusReady,
	})
	if err != nil {
		t.Fatalf("expected empty successful /v1/models result to win over root html error, got %v", err)
	}
	if len(models) != 0 {
		t.Fatalf("expected no models, got %+v", models)
	}
}
