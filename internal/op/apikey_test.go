package op

import (
	"testing"

	dbpkg "github.com/U188/octopus/internal/db"
	"github.com/U188/octopus/internal/model"
)

func TestAPIKeyRefreshCacheClearsStaleKeys(t *testing.T) {
	ctx := setupSiteOpTestDB(t)
	apiKeyCache.Clear()
	apiKeyIDMap.Clear()
	t.Cleanup(func() {
		apiKeyCache.Clear()
		apiKeyIDMap.Clear()
	})

	stale := model.APIKey{ID: 999, Name: "stale", APIKey: "sk-stale", Enabled: true}
	apiKeyCache.Set(stale.ID, stale)
	apiKeyIDMap.Set(stale.APIKey, stale.ID)

	stored := model.APIKey{Name: "stored", APIKey: "sk-restored", Enabled: true}
	if err := dbpkg.GetDB().WithContext(ctx).Create(&stored).Error; err != nil {
		t.Fatalf("create api key: %v", err)
	}

	if err := apiKeyRefreshCache(ctx); err != nil {
		t.Fatalf("apiKeyRefreshCache failed: %v", err)
	}

	if _, ok := apiKeyCache.Get(stale.ID); ok {
		t.Fatalf("stale api key remained in cache")
	}
	if _, err := APIKeyGetByAPIKey(stale.APIKey, ctx); err == nil {
		t.Fatalf("stale api key remained in lookup map")
	}

	got, err := APIKeyGetByAPIKey(stored.APIKey, ctx)
	if err != nil {
		t.Fatalf("restored api key not found: %v", err)
	}
	if got.ID != stored.ID || got.APIKey != stored.APIKey {
		t.Fatalf("unexpected restored api key: %+v", got)
	}
}
