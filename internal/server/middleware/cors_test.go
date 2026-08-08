package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestCorsAllowsProxyRewrittenSameOriginRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(Cors())
	router.POST("/login", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	tests := []struct {
		name    string
		headers map[string]string
	}{
		{
			name: "fetch metadata same origin",
			headers: map[string]string{
				"Origin":         "https://example.ms.fun",
				"Sec-Fetch-Site": "same-origin",
			},
		},
		{
			name: "forwarded host same origin",
			headers: map[string]string{
				"Origin":            "https://example.ms.fun",
				"X-Forwarded-Host":  "example.ms.fun",
				"X-Forwarded-Proto": "https",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "http://internal:7860/login", nil)
			for key, value := range tt.headers {
				req.Header.Set(key, value)
			}
			resp := httptest.NewRecorder()
			router.ServeHTTP(resp, req)
			if resp.Code != http.StatusOK {
				t.Fatalf("expected proxy same-origin request to pass, status=%d", resp.Code)
			}
		})
	}
}

func TestCorsRejectsCrossSiteRequestWithoutAllowlist(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(Cors())
	router.POST("/login", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "http://internal:7860/login", nil)
	req.Header.Set("Origin", "https://attacker.example")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.Header.Set("X-Forwarded-Host", "example.ms.fun")
	req.Header.Set("X-Forwarded-Proto", "https")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("expected cross-site request to be rejected, status=%d", resp.Code)
	}
}
