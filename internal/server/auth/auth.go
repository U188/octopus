package auth

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/U188/octopus/internal/conf"
	"github.com/U188/octopus/internal/model"
	"github.com/U188/octopus/internal/op"
	"github.com/U188/octopus/internal/utils/log"
	"github.com/golang-jwt/jwt/v5"
)

var (
	jwtSecretMu     sync.RWMutex
	jwtSecretLoaded bool
	jwtSecretKey    []byte
)

const (
	defaultTokenExpiryMinutes = 15
	maxTokenExpiryMinutes     = 30 * 24 * 60
)

// getJWTSecret returns the JWT signing key, generating and persisting one if needed.
func getJWTSecret() ([]byte, error) {
	jwtSecretMu.RLock()
	if jwtSecretLoaded {
		secret := append([]byte(nil), jwtSecretKey...)
		jwtSecretMu.RUnlock()
		return secret, nil
	}
	jwtSecretMu.RUnlock()

	jwtSecretMu.Lock()
	defer jwtSecretMu.Unlock()
	if jwtSecretLoaded {
		return append([]byte(nil), jwtSecretKey...), nil
	}

	secret, err := op.SettingGetString(model.SettingKeyJWTSecret)
	if err == nil && secret != "" {
		jwtSecretKey = []byte(secret)
		jwtSecretLoaded = true
		return append([]byte(nil), jwtSecretKey...), nil
	}

	generated, err := generateJWTSecret()
	if err != nil {
		return nil, err
	}
	if err := op.SettingSetString(model.SettingKeyJWTSecret, generated); err != nil {
		return nil, fmt.Errorf("persist JWT secret: %w", err)
	}
	jwtSecretKey = []byte(generated)
	jwtSecretLoaded = true
	return append([]byte(nil), jwtSecretKey...), nil
}

// RotateJWTSecret invalidates every existing JWT after account credential changes.
func RotateJWTSecret() error {
	generated, err := generateJWTSecret()
	if err != nil {
		return err
	}

	jwtSecretMu.Lock()
	defer jwtSecretMu.Unlock()
	if err := op.SettingSetString(model.SettingKeyJWTSecret, generated); err != nil {
		return fmt.Errorf("persist rotated JWT secret: %w", err)
	}
	jwtSecretKey = []byte(generated)
	jwtSecretLoaded = true
	return nil
}

func ChangePassword(oldPassword, newPassword string) error {
	generated, err := generateJWTSecret()
	if err != nil {
		return err
	}
	jwtSecretMu.Lock()
	defer jwtSecretMu.Unlock()
	if err := op.UserChangePasswordAndJWTSecret(oldPassword, newPassword, generated); err != nil {
		return err
	}
	jwtSecretKey = []byte(generated)
	jwtSecretLoaded = true
	return nil
}

func ChangeUsername(newUsername string) error {
	generated, err := generateJWTSecret()
	if err != nil {
		return err
	}
	jwtSecretMu.Lock()
	defer jwtSecretMu.Unlock()
	if err := op.UserChangeUsernameAndJWTSecret(newUsername, generated); err != nil {
		return err
	}
	jwtSecretKey = []byte(generated)
	jwtSecretLoaded = true
	return nil
}

func generateJWTSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate JWT secret: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func GenerateJWTToken(expiresMin int) (string, string, error) {
	if expiresMin < -1 || expiresMin > maxTokenExpiryMinutes {
		return "", "", fmt.Errorf("token expiry must be -1 or between 0 and %d minutes", maxTokenExpiryMinutes)
	}
	now := time.Now()
	claims := &jwt.RegisteredClaims{
		IssuedAt:  jwt.NewNumericDate(now),
		NotBefore: jwt.NewNumericDate(now),
		Issuer:    conf.APP_NAME,
	}
	if expiresMin == 0 {
		claims.ExpiresAt = jwt.NewNumericDate(now.Add(defaultTokenExpiryMinutes * time.Minute))
	} else if expiresMin > 0 {
		claims.ExpiresAt = jwt.NewNumericDate(now.Add(time.Duration(expiresMin) * time.Minute))
	} else if expiresMin == -1 {
		claims.ExpiresAt = jwt.NewNumericDate(now.Add(maxTokenExpiryMinutes * time.Minute))
	}
	secret, err := getJWTSecret()
	if err != nil {
		return "", "", err
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(secret)
	if err != nil {
		return "", "", err
	}
	return token, claims.ExpiresAt.Format(time.RFC3339), nil
}

func VerifyJWTToken(token string) bool {
	secret, err := getJWTSecret()
	if err != nil {
		log.Warnf("load JWT secret failed: %s", err.Error())
		return false
	}
	jwtToken, err := jwt.Parse(token, func(token *jwt.Token) (interface{}, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected JWT signing method: %s", token.Method.Alg())
		}
		return secret, nil
	}, jwt.WithIssuer(conf.APP_NAME), jwt.WithExpirationRequired())
	if err != nil || !jwtToken.Valid {
		return false
	}
	return true
}

func GenerateAPIKey() string {
	const keyChars = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	b := make([]byte, 48)
	maxI := big.NewInt(int64(len(keyChars)))
	for i := range b {
		n, err := rand.Int(rand.Reader, maxI)
		if err != nil {
			return ""
		}
		b[i] = keyChars[n.Int64()]
	}
	return "sk-" + conf.APP_NAME + "-" + string(b)
}
