package handlers

import (
	"encoding/json"
	"strings"

	"github.com/U188/octopus/internal/model"
	"github.com/U188/octopus/internal/op"
	"github.com/U188/octopus/internal/utils/log"
	"github.com/gin-gonic/gin"
)

func recordAudit(c *gin.Context, action, status string, detail map[string]any, auditErr error) {
	detailJSON := ""
	if detail != nil {
		if data, err := json.Marshal(sanitizeAuditDetail(detail)); err == nil {
			detailJSON = string(data)
		}
	}
	errText := ""
	if auditErr != nil {
		errText = auditErr.Error()
	}
	actor := "admin"
	if user := op.UserGet(); user.Username != "" {
		actor = user.Username
	}
	entry := &model.AuditLog{
		Action:    action,
		Status:    status,
		Actor:     actor,
		IP:        c.ClientIP(),
		UserAgent: c.Request.UserAgent(),
		Method:    c.Request.Method,
		Path:      c.FullPath(),
		Detail:    detailJSON,
		Error:     errText,
	}
	if err := op.AuditCreate(c.Request.Context(), entry); err != nil {
		log.Warnf("failed to write audit log: %v", err)
	}
}

func recordAuditSuccess(c *gin.Context, action string, detail map[string]any) {
	recordAudit(c, action, op.AuditStatusSuccess, detail, nil)
}

func recordAuditFailure(c *gin.Context, action string, detail map[string]any, err error) {
	recordAudit(c, action, op.AuditStatusFailed, detail, err)
}

func redactedSettingValue(key model.SettingKey, value string) string {
	if model.IsSensitiveSettingKey(key) && value != "" {
		return "<redacted>"
	}
	return value
}

func sanitizeAuditDetail(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			if isSensitiveAuditDetailKey(key) {
				out[key] = "<redacted>"
				continue
			}
			out[key] = sanitizeAuditDetail(item)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = sanitizeAuditDetail(item)
		}
		return out
	default:
		return value
	}
}

func isSensitiveAuditDetailKey(key string) bool {
	normalized := strings.ToLower(strings.NewReplacer("-", "_", " ", "_").Replace(strings.TrimSpace(key)))
	if normalized == "" {
		return false
	}
	if normalized == "key" || strings.HasSuffix(normalized, "_key") {
		return true
	}
	for _, marker := range []string{
		"api_key",
		"apikey",
		"access_token",
		"refresh_token",
		"authorization",
		"bearer",
		"cookie",
		"credential",
		"password",
		"secret",
		"token",
	} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}
