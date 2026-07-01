package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	dbpkg "github.com/U188/octopus/internal/db"
	"github.com/U188/octopus/internal/model"
	"github.com/U188/octopus/internal/op"
	"github.com/gin-gonic/gin"
)

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
