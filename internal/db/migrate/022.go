package migrate

import (
	"fmt"

	"github.com/U188/octopus/internal/model"
	"gorm.io/gorm"
)

func init() {
	RegisterBeforeAutoMigration(Migration{Version: 2026091601, Up: migrateGroupPromptFingerprintRules})
}

type groupPromptFingerprintRulesColumn struct {
	SystemPromptFingerprintRules string `gorm:"type:text;not null;default:''"`
	ConversationRewriteRules     string `gorm:"type:text;not null;default:''"`
}

func (groupPromptFingerprintRulesColumn) TableName() string { return "groups" }

func migrateGroupPromptFingerprintRules(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	if !db.Migrator().HasTable(&model.Group{}) {
		return nil
	}
	if !db.Migrator().HasColumn(&model.Group{}, "SystemPromptFingerprintRules") {
		if err := db.Migrator().AddColumn(&groupPromptFingerprintRulesColumn{}, "SystemPromptFingerprintRules"); err != nil {
			return err
		}
	}
	if !db.Migrator().HasColumn(&model.Group{}, "ConversationRewriteRules") {
		return db.Migrator().AddColumn(&groupPromptFingerprintRulesColumn{}, "ConversationRewriteRules")
	}
	return nil
}
