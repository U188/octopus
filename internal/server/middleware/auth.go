package middleware

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/U188/octopus/internal/apperror"
	"github.com/U188/octopus/internal/op"
	"github.com/U188/octopus/internal/server/auth"
	"github.com/U188/octopus/internal/server/resp"
	"github.com/gin-gonic/gin"
)

const octopusAuthorizationHeader = "X-Octopus-Authorization"

func Auth() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Some hosting gateways reserve or replace Authorization. Prefer the
		// Octopus-specific header while retaining compatibility with existing clients.
		token := strings.TrimSpace(c.GetHeader(octopusAuthorizationHeader))
		if token == "" {
			token = strings.TrimSpace(c.GetHeader("Authorization"))
		}
		if token == "" {
			resp.Unauthorized(c)
			c.Abort()
			return
		}
		if !auth.VerifyJWTToken(strings.TrimPrefix(token, "Bearer ")) {
			resp.InvalidToken(c)
			c.Abort()
			return
		}
		c.Next()
	}
}

func APIKeyAuth(countDailyRequest bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !checkIPWhitelist(c) {
			return
		}

		var apiKey string
		var requestType string

		if key := strings.TrimSpace(c.Request.Header.Get("x-api-key")); key != "" {
			apiKey = key
			requestType = "anthropic"
		} else if authHeader := strings.TrimSpace(c.Request.Header.Get("Authorization")); authHeader != "" {
			apiKey = extractAPIKeyFromAuthorization(authHeader)
			requestType = "openai"
		}

		if apiKey == "" {
			resp.APIKeyMissing(c)
			c.Abort()
			return
		}

		apiKeyObj, err := op.APIKeyGetByAPIKey(apiKey, c.Request.Context())
		if err != nil {
			resp.APIKeyInvalid(c)
			c.Abort()
			return
		}
		if !apiKeyObj.Enabled {
			resp.ErrorWithAppError(c, http.StatusUnauthorized, apperror.New(apperror.CodeAuthAPIKeyDisabled, "API key is disabled").WithStatus(http.StatusUnauthorized))
			c.Abort()
			return
		}
		now := time.Now()
		if apiKeyObj.ExpireAt > 0 && apiKeyObj.ExpireAt < now.Unix() {
			resp.APIKeyExpired(c)
			c.Abort()
			return
		}
		if apiKeyObj.MaxRPM > 0 {
			allowed, retryAfter := op.RateLimitCheck(apiKeyObj.ID, apiKeyObj.MaxRPM)
			if !allowed {
				c.Header("Retry-After", strconv.Itoa(retryAfter))
				resp.ErrorWithAppError(c, http.StatusTooManyRequests, apperror.New(apperror.CodeAuthAPIKeyRateLimited, "API key has exceeded the rate limit").WithStatus(http.StatusTooManyRequests))
				c.Abort()
				return
			}
		}
		switch op.APIKeyQuotaCheck(apiKeyObj, countDailyRequest, now) {
		case op.APIKeyQuotaTotalCostExceeded:
			resp.ErrorWithAppError(c, http.StatusUnauthorized, apperror.New(apperror.CodeAuthAPIKeyCostExceeded, "API key has reached the max cost").WithStatus(http.StatusUnauthorized))
			c.Abort()
			return
		case op.APIKeyQuotaDailyCostExceeded:
			c.Header("Retry-After", strconv.Itoa(secondsUntilNextDay(now)))
			resp.ErrorWithAppError(c, http.StatusTooManyRequests, apperror.New(apperror.CodeAuthAPIKeyDailyCostExceeded, "API key has reached the daily cost limit").WithStatus(http.StatusTooManyRequests))
			c.Abort()
			return
		case op.APIKeyQuotaDailyRequestsExceeded:
			c.Header("Retry-After", strconv.Itoa(secondsUntilNextDay(now)))
			resp.ErrorWithAppError(c, http.StatusTooManyRequests, apperror.New(apperror.CodeAuthAPIKeyDailyRequestsExceeded, "API key has reached the daily request limit").WithStatus(http.StatusTooManyRequests))
			c.Abort()
			return
		}
		c.Set("request_type", requestType)
		c.Set("supported_models", apiKeyObj.SupportedModels)
		c.Set("api_key_id", apiKeyObj.ID)
		c.Next()
	}
}

func secondsUntilNextDay(now time.Time) int {
	tomorrow := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
	seconds := int(tomorrow.Sub(now).Seconds())
	if seconds < 1 {
		return 1
	}
	return seconds
}

func extractAPIKeyFromAuthorization(authHeader string) string {
	authHeader = strings.TrimSpace(authHeader)
	if len(authHeader) >= 7 && strings.EqualFold(authHeader[:7], "Bearer ") {
		return strings.TrimSpace(authHeader[7:])
	}
	return authHeader
}
