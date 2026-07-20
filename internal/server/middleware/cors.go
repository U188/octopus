package middleware

import (
	"strings"

	"github.com/U188/octopus/internal/model"
	"github.com/U188/octopus/internal/op"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

func Cors() gin.HandlerFunc {
	config := cors.DefaultConfig()
	config.AllowCredentials = true
	config.AllowMethods = []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"}
	// 凭据模式下 Access-Control-Allow-Headers 不能用通配符 "*"（浏览器按字面头名处理，
	// 且通配符永不覆盖 Authorization），必须显式列出允许的请求头。
	config.AllowHeaders = []string{
		"Origin",
		"Content-Type",
		"Content-Length",
		"Accept",
		"Accept-Encoding",
		"Authorization",
		"X-Requested-With",
		"X-Api-Key",
		"Anthropic-Version",
		"Anthropic-Beta",
	}
	config.ExposeHeaders = []string{"Content-Disposition"}
	// CORS 白名单:
	// - 为空: 不允许跨域
	// - "*": 允许所有来源
	// - 逗号分隔的域名列表: 只允许指定的域名 (如 "https://example.com,https://example2.com")
	config.AllowOriginFunc = func(origin string) bool {
		allowed, err := op.SettingGetString(model.SettingKeyCORSAllowOrigins)
		if err != nil {
			return false
		}
		allowed = strings.TrimSpace(allowed)
		if allowed == "" {
			return false
		}
		if allowed == "*" {
			return true
		}

		origin = strings.TrimSpace(origin)
		if origin == "" {
			return false
		}

		// 提取 origin 的 host 部分用于匹配
		originHost := origin
		if idx := strings.Index(origin, "://"); idx != -1 {
			originHost = origin[idx+3:]
		}
		originHost = strings.TrimRight(originHost, "/")

		for _, item := range strings.Split(allowed, ",") {
			item = strings.TrimSpace(item)
			item = strings.TrimRight(item, "/")
			if item == "" {
				continue
			}
			// 支持完整 origin (https://example.com) 或仅域名 (example.com)
			if item == origin || item == originHost {
				return true
			}
		}
		return false
	}
	return cors.New(config)
}
