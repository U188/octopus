package migrate

import (
	"github.com/U188/octopus/internal/model"
	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 10,
		Up:      migrateGroupHealthTables,
	})
}

func migrateGroupHealthTables(db *gorm.DB) error {
	return db.AutoMigrate(
		&model.GroupHealthSnapshot{},
		&model.GroupHealthAttempt{},
	)
}
