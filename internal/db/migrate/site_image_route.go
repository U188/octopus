package migrate

import (
	"fmt"

	"github.com/U188/octopus/internal/model"
	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 2026070501,
		Up:      migrateSiteImageModelRoutes,
	})
}

func migrateSiteImageModelRoutes(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	if !db.Migrator().HasTable("site_models") || !db.Migrator().HasColumn("site_models", "route_type") {
		return nil
	}

	updates := map[string]any{
		"route_type": string(model.SiteModelRouteTypeOpenAIImage),
	}
	if db.Migrator().HasColumn("site_models", "route_source") {
		updates["route_source"] = string(model.SiteModelRouteSourceSyncInferred)
	}
	if db.Migrator().HasColumn("site_models", "route_updated_at") {
		updates["route_updated_at"] = gorm.Expr("CURRENT_TIMESTAMP")
	}

	query := db.Table("site_models").
		Where("route_type = ?", string(model.SiteModelRouteTypeOpenAIChat)).
		Where(`(
			LOWER(TRIM(model_name)) LIKE ?
			OR LOWER(TRIM(model_name)) LIKE ?
			OR LOWER(TRIM(model_name)) LIKE ?
			OR LOWER(TRIM(model_name)) LIKE ?
			OR LOWER(TRIM(model_name)) LIKE ?
			OR LOWER(TRIM(model_name)) LIKE ?
		)`, "gpt-image-%", "%gpt-image-%", "dall-e%", "dalle%", "%image-generation%", "%-image-%")
	if db.Migrator().HasColumn("site_models", "manual_override") {
		query = query.Where("COALESCE(manual_override, ?) = ?", false, false)
	}
	return query.Updates(updates).Error
}
