package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	dbpkg "github.com/U188/octopus/internal/db"
	"github.com/U188/octopus/internal/model"
	"github.com/U188/octopus/internal/op"
	"github.com/gin-gonic/gin"
)

func TestAPIKeyAuthEnforcesIPWhitelist(t *testing.T) {
	router := setupIPWhitelistRouter(t, true, "203.0.113.10")

	allowed := httptest.NewRequest(http.MethodGet, "/ok", nil)
	allowed.RemoteAddr = "203.0.113.10:1234"
	allowed.Header.Set("Authorization", "Bearer whitelist-test-key")
	allowedResp := httptest.NewRecorder()
	router.ServeHTTP(allowedResp, allowed)
	if allowedResp.Code != http.StatusOK {
		t.Fatalf("allowed request status=%d body=%s", allowedResp.Code, allowedResp.Body.String())
	}

	denied := httptest.NewRequest(http.MethodGet, "/ok", nil)
	denied.RemoteAddr = "198.51.100.20:1234"
	denied.Header.Set("Authorization", "Bearer whitelist-test-key")
	deniedResp := httptest.NewRecorder()
	router.ServeHTTP(deniedResp, denied)
	if deniedResp.Code != http.StatusForbidden {
		t.Fatalf("denied request status=%d body=%s", deniedResp.Code, deniedResp.Body.String())
	}
	var body struct {
		ErrorCode string `json:"error_code"`
	}
	if err := json.Unmarshal(deniedResp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode denied response: %v", err)
	}
	if body.ErrorCode != "auth.ip_not_allowed" {
		t.Fatalf("denied error_code=%q", body.ErrorCode)
	}
}

func TestAPIKeyAuthAllowsAllIPsWhenWhitelistDisabled(t *testing.T) {
	router := setupIPWhitelistRouter(t, false, "203.0.113.10")
	req := httptest.NewRequest(http.MethodGet, "/ok", nil)
	req.RemoteAddr = "198.51.100.20:1234"
	req.Header.Set("Authorization", "Bearer whitelist-test-key")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("disabled whitelist status=%d body=%s", resp.Code, resp.Body.String())
	}
}

func TestRequestIPOnlyUsesForwardedHeaderFromTrustedProxy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	if err := router.SetTrustedProxies([]string{"127.0.0.1"}); err != nil {
		t.Fatalf("set trusted proxies: %v", err)
	}
	router.GET("/ip", func(c *gin.Context) { c.String(http.StatusOK, RequestIP(c)) })

	req := httptest.NewRequest(http.MethodGet, "/ip", nil)
	req.RemoteAddr = "127.0.0.1:8080"
	req.Header.Set("X-Forwarded-For", "203.0.113.10")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	if got := resp.Body.String(); got != "203.0.113.10" {
		t.Fatalf("trusted proxy client IP=%q", got)
	}

	withoutTrust := gin.New()
	if err := withoutTrust.SetTrustedProxies(nil); err != nil {
		t.Fatalf("disable trusted proxies: %v", err)
	}
	withoutTrust.GET("/ip", func(c *gin.Context) { c.String(http.StatusOK, RequestIP(c)) })
	resp = httptest.NewRecorder()
	withoutTrust.ServeHTTP(resp, req)
	if got := resp.Body.String(); got != "127.0.0.1" {
		t.Fatalf("untrusted forwarded client IP=%q", got)
	}
}

func setupIPWhitelistRouter(t *testing.T, enabled bool, whitelist string) *gin.Engine {
	t.Helper()
	if err := dbpkg.InitDB("sqlite", filepath.Join(t.TempDir(), "ip-whitelist-test.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() {
		// Keep the process-wide setting cache from affecting other middleware
		// tests that run in the same package.
		_ = op.SettingSetString(model.SettingKeyIPWhitelistEnabled, "false")
		_ = op.SettingSetString(model.SettingKeyIPWhitelist, "")
		_ = dbpkg.Close()
	})
	if err := op.InitCache(); err != nil {
		t.Fatalf("init cache: %v", err)
	}
	if err := op.SettingSetString(model.SettingKeyIPWhitelistEnabled, map[bool]string{true: "true", false: "false"}[enabled]); err != nil {
		t.Fatalf("set whitelist enabled: %v", err)
	}
	if err := op.SettingSetString(model.SettingKeyIPWhitelist, whitelist); err != nil {
		t.Fatalf("set whitelist: %v", err)
	}
	if err := op.APIKeyCreate(&model.APIKey{Name: "whitelist-test", APIKey: "whitelist-test-key", Enabled: true}, context.Background()); err != nil {
		t.Fatalf("create API key: %v", err)
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(APIKeyAuth(false))
	router.GET("/ok", func(c *gin.Context) { c.Status(http.StatusOK) })
	return router
}
