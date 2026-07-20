package migrate

import (
	"fmt"

	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 7,
		Up:      migrateSiteTokensAddValueStatus,
	})
}

// 007:
// - add site_tokens.value_status if missing
// - backfill existing masked token values to masked_pending
func migrateSiteTokensAddValueStatus(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	if !db.Migrator().HasTable("site_tokens") {
		return nil
	}

	if !db.Migrator().HasColumn("site_tokens", "value_status") {
		// 使用 VARCHAR(32) 而非 TEXT：MySQL STRICT 模式下 TEXT/BLOB 列不允许字面量 DEFAULT
		// (error 1101)，VARCHAR 则三种方言 (SQLite/MySQL/Postgres) 均支持 NOT NULL DEFAULT。
		if err := db.Exec("ALTER TABLE site_tokens ADD COLUMN value_status VARCHAR(32) NOT NULL DEFAULT 'ready'").Error; err != nil {
			return fmt.Errorf("failed to add site_tokens.value_status: %w", err)
		}
	}

	if err := db.Exec("UPDATE site_tokens SET value_status = 'masked_pending' WHERE token LIKE '%*%' OR token LIKE '%•%'").Error; err != nil {
		return fmt.Errorf("failed to backfill masked site_tokens.value_status: %w", err)
	}
	if err := db.Exec("UPDATE site_tokens SET value_status = 'ready' WHERE value_status IS NULL OR value_status = ''").Error; err != nil {
		return fmt.Errorf("failed to backfill empty site_tokens.value_status: %w", err)
	}

	return nil
}
