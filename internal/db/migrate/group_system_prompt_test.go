package migrate

import (
	"testing"

	"github.com/U188/octopus/internal/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

type legacyGroupWithoutSystemPrompt struct {
	ID   int `gorm:"primaryKey"`
	Name string
	Mode model.GroupMode
}

func (legacyGroupWithoutSystemPrompt) TableName() string {
	return "groups"
}

func TestMigrateGroupSystemPromptBackfillsLegacyRows(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&legacyGroupWithoutSystemPrompt{}); err != nil {
		t.Fatalf("create legacy groups: %v", err)
	}
	legacy := legacyGroupWithoutSystemPrompt{Name: "legacy", Mode: model.GroupModeFailover}
	if err := db.Create(&legacy).Error; err != nil {
		t.Fatalf("insert legacy group: %v", err)
	}

	if err := migrateGroupSystemPrompt(db); err != nil {
		t.Fatalf("migrate group system prompt: %v", err)
	}
	if err := db.AutoMigrate(&model.Group{}); err != nil {
		t.Fatalf("finalize group schema: %v", err)
	}

	var stored model.Group
	if err := db.First(&stored, legacy.ID).Error; err != nil {
		t.Fatalf("load migrated group: %v", err)
	}
	if stored.SystemPromptMode != model.SystemPromptModeOff || stored.SystemPrompt != "" {
		t.Fatalf("unexpected migrated prompt config: mode=%q prompt=%q", stored.SystemPromptMode, stored.SystemPrompt)
	}
}
