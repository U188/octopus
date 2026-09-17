package middleware

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func doRequest(t *testing.T) http.Header {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(SecurityHeaders())
	r.GET("/", func(c *gin.Context) { c.String(200, "ok") })
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	r.ServeHTTP(w, req)
	return w.Header()
}

func TestSecurityHeadersDefaultDeny(t *testing.T) {
	os.Unsetenv("OCTOPUS_ALLOW_EMBED")
	os.Unsetenv("OCTOPUS_FRAME_ANCESTORS")
	h := doRequest(t)
	if got := h.Get("X-Frame-Options"); got != "DENY" {
		t.Fatalf("default XFO = %q, want DENY", got)
	}
	if csp := h.Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Fatalf("default CSP missing frame-ancestors 'none': %s", csp)
	}
}

func TestSecurityHeadersAllowEmbed(t *testing.T) {
	os.Setenv("OCTOPUS_ALLOW_EMBED", "true")
	defer os.Unsetenv("OCTOPUS_ALLOW_EMBED")
	h := doRequest(t)
	if got := h.Get("X-Frame-Options"); got != "" {
		t.Fatalf("XFO should be omitted, got %q", got)
	}
	if csp := h.Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors *") {
		t.Fatalf("CSP should allow all ancestors: %s", csp)
	}
}

func TestSecurityHeadersFrameAncestorsList(t *testing.T) {
	os.Setenv("OCTOPUS_FRAME_ANCESTORS", "https://*.modelscope.ai https://*.modelscope.cn")
	defer os.Unsetenv("OCTOPUS_FRAME_ANCESTORS")
	h := doRequest(t)
	if got := h.Get("X-Frame-Options"); got != "" {
		t.Fatalf("XFO must be omitted when allow-list is used, got %q", got)
	}
	csp := h.Get("Content-Security-Policy")
	if !strings.Contains(csp, "https://*.modelscope.ai") || !strings.Contains(csp, "https://*.modelscope.cn") {
		t.Fatalf("CSP missing allow-list: %s", csp)
	}
}
