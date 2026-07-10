package auth

import (
	"path/filepath"
	"testing"
	"time"

	dbpkg "github.com/U188/octopus/internal/db"
	"github.com/U188/octopus/internal/op"
)

func TestJWTExpiryValidationAndRotation(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "auth.db")
	if err := dbpkg.InitDB("sqlite", dbPath, false); err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	t.Cleanup(func() {
		_ = dbpkg.Close()
	})
	if err := op.InitCache(); err != nil {
		t.Fatalf("InitCache failed: %v", err)
	}

	jwtSecretMu.Lock()
	jwtSecretLoaded = false
	jwtSecretKey = nil
	jwtSecretMu.Unlock()

	if _, _, err := GenerateJWTToken(-2); err == nil {
		t.Fatal("expected expiry below -1 to fail")
	}
	if _, _, err := GenerateJWTToken(maxTokenExpiryMinutes + 1); err == nil {
		t.Fatal("expected expiry above maximum to fail")
	}

	oldToken, _, err := GenerateJWTToken(1)
	if err != nil {
		t.Fatalf("GenerateJWTToken failed: %v", err)
	}
	if !VerifyJWTToken(oldToken) {
		t.Fatal("expected generated token to verify")
	}
	if err := RotateJWTSecret(); err != nil {
		t.Fatalf("RotateJWTSecret failed: %v", err)
	}
	if VerifyJWTToken(oldToken) {
		t.Fatal("expected rotated secret to invalidate old token")
	}

	newToken, _, err := GenerateJWTToken(1)
	if err != nil {
		t.Fatalf("GenerateJWTToken after rotation failed: %v", err)
	}
	if !VerifyJWTToken(newToken) {
		t.Fatal("expected token generated after rotation to verify")
	}
}

func TestGenerateJWTTokenDefaultsTo24Hours(t *testing.T) {
	_, expiresAt, err := GenerateJWTToken(0)
	if err != nil {
		t.Fatalf("GenerateJWTToken failed: %v", err)
	}
	parsed, err := time.Parse(time.RFC3339, expiresAt)
	if err != nil {
		t.Fatalf("parse expiry: %v", err)
	}
	remaining := time.Until(parsed)
	if remaining < 23*time.Hour+59*time.Minute || remaining > 24*time.Hour+time.Minute {
		t.Fatalf("default expiry = %v, want approximately 24h", remaining)
	}
}

func TestLoginFailureLimiter(t *testing.T) {
	loginFailuresMu.Lock()
	loginFailures = make(map[string]loginFailureEntry)
	loginFailuresMu.Unlock()

	remote := "192.0.2.10:12345"
	for i := 0; i < loginFailureLimit; i++ {
		if allowed, _ := LoginAllowed(remote); !allowed {
			t.Fatalf("attempt %d should still be allowed", i+1)
		}
		RecordLoginFailure(remote)
	}
	if allowed, retryAfter := LoginAllowed(remote); allowed || retryAfter <= 0 {
		t.Fatalf("expected limiter to reject with retry duration, allowed=%t retry=%s", allowed, retryAfter)
	}
	ClearLoginFailures(remote)
	if allowed, _ := LoginAllowed(remote); !allowed {
		t.Fatal("expected successful login cleanup to allow future attempts")
	}
}
