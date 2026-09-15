package migrate

import (
	"fmt"

	"github.com/U188/octopus/internal/model"
	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 19,
		Up:      migrateRelayLogUpstreamFields,
	})
}

type relayLogUpstreamFields struct {
	UpstreamRequestContent string `gorm:"type:text"`
	UpstreamBaseURL        string `gorm:"type:text"`
}

func (relayLogUpstreamFields) TableName() string {
	return "relay_logs"
}

func migrateRelayLogUpstreamFields(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	if !db.Migrator().HasTable(&model.RelayLog{}) {
		return nil
	}
	if !db.Migrator().HasColumn(&model.RelayLog{}, "UpstreamRequestContent") {
		if err := db.Migrator().AddColumn(&relayLogUpstreamFields{}, "UpstreamRequestContent"); err != nil {
			return err
		}
	}
	if !db.Migrator().HasColumn(&model.RelayLog{}, "UpstreamBaseURL") {
		if err := db.Migrator().AddColumn(&relayLogUpstreamFields{}, "UpstreamBaseURL"); err != nil {
			return err
		}
	}
	return nil
}
