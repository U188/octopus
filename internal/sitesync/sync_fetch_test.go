package sitesync

import (
	"errors"
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
