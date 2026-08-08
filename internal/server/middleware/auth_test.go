package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/U188/octopus/internal/apperror"
	dbpkg "github.com/U188/octopus/internal/db"
	"github.com/U188/octopus/internal/model"
	"github.com/U188/octopus/internal/op"
	authpkg "github.com/U188/octopus/internal/server/auth"
	"github.com/gin-gonic/gin"
)

func TestAuthAcceptsOctopusAndLegacyAuthorizationHeaders(t *testing.T) {
	if err := dbpkg.InitDB("sqlite", filepath.Join(t.TempDir(), "admin-auth-test.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() {
		_ = dbpkg.Close()
	})
	if err := op.InitCache(); err != nil {
		t.Fatalf("init cache: %v", err)
	}
	token, _, err := authpkg.GenerateJWTToken(10)
	if err != nil {
		t.Fatalf("generate JWT: %v", err)
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(Auth())
	router.GET("/ok", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	tests := []struct {
		name          string
		authorization string
		octopusAuth   string
	}{
		{
			name:          "legacy authorization",
			authorization: "Bearer " + token,
		},
		{
			name:        "octopus authorization",
			octopusAuth: "Bearer " + token,
		},
		{
			name:          "octopus authorization takes precedence",
			authorization: "Bearer platform-managed-token",
			octopusAuth:   "Bearer " + token,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/ok", nil)
			if tt.authorization != "" {
				req.Header.Set("Authorization", tt.authorization)
			}
			if tt.octopusAuth != "" {
				req.Header.Set(octopusAuthorizationHeader, tt.octopusAuth)
			}
			resp := httptest.NewRecorder()
			router.ServeHTTP(resp, req)
			if resp.Code != http.StatusOK {
				t.Fatalf("expected authorization to pass, status=%d body=%s", resp.Code, resp.Body.String())
			}
		})
	}
}

func TestAPIKeyAuthAcceptsCustomKeyWithoutGeneratedPrefix(t *testing.T) {
	router := setupAPIKeyAuthTest(t, "custom-local-test-key")

	req := httptest.NewRequest(http.MethodGet, "/ok", nil)
	req.Header.Set("Authorization", "Bearer custom-local-test-key")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected custom API key to pass, status=%d body=%s", resp.Code, resp.Body.String())
	}
}

func TestAPIKeyAuthAcceptsCustomXAPIKey(t *testing.T) {
	router := setupAPIKeyAuthTest(t, "anthropic-custom-local-test-key")

	req := httptest.NewRequest(http.MethodGet, "/ok", nil)
	req.Header.Set("x-api-key", "anthropic-custom-local-test-key")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected custom x-api-key to pass, status=%d body=%s", resp.Code, resp.Body.String())
	}
}

func TestAPIKeyAuthInvalidKeyUsesAPIKeyErrorCode(t *testing.T) {
	router := setupAPIKeyAuthTest(t, "valid-local-test-key")

	req := httptest.NewRequest(http.MethodGet, "/ok", nil)
	req.Header.Set("Authorization", "Bearer wrong-local-test-key")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized, status=%d body=%s", resp.Code, resp.Body.String())
	}
	var body struct {
		ErrorCode string `json:"error_code"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if body.ErrorCode != apperror.CodeAuthAPIKeyInvalid {
		t.Fatalf("expected %q, got %q body=%s", apperror.CodeAuthAPIKeyInvalid, body.ErrorCode, resp.Body.String())
	}
}

func setupAPIKeyAuthTest(t *testing.T, apiKey string) *gin.Engine {
	t.Helper()

	if err := dbpkg.InitDB("sqlite", filepath.Join(t.TempDir(), "apikey-auth-test.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() {
		_ = dbpkg.Close()
	})
	record := &model.APIKey{
		Name:    "test-key",
		APIKey:  apiKey,
		Enabled: true,
	}
	if err := op.APIKeyCreate(record, context.Background()); err != nil {
		t.Fatalf("create api key: %v", err)
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(APIKeyAuth())
	router.GET("/ok", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	return router
}
