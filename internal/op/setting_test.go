package op

import (
	"context"
	"path/filepath"
	"testing"

	dbpkg "github.com/U188/octopus/internal/db"
	"github.com/U188/octopus/internal/model"
)

func TestSettingListRedactsSensitiveValuesButReportsStoredStatus(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "settings.db")
	if err := dbpkg.InitDB("sqlite", dbPath, false); err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	t.Cleanup(func() {
		_ = dbpkg.Close()
		settingCache.Clear()
	})

	settingCache.Clear()
	if err := settingRefreshCache(context.Background()); err != nil {
		t.Fatalf("settingRefreshCache failed: %v", err)
	}
	if err := SettingSetString(model.SettingKeyWebDAVAutoBackupPassword, "dav-secret"); err != nil {
		t.Fatalf("SettingSetString failed: %v", err)
	}
	if err := SettingSetString(model.SettingKeyJWTSecret, "jwt-secret"); err != nil {
		t.Fatalf("SettingSetString JWT secret failed: %v", err)
	}

	settings, err := SettingList(context.Background())
	if err != nil {
		t.Fatalf("SettingList failed: %v", err)
	}

	for _, key := range []model.SettingKey{model.SettingKeyWebDAVAutoBackupPassword, model.SettingKeyJWTSecret} {
		found := false
		for _, setting := range settings {
			if setting.Key != key {
				continue
			}
			found = true
			if setting.Value != "" {
				t.Fatalf("expected sensitive value for %s to be redacted, got %q", key, setting.Value)
			}
			if setting.ValueStatus != "stored" {
				t.Fatalf("expected stored value status for %s, got %q", key, setting.ValueStatus)
			}
		}
		if !found {
			t.Fatalf("expected %s setting in list", key)
		}
	}
}
