package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/U188/octopus/internal/utils/log"
	"github.com/gin-gonic/gin"
)

const defaultRequestDebugMaxBodyBytes = 8192

type RequestDebugConfig struct {
	Enabled        bool
	IncludeHeaders bool
	IncludeBody    bool
	MaxBodyBytes   int
}

func RequestDebug(cfg RequestDebugConfig) gin.HandlerFunc {
	if cfg.MaxBodyBytes <= 0 {
		cfg.MaxBodyBytes = defaultRequestDebugMaxBodyBytes
	}
	return func(c *gin.Context) {
		if cfg.Enabled {
			fields := []interface{}{
				"method", c.Request.Method,
				"path", c.Request.URL.Path,
				"ip", c.ClientIP(),
				"content_type", c.GetHeader("Content-Type"),
				"content_length", c.Request.ContentLength,
			}
			if query := c.Request.URL.RawQuery; query != "" {
				fields = append(fields, "query", redactQuery(query))
			}
			if cfg.IncludeHeaders {
				fields = append(fields, "headers", redactHeaders(c.Request.Header))
			}
			if cfg.IncludeBody {
				body, omitted := debugRequestBody(c.Request, cfg.MaxBodyBytes)
				if body != "" {
					fields = append(fields, "body", body)
				}
				if omitted != "" {
					fields = append(fields, "body_omitted", omitted)
				}
			}
			log.Infow("http.debug.request", fields...)
		}
		c.Next()
	}
}

func debugRequestBody(r *http.Request, maxBytes int) (string, string) {
	if r == nil || r.Body == nil || r.Body == http.NoBody {
		return "", "empty"
	}
	if maxBytes <= 0 {
		maxBytes = defaultRequestDebugMaxBodyBytes
	}
	if r.ContentLength == 0 {
		return "", "empty"
	}
	if r.ContentLength < 0 {
		return "", "unknown_length"
	}
	if r.ContentLength > int64(maxBytes) {
		return "", "too_large"
	}
	if !isDebugTextContentType(r.Header.Get("Content-Type")) {
		return "", "non_text"
	}

	data, err := io.ReadAll(r.Body)
	if err != nil {
		return "", "read_error:" + err.Error()
	}
	r.Body = io.NopCloser(bytes.NewReader(data))
	if len(data) == 0 {
		return "", "empty"
	}
	if body, ok := redactJSONBody(data); ok {
		return body, ""
	}
	if body, ok := redactFormBody(data, r.Header.Get("Content-Type")); ok {
		return body, ""
	}
	return string(data), ""
}

func isDebugTextContentType(contentType string) bool {
	if strings.TrimSpace(contentType) == "" {
		return true
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		mediaType = strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	}
	mediaType = strings.ToLower(mediaType)
	return strings.HasPrefix(mediaType, "text/") ||
		strings.HasSuffix(mediaType, "+json") ||
		strings.HasSuffix(mediaType, "/json") ||
		mediaType == "application/x-www-form-urlencoded" ||
		mediaType == "application/graphql"
}

func redactHeaders(headers http.Header) map[string][]string {
	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	out := make(map[string][]string, len(keys))
	for _, key := range keys {
		values := headers.Values(key)
		if isSensitiveName(key) {
			out[key] = []string{"[REDACTED]"}
			continue
		}
		out[key] = append([]string(nil), values...)
	}
	return out
}

func redactJSONBody(data []byte) (string, bool) {
	var value interface{}
	if err := json.Unmarshal(data, &value); err != nil {
		return "", false
	}
	redactJSONValue(value)
	out, err := json.Marshal(value)
	if err != nil {
		return "", false
	}
	return string(out), true
}

func redactFormBody(data []byte, contentType string) (string, bool) {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		mediaType = strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	}
	if !strings.EqualFold(mediaType, "application/x-www-form-urlencoded") {
		return "", false
	}
	values, err := url.ParseQuery(string(data))
	if err != nil {
		return "", false
	}
	redactValues(values)
	return values.Encode(), true
}

func redactQuery(rawQuery string) string {
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return rawQuery
	}
	redactValues(values)
	return values.Encode()
}

func redactValues(values url.Values) {
	for key := range values {
		if isSensitiveName(key) {
			values[key] = []string{"[REDACTED]"}
		}
	}
}

func redactJSONValue(value interface{}) {
	switch typed := value.(type) {
	case map[string]interface{}:
		for key, child := range typed {
			if isSensitiveName(key) {
				typed[key] = "[REDACTED]"
				continue
			}
			redactJSONValue(child)
		}
	case []interface{}:
		for _, child := range typed {
			redactJSONValue(child)
		}
	}
}

func isSensitiveName(name string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(name), "-", "_"), " ", "_"))
	switch normalized {
	case "authorization", "proxy_authorization", "cookie", "set_cookie", "x_api_key", "api_key", "apikey", "access_token", "refresh_token", "token", "password", "secret", "client_secret", "key":
		return true
	default:
		return strings.Contains(normalized, "authorization") ||
			strings.Contains(normalized, "api_key") ||
			strings.Contains(normalized, "apikey") ||
			strings.Contains(normalized, "access_token") ||
			strings.Contains(normalized, "refresh_token") ||
			strings.Contains(normalized, "password") ||
			strings.Contains(normalized, "secret")
	}
}
