package migrate

import (
	"testing"

	"github.com/U188/octopus/internal/model"
)

func TestMigrateSiteImageModelRoutesBackfillsNonManualImageModels(t *testing.T) {
	db := openMigrationTestDB(t)
	if err := db.Exec("CREATE TABLE site_models (id INTEGER PRIMARY KEY, model_name TEXT NOT NULL, route_type TEXT NOT NULL, route_source TEXT, manual_override BOOLEAN, route_updated_at DATETIME)").Error; err != nil {
		t.Fatalf("create legacy site_models failed: %v", err)
	}
	if err := db.Exec("INSERT INTO site_models (id, model_name, route_type, route_source, manual_override) VALUES " +
		"(1, 'gpt-image-2', 'openai_chat', 'sync_inferred', FALSE)," +
		"(2, 'codex-gpt-image-2', 'openai_chat', 'sync_inferred', FALSE)," +
		"(3, 'gpt-image-manual', 'openai_chat', 'manual_override', TRUE)," +
		"(4, 'gpt-4o-mini', 'openai_chat', 'sync_inferred', FALSE)").Error; err != nil {
		t.Fatalf("insert legacy site_models failed: %v", err)
	}

	if err := migrateSiteImageModelRoutes(db); err != nil {
		t.Fatalf("migrateSiteImageModelRoutes failed: %v", err)
	}

	var rows []struct {
		ID        int
		RouteType string
	}
	if err := db.Table("site_models").Select("id, route_type").Order("id ASC").Scan(&rows).Error; err != nil {
		t.Fatalf("query migrated site_models failed: %v", err)
	}
	if rows[0].RouteType != string(model.SiteModelRouteTypeOpenAIImage) || rows[1].RouteType != string(model.SiteModelRouteTypeOpenAIImage) {
		t.Fatalf("expected automatic image models to be backfilled, got %#v", rows)
	}
	if rows[2].RouteType != string(model.SiteModelRouteTypeOpenAIChat) || rows[3].RouteType != string(model.SiteModelRouteTypeOpenAIChat) {
		t.Fatalf("expected manual/non-image rows to stay chat, got %#v", rows)
	}
}
