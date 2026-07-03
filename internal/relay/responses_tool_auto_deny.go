package relay

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/U188/octopus/internal/op"
	"github.com/U188/octopus/internal/transformer/outbound"
	"github.com/U188/octopus/internal/utils/log"
)

const responsesToolAutoDenyTTL = 24 * time.Hour

func responsesToolTypesFromHTTPRequest(req *http.Request) []string {
	if req == nil || req.Body == nil {
		return nil
	}
	body, err := readOutboundRequestBody(req)
	if err != nil || len(body) == 0 {
		return nil
	}
	var payload struct {
		Tools []struct {
			Type string `json:"type"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil
	}
	if len(payload.Tools) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(payload.Tools))
	tools := make([]string, 0, len(payload.Tools))
	for _, item := range payload.Tools {
		tool := strings.ToLower(strings.TrimSpace(item.Type))
		if tool == "" {
			continue
		}
		if _, ok := seen[tool]; ok {
			continue
		}
		seen[tool] = struct{}{}
		tools = append(tools, tool)
	}
	return tools
}

func (ra *relayAttempt) maybeAutoDenyResponsesTool(requestTools []string, responseBody string) {
	if ra == nil || ra.channel == nil || ra.channel.Type != outbound.OutboundTypeOpenAIResponse {
		return
	}
	tool, reason := inferResponsesToolPermissionDeny(requestTools, responseBody)
	if tool == "" {
		return
	}
	if err := op.ChannelAutoDenyResponsesTool(ra.channel.ID, tool, reason, responseBody, responsesToolAutoDenyTTL, ra.requestContext()); err != nil {
		log.Warnf("failed to auto deny responses tool %s for channel %s: %v", tool, ra.channel.Name, err)
		return
	}
	ra.autoDeniedResponsesTool = true
	log.Infof("auto denied responses tool %s for channel %s for %s: %s", tool, ra.channel.Name, responsesToolAutoDenyTTL, reason)
}

func inferResponsesToolPermissionDeny(requestTools []string, responseBody string) (string, string) {
	if len(requestTools) == 0 || strings.TrimSpace(responseBody) == "" {
		return "", ""
	}
	body := strings.ToLower(responseBody)
	if !looksLikeToolCapabilityError(body) {
		return "", ""
	}

	if hasTool(requestTools, "image_generation") &&
		(strings.Contains(body, "image generation") || strings.Contains(body, "image_generation")) {
		return "image_generation", "upstream image_generation capability denied"
	}
	if hasTool(requestTools, "web_search") &&
		(strings.Contains(body, "web search") || strings.Contains(body, "web_search")) {
		return "web_search", "upstream web_search capability denied"
	}
	if hasTool(requestTools, "tool_search") &&
		(strings.Contains(body, "tool search") || strings.Contains(body, "tool_search")) {
		return "tool_search", "upstream tool_search capability denied"
	}

	for _, tool := range requestTools {
		tool = strings.ToLower(strings.TrimSpace(tool))
		if tool == "" {
			continue
		}
		if strings.Contains(body, tool) || strings.Contains(body, strings.ReplaceAll(tool, "_", " ")) {
			return tool, "upstream responses tool capability denied"
		}
	}
	if len(requestTools) == 1 {
		tool := strings.ToLower(strings.TrimSpace(requestTools[0]))
		if tool != "" && strings.Contains(body, "permission_error") {
			return tool, "upstream responses tool permission denied"
		}
	}
	return "", ""
}

func looksLikeToolCapabilityError(body string) bool {
	for _, marker := range []string{
		"not enabled",
		"not allowed",
		"not support",
		"not supported",
		"permission",
		"forbidden",
		"disabled",
		"unauthorized",
		"requires",
	} {
		if strings.Contains(body, marker) {
			return true
		}
	}
	return false
}

func hasTool(tools []string, target string) bool {
	for _, tool := range tools {
		if strings.EqualFold(strings.TrimSpace(tool), target) {
			return true
		}
	}
	return false
}
