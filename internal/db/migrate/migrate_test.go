package migrate

import (
	"errors"
	"testing"

	"gorm.io/gorm"
)

func TestMigrationRecordStatusValuesDistinct(t *testing.T) {
	if MigrationRecordStatusSuccess == MigrationRecordStatusFailed {
		t.Fatalf("MigrationRecordStatusSuccess (%d) must differ from MigrationRecordStatusFailed (%d)",
			MigrationRecordStatusSuccess, MigrationRecordStatusFailed)
	}
}

func TestFailedMigrationRetriesOnNextRun(t *testing.T) {
	db := openMigrationTestDB(t)

	runs := 0
	failing := []Migration{{
		Version: 990001,
		Up: func(db *gorm.DB) error {
			runs++
			if runs == 1 {
				return errors.New("boom")
			}
			return nil
		},
	}}

	if err := runMigrationsWithRecord(db, failing); err == nil {
		t.Fatalf("expected first run to fail")
	}

	var rec MigrationRecord
	if err := db.First(&rec, "version = ?", 990001).Error; err != nil {
		t.Fatalf("load migration record failed: %v", err)
	}
	if rec.Status != MigrationRecordStatusFailed {
		t.Fatalf("expected failed status %d, got %d", MigrationRecordStatusFailed, rec.Status)
	}

	if err := runMigrationsWithRecord(db, failing); err != nil {
		t.Fatalf("expected retry to succeed, got: %v", err)
	}
	if runs != 2 {
		t.Fatalf("expected migration Up to run twice (initial + retry), ran %d times", runs)
	}

	if err := db.First(&rec, "version = ?", 990001).Error; err != nil {
		t.Fatalf("reload migration record failed: %v", err)
	}
	if rec.Status != MigrationRecordStatusSuccess {
		t.Fatalf("expected success status after retry, got %d", rec.Status)
	}

	// 已成功的迁移不再重复执行。
	if err := runMigrationsWithRecord(db, failing); err != nil {
		t.Fatalf("third run should be a no-op, got: %v", err)
	}
	if runs != 2 {
		t.Fatalf("successful migration must not re-run, ran %d times", runs)
	}
}
