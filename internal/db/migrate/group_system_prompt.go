package migrate

import (
	"fmt"

	"github.com/U188/octopus/internal/model"
	"gorm.io/gorm"
)

func init() {
	RegisterBeforeAutoMigration(Migration{
		Version: 2026090801,
		Up:      migrateGroupSystemPrompt,
	})
}

type groupSystemPromptColumns struct {
	SystemPromptMode *string `gorm:"type:varchar(16)"`
	SystemPrompt     *string `gorm:"type:text"`
}

func (groupSystemPromptColumns) TableName() string {
	return "groups"
}

func migrateGroupSystemPrompt(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	if !db.Migrator().HasTable(&model.Group{}) {
		return nil
	}
	if !db.Migrator().HasColumn(&model.Group{}, "SystemPromptMode") {
		if err := db.Migrator().AddColumn(&groupSystemPromptColumns{}, "SystemPromptMode"); err != nil {
			return err
		}
	}
	if !db.Migrator().HasColumn(&model.Group{}, "SystemPrompt") {
		if err := db.Migrator().AddColumn(&groupSystemPromptColumns{}, "SystemPrompt"); err != nil {
			return err
		}
	}
	if err := db.Table("groups").Where("system_prompt_mode IS NULL OR system_prompt_mode = ?", "").Update("system_prompt_mode", model.SystemPromptModeOff).Error; err != nil {
		return err
	}
	return db.Table("groups").Where("system_prompt IS NULL").Update("system_prompt", "").Error
}
