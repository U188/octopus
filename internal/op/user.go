package op

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/U188/octopus/internal/apperror"
	"github.com/U188/octopus/internal/conf"
	"github.com/U188/octopus/internal/db"
	"github.com/U188/octopus/internal/model"
	"github.com/U188/octopus/internal/utils/log"
	"gorm.io/gorm"
)

var (
	userCache   model.User
	userCacheMu sync.RWMutex
)

const (
	initialAdminUsernameEnv  = "OCTOPUS_INITIAL_ADMIN_USERNAME"
	initialAdminPasswordEnv  = "OCTOPUS_INITIAL_ADMIN_PASSWORD"
	initialAdminPasswordFile = "initial-admin-password.txt"
)

func UserInit() error {
	userCacheMu.Lock()
	defer userCacheMu.Unlock()

	if err := db.GetDB().First(&userCache).Error; err == nil {
		return nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("load initial user: %w", err)
	}

	username := strings.TrimSpace(os.Getenv(initialAdminUsernameEnv))
	if username == "" {
		username = "admin"
	}
	password := os.Getenv(initialAdminPasswordEnv)
	passwordFile := ""
	if password == "" {
		var err error
		password, passwordFile, err = createInitialAdminPasswordFile()
		if err != nil {
			return err
		}
	}
	if err := validatePassword(password); err != nil {
		return fmt.Errorf("initial admin password: %w", err)
	}

	userCache.Username = username
	userCache.Password = password
	if err := userCache.HashPassword(); err != nil {
		return err
	}
	if err := db.GetDB().Create(&userCache).Error; err != nil {
		if passwordFile != "" {
			_ = os.Remove(passwordFile)
		}
		return err
	}
	if passwordFile != "" {
		log.Infof("initial admin user created; read the one-time password from %s", passwordFile)
	} else {
		log.Infof("initial admin user created from environment configuration")
	}
	return nil
}

func UserChangePasswordAndJWTSecret(oldPassword, newPassword, jwtSecret string) error {
	if err := validatePassword(newPassword); err != nil {
		return apperror.Wrap(apperror.CodeCommonValidationFailed, "invalid new password", err).WithStatus(http.StatusBadRequest)
	}
	if strings.TrimSpace(jwtSecret) == "" {
		return fmt.Errorf("jwt secret is required")
	}

	userCacheMu.Lock()
	defer userCacheMu.Unlock()
	current := userCache
	if err := current.ComparePassword(oldPassword); err != nil {
		return apperror.Wrap(apperror.CodeAuthPasswordIncorrect, "incorrect old password", err).WithStatus(http.StatusBadRequest)
	}

	updated := current
	updated.Password = newPassword
	if err := updated.HashPassword(); err != nil {
		return fmt.Errorf("failed to hash new password: %w", err)
	}
	if err := updateUserAndJWTSecret(updated, "password", updated.Password, jwtSecret); err != nil {
		return err
	}
	userCache = updated
	return nil
}

func UserChangeUsernameAndJWTSecret(newUsername, jwtSecret string) error {
	newUsername = strings.TrimSpace(newUsername)
	if newUsername == "" {
		return apperror.New(apperror.CodeCommonValidationFailed, "username is required").WithStatus(http.StatusBadRequest)
	}
	if strings.TrimSpace(jwtSecret) == "" {
		return fmt.Errorf("jwt secret is required")
	}

	userCacheMu.Lock()
	defer userCacheMu.Unlock()
	current := userCache
	if current.Username == newUsername {
		return apperror.New(apperror.CodeCommonValidationFailed, "new username is the same as the old username").WithStatus(http.StatusBadRequest)
	}
	updated := current
	updated.Username = newUsername
	if err := updateUserAndJWTSecret(updated, "username", updated.Username, jwtSecret); err != nil {
		return err
	}
	userCache = updated
	return nil
}

func updateUserAndJWTSecret(updated model.User, field string, value any, jwtSecret string) error {
	if field != "password" && field != "username" {
		return fmt.Errorf("unsupported user field %q", field)
	}
	err := db.GetDB().Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.User{}).Where("id = ?", updated.ID).Update(field, value)
		if result.Error != nil {
			return fmt.Errorf("failed to update %s: %w", field, result.Error)
		}
		if result.RowsAffected == 0 {
			return fmt.Errorf("failed to update %s: user not found", field)
		}
		result = tx.Model(&model.Setting{}).
			Where("key = ?", model.SettingKeyJWTSecret).
			Update("value", jwtSecret)
		if result.Error != nil {
			return fmt.Errorf("failed to rotate jwt secret: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return fmt.Errorf("failed to rotate jwt secret: setting not found")
		}
		return nil
	})
	if err != nil {
		return err
	}
	settingCache.Set(model.SettingKeyJWTSecret, jwtSecret)
	return nil
}

func UserVerify(username, password string) error {
	userCacheMu.RLock()
	current := userCache
	userCacheMu.RUnlock()
	if username != current.Username {
		return fmt.Errorf("incorrect username")
	}
	if err := current.ComparePassword(password); err != nil {
		return fmt.Errorf("incorrect password")
	}
	removeInitialAdminPasswordFile()
	return nil
}

func UserGet() model.User {
	userCacheMu.RLock()
	defer userCacheMu.RUnlock()
	return userCache
}

func validatePassword(password string) error {
	if len(password) < 6 {
		return fmt.Errorf("password must be at least 6 characters")
	}
	return nil
}

func createInitialAdminPasswordFile() (string, string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("generate initial admin password: %w", err)
	}
	password := base64.RawURLEncoding.EncodeToString(raw)
	dir := initialAdminCredentialDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", "", fmt.Errorf("create initial admin credential directory: %w", err)
	}
	path := filepath.Join(dir, initialAdminPasswordFile)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			stored, readErr := os.ReadFile(path)
			if readErr != nil {
				return "", "", fmt.Errorf("read existing initial admin password file: %w", readErr)
			}
			password = strings.TrimSpace(string(stored))
			if validateErr := validatePassword(password); validateErr != nil {
				return "", "", fmt.Errorf("existing initial admin password file: %w", validateErr)
			}
			return password, path, nil
		}
		return "", "", fmt.Errorf("create initial admin password file: %w", err)
	}
	if _, err := file.WriteString(password + "\n"); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", "", fmt.Errorf("write initial admin password file: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", "", fmt.Errorf("close initial admin password file: %w", err)
	}
	return password, path, nil
}

func initialAdminCredentialDir() string {
	if databaseType := strings.ToLower(strings.TrimSpace(conf.AppConfig.Database.Type)); databaseType != "" && databaseType != "sqlite" {
		return "data"
	}
	dir := filepath.Dir(conf.AppConfig.Database.Path)
	if dir == "" || dir == "." {
		return "data"
	}
	return dir
}

func removeInitialAdminPasswordFile() {
	path := filepath.Join(initialAdminCredentialDir(), initialAdminPasswordFile)
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Warnf("remove initial admin password file failed: %v", err)
	}
}
