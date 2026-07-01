package migrate

import (
	"fmt"

	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 2,
		Up:      dropLegacyChannelColumns,
	})
}

// 002: drop legacy channels.key and channels.base_url columns after data migration
func dropLegacyChannelColumns(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}

	dialect := db.Dialector.Name()

	// column existence helper
	hasColumn := func(table, column string) bool {
		switch dialect {
		case "sqlite":
			var name string
			db.Raw("SELECT name FROM pragma_table_info(?) WHERE name = ? LIMIT 1", table, column).Scan(&name)
			return name == column
		case "mysql":
			var count int64
			db.Raw("SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = ? AND column_name = ?", table, column).Scan(&count)
			return count > 0
		case "postgres":
			var count int64
			db.Raw("SELECT COUNT(*) FROM information_schema.columns WHERE table_name = ? AND column_name = ?", table, column).Scan(&count)
			return count > 0
		default:
			return db.Migrator().HasColumn(table, column)
		}
	}

	// drop column helper
	dropColumn := func(table, column string) error {
		var sql string
		switch dialect {
		case "sqlite":
			// SQLite 3.35.0+
			sql = fmt.Sprintf("ALTER TABLE %s DROP COLUMN %s", table, column)
		case "mysql":
			sql = fmt.Sprintf("ALTER TABLE `%s` DROP COLUMN `%s`", table, column)
		case "postgres":
			sql = fmt.Sprintf("ALTER TABLE %s DROP COLUMN IF EXISTS %s", table, column)
		default:
			sql = fmt.Sprintf("ALTER TABLE %s DROP COLUMN %s", table, column)
		}
		return db.Exec(sql).Error
	}

	// drop channels.key
	if hasColumn("channels", "key") {
		if err := dropColumn("channels", "key"); err != nil {
			return fmt.Errorf("failed to drop channels.key: %w", err)
		}
	}

	// drop channels.base_url
	if hasColumn("channels", "base_url") {
		if err := dropColumn("channels", "base_url"); err != nil {
			return fmt.Errorf("failed to drop channels.base_url: %w", err)
		}
	}

	return nil
}
