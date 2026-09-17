package middleware

import (
	"os"
	"strings"

	"github.com/gin-gonic/gin"
)

// frameAncestors returns the CSP frame-ancestors directive value and the
// X-Frame-Options value.
//
// Default (no env): DENY / 'none' — clickjacking protection, blocks all embedding.
//
// To allow embedding (e.g. inside a ModelScope studio iframe), set:
//
//	OCTOPUS_FRAME_ANCESTORS="https://*.modelscope.ai https://*.modelscope.cn"
//
// or simply OCTOPUS_ALLOW_EMBED=true to allow any origin (SAMEORIGIN + *).
func frameAncestors() (string, string) {
	if raw := strings.TrimSpace(os.Getenv("OCTOPUS_FRAME_ANCESTORS")); raw != "" {
		// X-Frame-Options cannot express a list; use ALLOW-FROM-less fallback:
		// when a specific allow-list is given we drop XFO entirely and rely on CSP,
		// which modern browsers honour and which supports arbitrary origins.
		return raw, ""
	}
	if v := strings.ToLower(strings.TrimSpace(os.Getenv("OCTOPUS_ALLOW_EMBED"))); v == "true" || v == "1" || v == "yes" {
		return "*", ""
	}
	return "'none'", "DENY"
}

func SecurityHeaders() gin.HandlerFunc {
	ancestors, xfo := frameAncestors()
	csp := "default-src 'self'; base-uri 'self'; object-src 'none'; frame-ancestors " + ancestors +
		"; form-action 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline';" +
		" img-src 'self' data: blob: http: https:; font-src 'self' data:;" +
		" connect-src 'self' http: https: ws: wss:; worker-src 'self' blob:"

	return func(c *gin.Context) {
		headers := c.Writer.Header()
		headers.Set("X-Content-Type-Options", "nosniff")
		if xfo != "" {
			headers.Set("X-Frame-Options", xfo)
		}
		headers.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		headers.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
		headers.Set("Content-Security-Policy", csp)
		if c.Request.TLS != nil || strings.EqualFold(c.GetHeader("X-Forwarded-Proto"), "https") {
			headers.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		c.Next()
	}
}
