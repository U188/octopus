package op

import (
	"path/filepath"
	"testing"

	dbpkg "github.com/U188/octopus/internal/db"
	"github.com/U188/octopus/internal/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestStripRelayLogsFromSQLiteBackupKeepsDatabaseRestorable(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "backup.db")
	conn, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite db failed: %v", err)
	}
	if err := conn.AutoMigrate(&model.User{}, &model.RelayLog{}, &model.Setting{}); err != nil {
		t.Fatalf("migrate sqlite db failed: %v", err)
	}
	if err := conn.Create(&[]model.RelayLog{
		{ID: 1, Time: 1, RequestModelName: "gpt", RequestContent: "request", ResponseContent: "response", Success: true},
		{ID: 2, Time: 2, RequestModelName: "gpt", RequestContent: "request 2", ResponseContent: "response 2", Success: true},
	}).Error; err != nil {
		t.Fatalf("seed relay logs failed: %v", err)
	}
	if err := conn.Create(&model.Setting{Key: model.SettingKeyRelayLogKeepPeriod, Value: "7"}).Error; err != nil {
		t.Fatalf("seed setting failed: %v", err)
	}
	sqlDB, err := conn.DB()
	if err != nil {
		t.Fatalf("get db handle failed: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close sqlite db failed: %v", err)
	}

	if err := stripRelayLogsFromSQLiteBackup(dbPath); err != nil {
		t.Fatalf("stripRelayLogsFromSQLiteBackup returned error: %v", err)
	}
	if err := dbpkg.ValidateSQLiteDatabaseFile(dbPath); err != nil {
		t.Fatalf("stripped backup should remain valid: %v", err)
	}

	conn, err = gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("reopen sqlite db failed: %v", err)
	}
	var relayLogCount int64
	if err := conn.Model(&model.RelayLog{}).Count(&relayLogCount).Error; err != nil {
		t.Fatalf("count relay logs failed: %v", err)
	}
	if relayLogCount != 0 {
		t.Fatalf("expected relay logs to be stripped, got %d", relayLogCount)
	}
	var settingCount int64
	if err := conn.Model(&model.Setting{}).Count(&settingCount).Error; err != nil {
		t.Fatalf("count settings failed: %v", err)
	}
	if settingCount != 1 {
		t.Fatalf("expected non-log tables to be preserved, got %d settings", settingCount)
	}
}

func TestStripRelayLogsFromSQLiteBackupNoopsWithoutRelayLogsTable(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "backup.db")
	conn, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite db failed: %v", err)
	}
	if err := conn.AutoMigrate(&model.Setting{}); err != nil {
		t.Fatalf("migrate sqlite db failed: %v", err)
	}
	sqlDB, err := conn.DB()
	if err != nil {
		t.Fatalf("get db handle failed: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close sqlite db failed: %v", err)
	}

	if err := stripRelayLogsFromSQLiteBackup(dbPath); err != nil {
		t.Fatalf("stripRelayLogsFromSQLiteBackup returned error: %v", err)
	}
}
