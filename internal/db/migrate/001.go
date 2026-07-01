package migrate

import (
	"encoding/json"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 1,
		Up:      migrateChannelKeyToChannelKeys,
	})
}

// 001:
// - move legacy channels.key column into channel_keys table and drop channels.key
// - move legacy channels.base_url column into channels.base_urls (json) and drop channels.base_url
func migrateChannelKeyToChannelKeys(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}

	dialect := db.Dialector.Name()

	// column existence helper (sqlite needs exact check; gorm HasColumn may false-positive on "PRIMARY KEY")
	hasColumn := func(table, column string) (bool, error) {
		if dialect == "sqlite" {
			var name string
			if err := db.Raw("SELECT name FROM pragma_table_info(?) WHERE name = ? LIMIT 1", table, column).
				Scan(&name).Error; err != nil {
				return false, fmt.Errorf("failed to check sqlite column %s.%s: %w", table, column, err)
			}
			return name == column, nil
		}
		return db.Migrator().HasColumn(table, column), nil
	}

	// legacy columns may already be gone on a fresh DB
	hasKeyCol, err := hasColumn("channels", "key")
	if err != nil {
		return err
	}
	hasBaseURLCol, err := hasColumn("channels", "base_url")
	if err != nil {
		return err
	}
	if !hasKeyCol && !hasBaseURLCol {
		return nil
	}

	// --- migrate key -> channel_keys ---
	if hasKeyCol {
		if !db.Migrator().HasTable("channel_keys") {
			return fmt.Errorf("channel_keys table not found")
		}

		var quoted strings.Builder
		db.Dialector.QuoteTo(&quoted, "key")
		colRef := "c." + quoted.String()

		insertSQL := fmt.Sprintf(`
INSERT INTO channel_keys (channel_id, channel_key, status_code, last_use_time_stamp, total_cost)
SELECT c.id, %s, 0, 0, 0
FROM channels c
WHERE %s IS NOT NULL AND TRIM(%s) != ''
  AND NOT EXISTS (SELECT 1 FROM channel_keys k WHERE k.channel_id = c.id)
`, colRef, colRef, colRef)

		if err := db.Exec(insertSQL).Error; err != nil {
			return fmt.Errorf("failed to migrate channels.key to channel_keys: %w", err)
		}
	}

	// --- migrate base_url -> base_urls (json) ---
	if hasBaseURLCol {
		// Only set base_urls when base_urls is empty/null to avoid overwriting already-migrated data
		type row struct {
			ID       int    `gorm:"column:id"`
			BaseURL  string `gorm:"column:base_url"`
			BaseUrls string `gorm:"column:base_urls"`
		}
		rows := make([]row, 0)
		if err := db.Raw(`
SELECT id, base_url, base_urls
FROM channels
WHERE base_url IS NOT NULL AND TRIM(base_url) != ''
`).Scan(&rows).Error; err != nil {
			return fmt.Errorf("failed to read channels.base_url: %w", err)
		}

		for _, r := range rows {
			// if base_urls already present and not empty, skip
			if strings.TrimSpace(r.BaseUrls) != "" && strings.TrimSpace(r.BaseUrls) != "null" && strings.TrimSpace(r.BaseUrls) != "[]" {
				continue
			}
			payload, _ := json.Marshal([]map[string]any{
				{"url": r.BaseURL, "delay": 0},
			})
			if err := db.Exec("UPDATE channels SET base_urls = ? WHERE id = ?", string(payload), r.ID).Error; err != nil {
				return fmt.Errorf("failed to update channels.base_urls for id=%d: %w", r.ID, err)
			}
		}
	}

	return nil
}
