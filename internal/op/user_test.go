package op

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/U188/octopus/internal/apperror"
	"github.com/U188/octopus/internal/conf"
	dbpkg "github.com/U188/octopus/internal/db"
	"github.com/U188/octopus/internal/model"
)

func TestUserInitCreatesRandomBootstrapPasswordFile(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "user.db")
	oldPath := conf.AppConfig.Database.Path
	oldType := conf.AppConfig.Database.Type
	conf.AppConfig.Database.Path = dbPath
	conf.AppConfig.Database.Type = "sqlite"
	t.Cleanup(func() {
		conf.AppConfig.Database.Path = oldPath
		conf.AppConfig.Database.Type = oldType
	})
	t.Setenv(initialAdminUsernameEnv, "")
	t.Setenv(initialAdminPasswordEnv, "")

	if err := dbpkg.InitDB("sqlite", dbPath, false); err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	t.Cleanup(func() {
		_ = dbpkg.Close()
	})
	if err := InitCache(); err != nil {
		t.Fatalf("InitCache failed: %v", err)
	}

	if err := UserInit(); err != nil {
		t.Fatalf("UserInit failed: %v", err)
	}
	passwordPath := filepath.Join(filepath.Dir(dbPath), initialAdminPasswordFile)
	passwordBytes, err := os.ReadFile(passwordPath)
	if err != nil {
		t.Fatalf("read bootstrap password failed: %v", err)
	}
	password := string(passwordBytes)
	if password == "admin\n" || len(password) < 20 {
		t.Fatalf("expected strong random bootstrap password, got length %d", len(password))
	}
	if err := UserVerify("admin", password[:len(password)-1]); err != nil {
		t.Fatalf("bootstrap credentials should verify: %v", err)
	}
	if _, err := os.Stat(passwordPath); !os.IsNotExist(err) {
		t.Fatalf("bootstrap password file should be removed after successful login, err=%v", err)
	}

	const (
		newPassword = "new-password"
		newSecret   = "rotated-jwt-secret"
	)
	if err := UserChangePasswordAndJWTSecret(password[:len(password)-1], newPassword, newSecret); err != nil {
		t.Fatalf("atomic password and jwt secret update failed: %v", err)
	}
	if err := UserVerify("admin", newPassword); err != nil {
		t.Fatalf("new password should verify: %v", err)
	}
	if secret, err := SettingGetString(model.SettingKeyJWTSecret); err != nil || secret != newSecret {
		t.Fatalf("jwt secret was not updated atomically: value=%q err=%v", secret, err)
	}

	for _, username := range []string{"", "admin"} {
		err := UserChangeUsernameAndJWTSecret(username, "next-jwt-secret")
		if apperror.Status(err) != http.StatusBadRequest ||
			apperror.Code(err) != apperror.CodeCommonValidationFailed {
			t.Fatalf("username %q should return a validation error, got %v", username, err)
		}
	}
}
