package migrate

import (
	"fmt"

	"github.com/U188/octopus/internal/model"
	"gorm.io/gorm"
)

func init() {
	RegisterBeforeAutoMigration(Migration{Version: 2026091501, Up: migrateGroupPromptSanitize})
}

type groupPromptSanitizeColumn struct {
	SystemPromptSanitizeFingerprints bool `gorm:"not null;default:false"`
}

func (groupPromptSanitizeColumn) TableName() string { return "groups" }

func migrateGroupPromptSanitize(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	if !db.Migrator().HasTable(&model.Group{}) {
		return nil
	}
	if !db.Migrator().HasColumn(&model.Group{}, "SystemPromptSanitizeFingerprints") {
		return db.Migrator().AddColumn(&groupPromptSanitizeColumn{}, "SystemPromptSanitizeFingerprints")
	}
	return nil
}
