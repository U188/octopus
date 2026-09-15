package migrate

import (
	"fmt"

	"github.com/U188/octopus/internal/model"
	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{Version: 21, Up: migrateRelayLogPromptRetryFields})
}

type relayLogPromptRetryFields struct {
	SystemPromptRetry       bool   `gorm:"not null;default:false"`
	SystemPromptRetryReason string `gorm:"type:varchar(32)"`
}

func (relayLogPromptRetryFields) TableName() string { return "relay_logs" }

func migrateRelayLogPromptRetryFields(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	if !db.Migrator().HasTable(&model.RelayLog{}) {
		return nil
	}
	for _, field := range []string{"SystemPromptRetry", "SystemPromptRetryReason"} {
		if !db.Migrator().HasColumn(&model.RelayLog{}, field) {
			if err := db.Migrator().AddColumn(&relayLogPromptRetryFields{}, field); err != nil {
				return err
			}
		}
	}
	return nil
}
