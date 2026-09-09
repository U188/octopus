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

func TestAPIKeyAuthDailyRequestLimitStopsAllKeyRoutes(t *testing.T) {
	const apiKey = "daily-limit-local-test-key"
	setupAPIKeyAuthTest(t, apiKey)
	record, err := op.APIKeyGetByAPIKey(apiKey, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := op.StatsAPIKeyDel(record.ID); err != nil {
		t.Fatal(err)
	}
	record.MaxDailyRequests = 1
	if err := op.APIKeyUpdate(&record, context.Background()); err != nil {
		t.Fatal(err)
	}

	request := func(router *gin.Engine) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/ok", nil)
		req.Header.Set("Authorization", "Bearer "+apiKey)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		return response
	}
	newRouter := func(countDailyRequest bool) *gin.Engine {
		router := gin.New()
		router.Use(APIKeyAuth(countDailyRequest))
		router.GET("/ok", func(c *gin.Context) { c.Status(http.StatusOK) })
		return router
	}

	if response := request(newRouter(true)); response.Code != http.StatusOK {
		t.Fatalf("first request status=%d body=%s", response.Code, response.Body.String())
	}
	response := request(newRouter(false))
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("non-counted route after limit status=%d body=%s", response.Code, response.Body.String())
	}
	if response.Header().Get("Retry-After") == "" {
		t.Fatal("missing Retry-After header")
	}
	var body struct {
		ErrorCode string `json:"error_code"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.ErrorCode != apperror.CodeAuthAPIKeyDailyRequestsExceeded {
		t.Fatalf("error code = %q, want %q", body.ErrorCode, apperror.CodeAuthAPIKeyDailyRequestsExceeded)
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
	router.Use(APIKeyAuth(false))
	router.GET("/ok", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	return router
}
