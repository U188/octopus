package middleware

import (
	"net"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/U188/octopus/internal/model"
	"github.com/U188/octopus/internal/op"
	"github.com/U188/octopus/internal/server/resp"
	"github.com/U188/octopus/internal/utils/log"
	"github.com/gin-gonic/gin"
)

type parsedIPWhitelist struct {
	raw      string
	networks []*net.IPNet
	err      error
}

var ipWhitelistCache atomic.Pointer[parsedIPWhitelist]

// RequestIP returns the normalized client IP used by the server's request
// checks and logs. Gin applies its configured trusted-proxy rules here.
func RequestIP(c *gin.Context) string {
	if c == nil || c.Request == nil {
		return ""
	}
	ip := strings.TrimSpace(c.ClientIP())
	if net.ParseIP(strings.Trim(ip, "[]")) != nil {
		return strings.Trim(ip, "[]")
	}
	return strings.TrimSpace(c.RemoteIP())
}

// IPWhitelist enforces the API request IP whitelist. It is intentionally a
// standalone middleware so callers can use it on a route group without
// duplicating the setting lookup logic.
func IPWhitelist() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !checkIPWhitelist(c) {
			return
		}
		c.Next()
	}
}

func checkIPWhitelist(c *gin.Context) bool {
	ip := RequestIP(c)

	enabled, err := op.SettingGetBool(model.SettingKeyIPWhitelistEnabled)
	if err != nil {
		log.Warnw("http.ip_whitelist.setting_failed", "ip", ip, "error", err.Error())
		resp.Error(c, http.StatusInternalServerError, "failed to load IP whitelist setting")
		c.Abort()
		return false
	}
	if !enabled {
		return true
	}

	whitelist, err := op.SettingGetString(model.SettingKeyIPWhitelist)
	if err != nil {
		log.Warnw("http.ip_whitelist.setting_failed", "ip", ip, "error", err.Error())
		resp.Error(c, http.StatusInternalServerError, "failed to load IP whitelist")
		c.Abort()
		return false
	}
	networks, err := loadIPWhitelist(whitelist)
	if err != nil {
		// A malformed value should never be silently treated as an open list.
		log.Warnw("http.ip_whitelist.invalid", "ip", ip, "error", err.Error())
		resp.Error(c, http.StatusInternalServerError, "invalid IP whitelist setting")
		c.Abort()
		return false
	}
	if !ipAllowedByNetworks(ip, networks) {
		log.Warnw("http.ip_whitelist.denied",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"ip", ip,
		)
		resp.IPNotAllowed(c)
		c.Abort()
		return false
	}
	return true
}

func loadIPWhitelist(raw string) ([]*net.IPNet, error) {
	if cached := ipWhitelistCache.Load(); cached != nil && cached.raw == raw {
		return cached.networks, cached.err
	}
	networks, err := model.ParseIPWhitelist(raw)
	ipWhitelistCache.Store(&parsedIPWhitelist{raw: raw, networks: networks, err: err})
	return networks, err
}

func ipAllowedByNetworks(ip string, networks []*net.IPNet) bool {
	parsedIP := net.ParseIP(strings.Trim(strings.TrimSpace(ip), "[]"))
	if parsedIP == nil {
		return false
	}
	for _, network := range networks {
		if network.Contains(parsedIP) {
			return true
		}
		if ip4 := parsedIP.To4(); ip4 != nil && network.Contains(ip4) {
			return true
		}
	}
	return false
}
