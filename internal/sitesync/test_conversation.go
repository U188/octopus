package sitesync

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/U188/octopus/internal/claudemode"
	"github.com/U188/octopus/internal/codexmode"
	"github.com/U188/octopus/internal/model"
	"github.com/U188/octopus/internal/op"
	"github.com/U188/octopus/internal/utils/log"
)

type TestConversationMode string
type TestConversationClient string

const (
	TestConversationModeOpenAIChat     TestConversationMode = "openai_chat"
	TestConversationModeOpenAIResponse TestConversationMode = "openai_response"
	TestConversationModeOpenAIImage    TestConversationMode = "openai_image"
	TestConversationModeAnthropic      TestConversationMode = "anthropic"

	TestConversationClientDefault TestConversationClient = ""
	TestConversationClientCodex   TestConversationClient = "codex"
	TestConversationClientClaude  TestConversationClient = "claude"

	claudeTestConversationUserAgent = claudemode.UserAgent
	claudeTestConversationBeta      = claudemode.BaseAnthropicBeta
)

type TestConversationRequest struct {
	AccountID int                    `json:"account_id"`
	TokenID   int                    `json:"token_id"`
	Model     string                 `json:"model"`
	Mode      TestConversationMode   `json:"mode"`
	Greeting  string                 `json:"greeting"`
	Client    TestConversationClient `json:"client,omitempty"`
}

type TestConversationResult struct {
	Model      string         `json:"model"`
	Mode       string         `json:"mode"`
	Greeting   string         `json:"greeting"`
	Reply      string         `json:"reply"`
	DurationMS int64          `json:"duration_ms"`
	Images     []TestImage    `json:"images,omitempty"`
	Raw        map[string]any `json:"raw,omitempty"`
}

type TestImage struct {
	URL           string `json:"url,omitempty"`
	B64JSON       string `json:"b64_json,omitempty"`
	MimeType      string `json:"mime_type,omitempty"`
	RevisedPrompt string `json:"revised_prompt,omitempty"`
}

type TestConversationStreamEvent struct {
	Type   string                  `json:"type"`
	Delta  string                  `json:"delta,omitempty"`
	Result *TestConversationResult `json:"result,omitempty"`
	Error  string                  `json:"error,omitempty"`
}

type TestConversationStreamEmit func(TestConversationStreamEvent) error

func TestConversation(ctx context.Context, req TestConversationRequest) (*TestConversationResult, error) {
	return testConversation(ctx, req, nil)
}

func TestConversationStream(ctx context.Context, req TestConversationRequest, emit TestConversationStreamEmit) (*TestConversationResult, error) {
	return testConversation(ctx, req, emit)
}

func testConversation(ctx context.Context, req TestConversationRequest, emit TestConversationStreamEmit) (*TestConversationResult, error) {
	if req.AccountID <= 0 {
		return nil, fmt.Errorf("account id is required")
	}
	if req.TokenID <= 0 {
		return nil, fmt.Errorf("api key is required")
	}
	modelName := strings.TrimSpace(req.Model)
	if modelName == "" {
		return nil, fmt.Errorf("model is required")
	}
	greeting := strings.TrimSpace(req.Greeting)
	if greeting == "" {
		greeting = "hi"
	}
	mode := normalizeTestConversationMode(req.Mode)
	client := normalizeTestConversationClient(req.Client)
	if client == TestConversationClientCodex {
		mode = TestConversationModeOpenAIResponse
	}
	if client == TestConversationClientClaude {
		mode = TestConversationModeAnthropic
	}

	siteRecord, account, token, err := testConversationTarget(ctx, req.AccountID, req.TokenID)
	if err != nil {
		return nil, err
	}
	if !account.Enabled {
		return nil, fmt.Errorf("site account is disabled")
	}
	if !token.Enabled || !model.IsReadySiteToken(*token) || model.IsMaskedSiteTokenValue(token.Token) {
		return nil, fmt.Errorf("api key is not ready")
	}

	context1M := testConversationContext1M(account, token, modelName)
	requestURL, body, headers := buildTestConversationRequest(siteRecord, *token, modelName, mode, greeting, client, context1M)
	if emit != nil {
		if err := emit(TestConversationStreamEvent{Type: "start"}); err != nil {
			return nil, err
		}
	}
	start := time.Now()
	payload, err := requestTestConversation(ctx, siteRecord, requestURL, body, headers, mode, client, account, emit)
	duration := time.Since(start)
	recordTestConversationRelayLog(ctx, siteRecord, account, token, modelName, mode, client, greeting, requestURL, body, headers, payload, duration, err)
	if err != nil {
		return nil, err
	}

	result := &TestConversationResult{
		Model:      modelName,
		Mode:       string(mode),
		Greeting:   greeting,
		Reply:      extractTestConversationReply(mode, payload),
		DurationMS: duration.Milliseconds(),
		Images:     extractTestConversationImages(mode, payload),
		Raw:        payload,
	}
	if emit != nil {
		if err := emit(TestConversationStreamEvent{Type: "done", Result: result}); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func recordTestConversationRelayLog(
	ctx context.Context,
	siteRecord *model.Site,
	account *model.SiteAccount,
	token *model.SiteToken,
	modelName string,
	mode TestConversationMode,
	client TestConversationClient,
	greeting string,
	requestURL string,
	body map[string]any,
	headers map[string]string,
	payload map[string]any,
	duration time.Duration,
	requestErr error,
) {
	if siteRecord == nil || account == nil || token == nil {
		return
	}

	requestForLog := map[string]any{
		"type":     "site_test_conversation",
		"site":     siteRecord.Name,
		"account":  account.Name,
		"api_key":  testConversationKeyName(token),
		"mode":     string(mode),
		"client":   testConversationClientName(client),
		"greeting": greeting,
		"url":      requestURL,
		"headers":  buildTestConversationLogHeaders(siteRecord, headers),
		"body":     body,
	}
	requestContent, _ := json.Marshal(requestForLog)

	responseContent := []byte(nil)
	if payload != nil {
		responseContent, _ = json.Marshal(payload)
	}

	channelName := testConversationChannelName(siteRecord, account, token)
	status := model.AttemptSuccess
	errorText := ""
	if requestErr != nil {
		status = model.AttemptFailed
		errorText = requestErr.Error()
		if len(responseContent) == 0 {
			responseContent, _ = json.Marshal(map[string]any{"error": errorText})
		}
	}

	relayLog := model.RelayLog{
		Time:              time.Now().Unix(),
		RequestModelName:  modelName,
		RequestAPIKeyName: testConversationKeyName(token),
		ChannelName:       channelName,
		ActualModelName:   modelName,
		UseTime:           int(duration.Milliseconds()),
		RequestContent:    string(requestContent),
		ResponseContent:   string(responseContent),
		Error:             errorText,
		Success:           requestErr == nil,
		Attempts: []model.ChannelAttempt{
			{
				ChannelKeyID: token.ID,
				ChannelName:  channelName,
				ModelName:    modelName,
				AttemptNum:   1,
				Status:       status,
				Duration:     int(duration.Milliseconds()),
				Msg:          errorText,
			},
		},
		TotalAttempts: 1,
	}

	if err := op.RelayLogAdd(ctx, relayLog); err != nil {
		log.Warnf("failed to save site test conversation relay log: %v", err)
	}
}

func testConversationKeyName(token *model.SiteToken) string {
	if token == nil {
		return ""
	}
	return firstNonEmptyString(token.Name, token.GroupName, fmt.Sprintf("Key %d", token.ID))
}

func testConversationChannelName(siteRecord *model.Site, account *model.SiteAccount, token *model.SiteToken) string {
	parts := []string{"Site Test Conversation"}
	if siteRecord != nil && strings.TrimSpace(siteRecord.Name) != "" {
		parts = append(parts, siteRecord.Name)
	}
	if account != nil && strings.TrimSpace(account.Name) != "" {
		parts = append(parts, account.Name)
	}
	if keyName := testConversationKeyName(token); keyName != "" {
		parts = append(parts, keyName)
	}
	return strings.Join(parts, " / ")
}

func testConversationTarget(ctx context.Context, accountID int, tokenID int) (*model.Site, *model.SiteAccount, *model.SiteToken, error) {
	account, err := op.SiteAccountGet(accountID, ctx)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("site account not found")
	}
	siteRecord, err := op.SiteGet(account.SiteID, ctx)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("site not found")
	}
	var accountInSite *model.SiteAccount
	for i := range siteRecord.Accounts {
		if siteRecord.Accounts[i].ID == accountID {
			accountInSite = &siteRecord.Accounts[i]
			break
		}
	}
	if accountInSite == nil {
		accountInSite = account
	}
	for i := range accountInSite.Tokens {
		if accountInSite.Tokens[i].ID == tokenID {
			if shouldRejectAccountCredentialForTestConversation(siteRecord.Platform, accountInSite.Tokens[i]) {
				return nil, nil, nil, fmt.Errorf("api key is an account credential, please select a synced site key")
			}
			return siteRecord, accountInSite, &accountInSite.Tokens[i], nil
		}
	}
	return nil, nil, nil, fmt.Errorf("api key not found")
}

func shouldRejectAccountCredentialForTestConversation(platform model.SitePlatform, token model.SiteToken) bool {
	if strings.TrimSpace(token.Source) != "account" {
		return false
	}
	switch platform {
	case model.SitePlatformAPI, model.SitePlatformDeepSeek:
		return false
	default:
		return true
	}
}

func normalizeTestConversationMode(mode TestConversationMode) TestConversationMode {
	switch mode {
	case TestConversationModeOpenAIResponse, TestConversationModeOpenAIImage, TestConversationModeAnthropic:
		return mode
	default:
		return TestConversationModeOpenAIChat
	}
}

func normalizeTestConversationClient(client TestConversationClient) TestConversationClient {
	switch client {
	case TestConversationClientCodex, TestConversationClientClaude:
		return client
	default:
		return TestConversationClientDefault
	}
}

func testConversationClientName(client TestConversationClient) string {
	switch client {
	case TestConversationClientCodex:
		return "codex"
	case TestConversationClientClaude:
		return "claude"
	default:
		return "default"
	}
}

func testConversationContext1M(account *model.SiteAccount, token *model.SiteToken, modelName string) bool {
	if account == nil || token == nil {
		return false
	}
	targetModel := strings.TrimSpace(modelName)
	if targetModel == "" {
		return false
	}
	targetGroup := model.NormalizeSiteGroupKey(token.GroupKey)
	for _, item := range account.Models {
		if strings.TrimSpace(item.ModelName) == targetModel && model.NormalizeSiteGroupKey(item.GroupKey) == targetGroup {
			return item.Context1M
		}
	}
	for _, item := range account.Models {
		if strings.TrimSpace(item.ModelName) == targetModel && model.NormalizeSiteGroupKey(item.GroupKey) == model.SiteDefaultGroupKey {
			return item.Context1M
		}
	}
	return false
}

func buildTestConversationRequest(siteRecord *model.Site, token model.SiteToken, modelName string, mode TestConversationMode, greeting string, client TestConversationClient, context1M bool) (string, map[string]any, map[string]string) {
	key := model.NormalizeSiteSyncTokenValueForPlatform(siteRecord.Platform, token.Token)
	if client == TestConversationClientCodex {
		baseURL := testConversationBaseURL(siteRecord, model.SiteModelRouteTypeOpenAIResponse)
		sessionID, turnID := newCodexTestConversationIDs()
		installationID := newCodexLikeUUID()
		turnMetadata := buildCodexTurnMetadata(sessionID, turnID, installationID)
		return buildSiteURL(baseURL, "/responses"),
			buildCodexTestConversationBody(modelName, greeting, sessionID, turnID, installationID, turnMetadata),
			map[string]string{
				"Authorization":         ensureBearer(key),
				"Accept":                "text/event-stream",
				"Originator":            codexmode.Originator,
				"Session-Id":            sessionID,
				"Thread-Id":             sessionID,
				"User-Agent":            codexmode.UserAgent,
				"X-Client-Request-Id":   sessionID,
				"X-Codex-Beta-Features": codexmode.BetaFeatures,
				"X-Codex-Turn-Metadata": turnMetadata,
				"X-Codex-Window-Id":     sessionID + ":0",
			}
	}
	if client == TestConversationClientClaude {
		baseURL := testConversationBaseURL(siteRecord, model.SiteModelRouteTypeAnthropic)
		sessionID := newCodexLikeUUID()
		return buildSiteURL(baseURL, "/messages?beta=true"),
			buildClaudeTestConversationBody(modelName, greeting, sessionID),
			map[string]string{
				"X-API-Key":         key,
				"Accept":            "application/json",
				"Anthropic-Version": "2023-06-01",
				"anthropic-beta":    claudemode.AnthropicBeta(context1M),
				"Anthropic-Dangerous-Direct-Browser-Access": "true",
				"User-Agent":                  claudeTestConversationUserAgent,
				"X-App":                       "cli",
				"X-Claude-Code-Session-Id":    sessionID,
				"X-Stainless-Retry-Count":     "0",
				"X-Stainless-Timeout":         "600",
				"X-Stainless-Lang":            "js",
				"X-Stainless-Package-Version": "0.74.0",
				"X-Stainless-OS":              "MacOS",
				"X-Stainless-Arch":            "x64",
				"X-Stainless-Runtime":         "node",
				"X-Stainless-Runtime-Version": "v22.21.0",
			}
	}
	switch mode {
	case TestConversationModeOpenAIImage:
		baseURL := testConversationBaseURL(siteRecord, model.SiteModelRouteTypeOpenAIImage)
		return buildSiteURL(baseURL, "/images/generations"),
			map[string]any{
				"model":  modelName,
				"prompt": greeting,
				"n":      1,
				"size":   "1024x1024",
			},
			map[string]string{
				"Authorization": ensureBearer(key),
				"Accept":        "application/json",
			}
	case TestConversationModeOpenAIResponse:
		baseURL := testConversationBaseURL(siteRecord, model.SiteModelRouteTypeOpenAIResponse)
		return buildSiteURL(baseURL, "/responses"),
			map[string]any{
				"model":  modelName,
				"input":  greeting,
				"stream": true,
			},
			map[string]string{
				"Authorization": ensureBearer(key),
				"Accept":        "text/event-stream",
			}
	case TestConversationModeAnthropic:
		baseURL := testConversationBaseURL(siteRecord, model.SiteModelRouteTypeAnthropic)
		headers := map[string]string{
			"X-API-Key":         key,
			"Anthropic-Version": "2023-06-01",
		}
		if siteRecord.Platform != model.SitePlatformAPI {
			headers["Authorization"] = ensureBearer(key)
		}
		return buildSiteURL(baseURL, "/messages"),
			map[string]any{
				"model":      modelName,
				"max_tokens": 512,
				"messages": []map[string]string{
					{"role": "user", "content": greeting},
				},
			},
			headers
	default:
		baseURL := testConversationBaseURL(siteRecord, model.SiteModelRouteTypeOpenAIChat)
		return buildSiteURL(baseURL, "/chat/completions"),
			map[string]any{
				"model":  modelName,
				"stream": true,
				"messages": []map[string]string{
					{"role": "user", "content": greeting},
				},
			},
			map[string]string{
				"Authorization": ensureBearer(key),
				"Accept":        "text/event-stream",
			}
	}
}

func requestTestConversation(ctx context.Context, siteRecord *model.Site, requestURL string, body map[string]any, headers map[string]string, mode TestConversationMode, client TestConversationClient, account *model.SiteAccount, emit TestConversationStreamEmit) (map[string]any, error) {
	if client == TestConversationClientCodex {
		return requestCodexTestConversationStream(ctx, siteRecord, requestURL, body, headers, account, emit)
	}
	if client == TestConversationClientClaude {
		return requestClaudeTestConversationStream(ctx, siteRecord, requestURL, body, headers, account, emit)
	}
	if mode == TestConversationModeOpenAIImage {
		payload, err := requestJSON(ctx, siteRecord, http.MethodPost, requestURL, body, headers, account)
		if err == nil {
			emitTestConversationReply(mode, payload, emit)
		}
		return payload, err
	}
	if mode == TestConversationModeOpenAIResponse && testConversationBodyStream(body) {
		return requestCodexTestConversationStream(ctx, siteRecord, requestURL, body, headers, account, emit)
	}
	if mode == TestConversationModeOpenAIChat && testConversationBodyStream(body) {
		return requestOpenAIChatTestConversationStream(ctx, siteRecord, requestURL, body, headers, account, emit)
	}
	payload, err := requestJSON(ctx, siteRecord, http.MethodPost, requestURL, body, headers, account)
	if err == nil {
		emitTestConversationReply(mode, payload, emit)
		return payload, nil
	}
	if isStreamRequiredError(err) && !testConversationBodyStream(body) {
		retryBody := cloneTestConversationBody(body)
		retryBody["stream"] = true
		retryHeaders := cloneTestConversationHeaders(headers)
		if _, ok := retryHeaders["Accept"]; !ok {
			retryHeaders["Accept"] = "text/event-stream"
		}
		if mode == TestConversationModeOpenAIResponse {
			return requestCodexTestConversationStream(ctx, siteRecord, requestURL, retryBody, retryHeaders, account, emit)
		}
		return requestOpenAIChatTestConversationStream(ctx, siteRecord, requestURL, retryBody, retryHeaders, account, emit)
	}
	return payload, err
}

func buildClaudeTestConversationBody(modelName string, greeting string, sessionID string) map[string]any {
	return map[string]any{
		"model":      modelName,
		"max_tokens": 32000,
		"stream":     true,
		"messages": []map[string]any{
			{
				"role": "user",
				"content": []map[string]any{
					{
						"type": "text",
						"text": greeting,
						"cache_control": map[string]string{
							"type": "ephemeral",
						},
					},
				},
			},
		},
		"system": []map[string]any{
			{"type": "text", "text": "x-anthropic-billing-header: cc_version=2.1.89.4fa; cc_entrypoint=sdk-cli; cch=00000;"},
			{"type": "text", "text": "You are a Claude agent, built on Anthropic's Claude Agent SDK.", "cache_control": map[string]string{"type": "ephemeral"}},
		},
		"thinking": map[string]any{
			"type":          "enabled",
			"budget_tokens": 31999,
		},
		"context_management": map[string]any{
			"edits": []map[string]string{
				{"type": "clear_thinking_20251015", "keep": "all"},
			},
		},
		"metadata": map[string]string{
			"user_id": fmt.Sprintf(`{"device_id":"%s","account_uuid":"","session_id":"%s"}`, strings.ReplaceAll(sessionID, "-", ""), sessionID),
		},
	}
}

func buildCodexTestConversationBody(modelName string, greeting string, sessionID string, turnID string, installationID string, turnMetadata string) map[string]any {
	return map[string]any{
		"model":        modelName,
		"instructions": codexTestConversationInstructions(greeting),
		"input": []map[string]any{
			{
				"type": "message",
				"role": "user",
				"content": []map[string]string{
					{"type": "input_text", "text": greeting},
				},
			},
		},
		"tools":               codexTestConversationTools(),
		"tool_choice":         "auto",
		"parallel_tool_calls": true,
		"reasoning":           map[string]any{"effort": "high"},
		"store":               false,
		"stream":              true,
		"include":             []string{"reasoning.encrypted_content"},
		"prompt_cache_key":    sessionID,
		"text":                map[string]any{"verbosity": "low"},
		"client_metadata": map[string]string{
			"session_id":              sessionID,
			"thread_id":               sessionID,
			"turn_id":                 turnID,
			"x-codex-installation-id": installationID,
			"x-codex-turn-metadata":   turnMetadata,
			"x-codex-window-id":       sessionID + ":0",
		},
	}
}

func codexTestConversationInstructions(greeting string) string {
	return fmt.Sprintf(
		"You are Codex. This is an Octopus connectivity test. Reply directly to the user's greeting %q with one short plain-text sentence. Do not call tools.",
		greeting,
	)
}

func codexTestConversationTools() []map[string]any {
	return []map[string]any{
		codexFunctionTool("shell_command", "Runs a Powershell command (Windows) and returns its output."),
		codexFunctionTool("update_plan", "Updates the task plan."),
		codexFunctionTool("request_user_input", "Request user input for one to three short questions and wait for the response."),
		{
			"type":        "custom",
			"name":        "apply_patch",
			"description": "Use the apply_patch tool to edit files.",
			"format": map[string]any{
				"type":       "grammar",
				"syntax":     "lark",
				"definition": "start: /(.|\\n)*/",
			},
		},
		codexFunctionTool("view_image", "View a local image file from the filesystem when visual inspection is needed."),
		codexFunctionTool("get_goal", "Get the current goal for this thread."),
		codexFunctionTool("create_goal", "Create a goal only when explicitly requested by the user or system/developer instructions."),
		codexFunctionTool("update_goal", "Update the existing goal."),
		{"type": "web_search"},
	}
}

func codexFunctionTool(name string, description string) map[string]any {
	return map[string]any{
		"type":        "function",
		"name":        name,
		"description": description,
		"strict":      false,
		"parameters": map[string]any{
			"type":                 "object",
			"properties":           map[string]any{},
			"additionalProperties": true,
		},
	}
}

func newCodexTestConversationIDs() (string, string) {
	return newCodexLikeUUID(), newCodexLikeUUID()
}

func newCodexLikeUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("00000000-0000-4000-8000-%012x", time.Now().UnixNano()&0xffffffffffff)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func buildCodexTurnMetadata(sessionID string, turnID string, installationID string) string {
	payload := map[string]any{
		"installation_id":         installationID,
		"session_id":              sessionID,
		"thread_id":               sessionID,
		"turn_id":                 turnID,
		"window_id":               sessionID + ":0",
		"request_kind":            "turn",
		"thread_source":           "user",
		"sandbox":                 codexmode.Sandbox,
		"turn_started_at_unix_ms": time.Now().UnixMilli(),
	}
	data, _ := json.Marshal(payload)
	return string(data)
}

func requestCodexTestConversationStream(ctx context.Context, siteRecord *model.Site, requestURL string, body map[string]any, headers map[string]string, account *model.SiteAccount, emit TestConversationStreamEmit) (map[string]any, error) {
	httpClient, err := siteHTTPClient(ctx, siteRecord, account)
	if err != nil {
		return nil, err
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	applyDefaultSiteRequestHeaders(req, true)
	for _, item := range siteRecord.CustomHeader {
		if strings.TrimSpace(item.HeaderKey) != "" {
			req.Header.Set(strings.TrimSpace(item.HeaderKey), item.HeaderValue)
		}
	}
	for key, value := range headers {
		if strings.TrimSpace(key) != "" && strings.TrimSpace(value) != "" {
			req.Header.Set(key, value)
		}
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	if !strings.Contains(contentType, "text/event-stream") {
		bodyBytes, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return nil, readErr
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, formatSiteHTTPError(resp.StatusCode, resp.Header, bodyBytes)
		}
		if len(bodyBytes) == 0 {
			return map[string]any{}, nil
		}
		var responsePayload map[string]any
		if err := json.Unmarshal(bodyBytes, &responsePayload); err != nil {
			return nil, formatSiteDecodeError(resp.Header.Get("Content-Type"), bodyBytes, err)
		}
		if err := emitTestConversationReply(TestConversationModeOpenAIResponse, responsePayload, emit); err != nil {
			return nil, err
		}
		return responsePayload, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, formatSiteHTTPError(resp.StatusCode, resp.Header, bodyBytes)
	}
	return parseCodexTestConversationSSEWithEmit(resp.Body, emit)
}

func parseCodexTestConversationSSE(reader io.Reader) (map[string]any, error) {
	return parseCodexTestConversationSSEWithEmit(reader, nil)
}

func parseCodexTestConversationSSEWithEmit(reader io.Reader, emit TestConversationStreamEmit) (map[string]any, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	dataLines := make([]string, 0)
	eventCount := 0
	eventTypes := make([]string, 0)
	var output strings.Builder
	var completed map[string]any
	outputItems := make([]any, 0)
	appendOutputText := func(text string) error {
		if text != "" {
			output.WriteString(text)
			if err := emitTestConversationDelta(text, emit); err != nil {
				return err
			}
		}
		return nil
	}
	rememberOutputItem := func(index int, value any) {
		item, ok := value.(map[string]any)
		if !ok {
			return
		}
		mergeItem := func(existing any) {
			existingItem, ok := existing.(map[string]any)
			if !ok {
				return
			}
			if jsonRawString(item["arguments"]) == "" {
				if arguments := jsonRawString(existingItem["arguments"]); arguments != "" {
					item["arguments"] = arguments
				}
			}
		}
		if index >= 0 {
			for len(outputItems) <= index {
				outputItems = append(outputItems, nil)
			}
			mergeItem(outputItems[index])
			outputItems[index] = item
			return
		}
		itemID := jsonString(item["id"])
		if itemID != "" {
			for i, existing := range outputItems {
				if existingItem, ok := existing.(map[string]any); ok && jsonString(existingItem["id"]) == itemID {
					mergeItem(existing)
					outputItems[i] = item
					return
				}
			}
		}
		outputItems = append(outputItems, item)
	}
	findOutputItem := func(event map[string]any) map[string]any {
		if index, ok := jsonInt(event["output_index"]); ok && index >= 0 && index < len(outputItems) {
			if item, ok := outputItems[index].(map[string]any); ok {
				return item
			}
		}
		itemID := jsonString(event["item_id"])
		if itemID != "" {
			for _, existing := range outputItems {
				if item, ok := existing.(map[string]any); ok && jsonString(item["id"]) == itemID {
					return item
				}
			}
			item := map[string]any{"id": itemID, "type": "function_call"}
			outputItems = append(outputItems, item)
			return item
		}
		return nil
	}
	flush := func() error {
		if len(dataLines) == 0 {
			return nil
		}
		data := strings.Join(dataLines, "\n")
		dataLines = dataLines[:0]
		data = trimNestedSSEDataPrefix(data)
		if strings.TrimSpace(data) == "[DONE]" {
			return nil
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return err
		}
		eventCount++
		eventType := jsonString(event["type"])
		if eventType != "" {
			eventTypes = append(eventTypes, eventType)
		}
		switch eventType {
		case "response.output_text.delta":
			if err := appendOutputText(jsonString(event["delta"])); err != nil {
				return err
			}
		case "response.output_text.done":
			if output.Len() == 0 {
				if err := appendOutputText(jsonString(event["text"])); err != nil {
					return err
				}
			}
		case "response.content_part.done":
			if output.Len() == 0 {
				if err := appendOutputText(extractResponsesContentPartText(event["part"])); err != nil {
					return err
				}
			}
		case "response.output_item.added":
			index, _ := jsonInt(event["output_index"])
			rememberOutputItem(index, event["item"])
		case "response.output_item.done":
			index, _ := jsonInt(event["output_index"])
			rememberOutputItem(index, event["item"])
			if output.Len() == 0 {
				if err := appendOutputText(extractResponsesOutputItemText(event["item"])); err != nil {
					return err
				}
			}
		case "response.function_call_arguments.delta":
			if item := findOutputItem(event); item != nil {
				item["arguments"] = jsonRawString(item["arguments"]) + jsonRawString(event["delta"])
			}
		case "response.function_call_arguments.done":
			if item := findOutputItem(event); item != nil {
				if arguments := jsonRawString(event["arguments"]); arguments != "" {
					item["arguments"] = arguments
				}
			}
		case "response.completed":
			if response, ok := event["response"].(map[string]any); ok {
				completed = response
				if output.Len() == 0 {
					if err := appendOutputText(extractResponsesResponseText(response)); err != nil {
						return err
					}
				}
			}
		case "response.failed", "response.incomplete", "error":
			if message := extractSiteResponseMessage(event); message != "" {
				return fmt.Errorf("%s", message)
			}
			return fmt.Errorf("codex stream returned %s", eventType)
		}
		return nil
	}
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if strings.TrimSpace(line) == "" {
			if err := flush(); err != nil {
				return nil, err
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if err := flush(); err != nil {
		return nil, err
	}
	if completed == nil {
		completed = map[string]any{
			"object": "response",
			"status": "completed",
		}
	}
	if len(outputItems) > 0 {
		if existingOutput, ok := completed["output"].([]any); !ok || len(existingOutput) == 0 {
			completed["output"] = compactResponseOutputItems(outputItems)
		}
	}
	if text := output.String(); text != "" {
		completed["output_text"] = text
	}
	completed["stream"] = true
	completed["stream_event_count"] = eventCount
	completed["stream_event_types"] = eventTypes
	return completed, nil
}

func compactResponseOutputItems(items []any) []any {
	compact := make([]any, 0, len(items))
	for _, item := range items {
		if item != nil {
			compact = append(compact, item)
		}
	}
	return compact
}

func requestClaudeTestConversationStream(ctx context.Context, siteRecord *model.Site, requestURL string, body map[string]any, headers map[string]string, account *model.SiteAccount, emit TestConversationStreamEmit) (map[string]any, error) {
	httpClient, err := siteHTTPClient(ctx, siteRecord, account)
	if err != nil {
		return nil, err
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	applyDefaultSiteRequestHeaders(req, true)
	for _, item := range siteRecord.CustomHeader {
		if strings.TrimSpace(item.HeaderKey) != "" {
			req.Header.Set(strings.TrimSpace(item.HeaderKey), item.HeaderValue)
		}
	}
	for key, value := range headers {
		if strings.TrimSpace(key) != "" && strings.TrimSpace(value) != "" {
			req.Header.Set(key, value)
		}
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	if !strings.Contains(contentType, "text/event-stream") {
		bodyBytes, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return nil, readErr
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, formatSiteHTTPError(resp.StatusCode, resp.Header, bodyBytes)
		}
		if len(bodyBytes) == 0 {
			return map[string]any{}, nil
		}
		var responsePayload map[string]any
		if err := json.Unmarshal(bodyBytes, &responsePayload); err != nil {
			return nil, formatSiteDecodeError(resp.Header.Get("Content-Type"), bodyBytes, err)
		}
		if err := emitTestConversationReply(TestConversationModeAnthropic, responsePayload, emit); err != nil {
			return nil, err
		}
		return responsePayload, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, formatSiteHTTPError(resp.StatusCode, resp.Header, bodyBytes)
	}
	return parseClaudeTestConversationSSEWithEmit(resp.Body, emit)
}

func parseClaudeTestConversationSSE(reader io.Reader) (map[string]any, error) {
	return parseClaudeTestConversationSSEWithEmit(reader, nil)
}

func parseClaudeTestConversationSSEWithEmit(reader io.Reader, emit TestConversationStreamEmit) (map[string]any, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	dataLines := make([]string, 0)
	var output strings.Builder
	var message map[string]any
	flush := func() error {
		if len(dataLines) == 0 {
			return nil
		}
		data := strings.Join(dataLines, "\n")
		dataLines = dataLines[:0]
		var event map[string]any
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return err
		}
		switch jsonString(event["type"]) {
		case "message_start":
			if msg, ok := event["message"].(map[string]any); ok {
				message = msg
			}
		case "content_block_start":
			if block, ok := event["content_block"].(map[string]any); ok && jsonString(block["type"]) == "text" {
				output.WriteString(jsonString(block["text"]))
			}
		case "content_block_delta":
			if delta, ok := event["delta"].(map[string]any); ok && jsonString(delta["type"]) == "text_delta" {
				text := jsonRawString(delta["text"])
				output.WriteString(text)
				if err := emitTestConversationDelta(text, emit); err != nil {
					return err
				}
			}
		case "message_delta":
			if message == nil {
				message = map[string]any{}
			}
			if delta, ok := event["delta"].(map[string]any); ok {
				for key, value := range delta {
					message[key] = value
				}
			}
		case "error":
			if messageText := extractSiteResponseMessage(event); messageText != "" {
				return fmt.Errorf("%s", messageText)
			}
			return fmt.Errorf("claude stream returned error")
		}
		return nil
	}
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if strings.TrimSpace(line) == "" {
			if err := flush(); err != nil {
				return nil, err
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if err := flush(); err != nil {
		return nil, err
	}
	if message == nil {
		message = map[string]any{
			"type": "message",
			"role": "assistant",
		}
	}
	if text := output.String(); text != "" {
		message["content"] = []any{map[string]any{"type": "text", "text": text}}
	}
	return message, nil
}

func extractResponsesResponseText(response map[string]any) string {
	if response == nil {
		return ""
	}
	output, ok := response["output"].([]any)
	if ok {
		parts := make([]string, 0)
		for _, item := range output {
			if text := extractResponsesOutputItemText(item); text != "" {
				parts = append(parts, text)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "")
		}
	}
	if text := jsonString(response["output_text"]); text != "" {
		return text
	}
	return ""
}

func extractResponsesText(value any) string {
	payload, ok := value.(map[string]any)
	if !ok {
		if items, ok := value.([]any); ok {
			parts := make([]string, 0, len(items))
			for _, item := range items {
				if text := firstNonEmptyString(extractResponsesOutputItemText(item), extractResponsesContentPartText(item), extractResponsesText(item)); text != "" {
					parts = append(parts, text)
				}
			}
			return strings.Join(parts, "")
		}
		return ""
	}
	if text := extractResponsesResponseText(payload); text != "" {
		return text
	}
	for _, key := range []string{"response", "data", "result"} {
		if text := extractResponsesText(payload[key]); text != "" {
			return text
		}
	}
	return ""
}

func extractResponsesOutputItemText(value any) string {
	item, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	if text := extractResponsesContentPartText(item); text != "" {
		return text
	}
	switch jsonString(item["type"]) {
	case "function_call":
		return formatResponsesToolCall(jsonString(item["name"]), jsonString(item["arguments"]))
	case "tool_call":
		if function, ok := item["function"].(map[string]any); ok {
			return formatResponsesToolCall(jsonString(function["name"]), jsonString(function["arguments"]))
		}
		return formatResponsesToolCall(jsonString(item["name"]), jsonString(item["arguments"]))
	}
	content, ok := item["content"].([]any)
	if !ok {
		return ""
	}
	parts := make([]string, 0)
	for _, part := range content {
		if text := extractResponsesContentPartText(part); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "")
}

func formatResponsesToolCall(name string, arguments string) string {
	name = strings.TrimSpace(name)
	arguments = strings.TrimSpace(arguments)
	if name == "" && arguments == "" {
		return ""
	}
	if name == "" {
		return "Tool call:\n" + arguments
	}
	if arguments == "" {
		return "Tool call: " + name
	}
	return "Tool call: " + name + "\n" + arguments
}

func extractResponsesContentPartText(value any) string {
	part, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	partType := jsonString(part["type"])
	switch partType {
	case "", "output_text", "text", "refusal":
		return firstNonEmptyString(jsonString(part["text"]), jsonString(part["content"]), jsonString(part["refusal"]))
	default:
		return ""
	}
}

func buildTestConversationLogHeaders(siteRecord *model.Site, requestHeaders map[string]string) map[string]string {
	headers := map[string]string{
		"User-Agent":      anyRouterUserAgent,
		"Accept":          "application/json, text/plain, */*",
		"Accept-Language": "zh-CN,zh;q=0.9,en;q=0.8",
		"Content-Type":    "application/json",
	}
	if siteRecord != nil {
		for _, item := range siteRecord.CustomHeader {
			key := strings.TrimSpace(item.HeaderKey)
			if key != "" {
				headers[key] = sanitizeTestConversationHeaderValue(key, item.HeaderValue)
			}
		}
	}
	for key, value := range requestHeaders {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			headers[key] = sanitizeTestConversationHeaderValue(key, value)
		}
	}
	return headers
}

func sanitizeTestConversationHeaderValue(key string, value string) string {
	lowerKey := strings.ToLower(strings.TrimSpace(key))
	if lowerKey == "authorization" || lowerKey == "x-api-key" || lowerKey == "api-key" || lowerKey == "apikey" {
		return "[redacted]"
	}
	return value
}

func testConversationBaseURL(siteRecord *model.Site, routeType model.SiteModelRouteType) string {
	if siteRecord == nil {
		return ""
	}
	return resolveProjectedChannelBaseURL(siteRecord, routeType)
}

func isStreamRequiredError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "stream_required") || strings.Contains(msg, "requires streaming") || strings.Contains(msg, "set stream=true")
}

func testConversationBodyStream(body map[string]any) bool {
	v, ok := body["stream"]
	if !ok {
		return false
	}
	b, _ := v.(bool)
	return b
}

func cloneTestConversationBody(body map[string]any) map[string]any {
	clone := make(map[string]any, len(body))
	for k, v := range body {
		clone[k] = v
	}
	return clone
}

func cloneTestConversationHeaders(headers map[string]string) map[string]string {
	clone := make(map[string]string, len(headers))
	for k, v := range headers {
		clone[k] = v
	}
	return clone
}

func requestOpenAIChatTestConversationStream(ctx context.Context, siteRecord *model.Site, requestURL string, body map[string]any, headers map[string]string, account *model.SiteAccount, emit TestConversationStreamEmit) (map[string]any, error) {
	httpClient, err := siteHTTPClient(ctx, siteRecord, account)
	if err != nil {
		return nil, err
	}
	payloadBytes, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(payloadBytes))
	if err != nil {
		return nil, err
	}
	applyDefaultSiteRequestHeaders(req, true)
	for _, item := range siteRecord.CustomHeader {
		if strings.TrimSpace(item.HeaderKey) != "" {
			req.Header.Set(strings.TrimSpace(item.HeaderKey), item.HeaderValue)
		}
	}
	for key, value := range headers {
		if strings.TrimSpace(key) != "" && strings.TrimSpace(value) != "" {
			req.Header.Set(key, value)
		}
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.Contains(contentType, "text/event-stream") {
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			bodyBytes, _ := io.ReadAll(resp.Body)
			return nil, formatSiteHTTPError(resp.StatusCode, resp.Header, bodyBytes)
		}
		return parseOpenAIChatTestConversationSSEWithEmit(resp.Body, emit)
	}
	bodyBytes, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, readErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, formatSiteHTTPError(resp.StatusCode, resp.Header, bodyBytes)
	}
	if len(bodyBytes) == 0 {
		return map[string]any{}, nil
	}
	if looksLikeSSEPayload(bodyBytes) {
		return parseOpenAIChatTestConversationSSEWithEmit(bytes.NewReader(bodyBytes), emit)
	}
	var responsePayload map[string]any
	if err := json.Unmarshal(bodyBytes, &responsePayload); err != nil {
		return nil, formatSiteDecodeError(resp.Header.Get("Content-Type"), bodyBytes, err)
	}
	if err := emitTestConversationReply(TestConversationModeOpenAIChat, responsePayload, emit); err != nil {
		return nil, err
	}
	return responsePayload, nil
}

func trimNestedSSEDataPrefix(data string) string {
	for {
		trimmed := strings.TrimSpace(data)
		if !strings.HasPrefix(trimmed, "data:") {
			return data
		}
		data = strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
	}
}

func looksLikeSSEPayload(body []byte) bool {
	trimmed := strings.TrimLeft(string(body), " \t\r\n")
	return strings.HasPrefix(trimmed, "data:") || strings.HasPrefix(trimmed, "event:")
}

func parseOpenAIChatTestConversationSSE(reader io.Reader) (map[string]any, error) {
	return parseOpenAIChatTestConversationSSEWithEmit(reader, nil)
}

func parseOpenAIChatTestConversationSSEWithEmit(reader io.Reader, emit TestConversationStreamEmit) (map[string]any, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	dataLines := make([]string, 0)
	var output strings.Builder
	var lastChunk map[string]any
	flush := func() error {
		if len(dataLines) == 0 {
			return nil
		}
		data := strings.Join(dataLines, "\n")
		dataLines = dataLines[:0]
		data = trimNestedSSEDataPrefix(data)
		if strings.TrimSpace(data) == "[DONE]" {
			return nil
		}
		var chunk map[string]any
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return err
		}
		lastChunk = chunk
		if choices, ok := chunk["choices"].([]any); ok && len(choices) > 0 {
			if choice, ok := choices[0].(map[string]any); ok {
				if delta, ok := choice["delta"].(map[string]any); ok {
					if text, ok := delta["content"].(string); ok {
						output.WriteString(text)
						if err := emitTestConversationDelta(text, emit); err != nil {
							return err
						}
					}
				}
			}
		}
		return nil
	}
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if strings.TrimSpace(line) == "" {
			if err := flush(); err != nil {
				return nil, err
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if err := flush(); err != nil {
		return nil, err
	}
	result := map[string]any{
		"object":  "chat.completion",
		"choices": []any{},
		"stream":  true,
	}
	if text := output.String(); text != "" {
		result["choices"] = []any{map[string]any{
			"index": 0,
			"message": map[string]any{
				"role":    "assistant",
				"content": text,
			},
			"finish_reason": "stop",
		}}
	}
	if lastChunk != nil {
		if id, ok := lastChunk["id"]; ok {
			result["id"] = id
		}
		if m, ok := lastChunk["model"]; ok {
			result["model"] = m
		}
		if created, ok := lastChunk["created"]; ok {
			result["created"] = created
		}
	}
	return result, nil
}

func emitTestConversationDelta(text string, emit TestConversationStreamEmit) error {
	if emit == nil || text == "" {
		return nil
	}
	return emit(TestConversationStreamEvent{Type: "delta", Delta: text})
}

func emitTestConversationReply(mode TestConversationMode, payload map[string]any, emit TestConversationStreamEmit) error {
	if emit == nil {
		return nil
	}
	return emitTestConversationDelta(extractTestConversationReply(mode, payload), emit)
}

func extractTestConversationReply(mode TestConversationMode, payload map[string]any) string {
	switch mode {
	case TestConversationModeOpenAIResponse:
		if text := extractResponsesText(payload); text != "" {
			return text
		}
	case TestConversationModeOpenAIImage:
		if text := extractImagesGenerationSummary(payload); text != "" {
			return text
		}
	case TestConversationModeAnthropic:
		if content, ok := payload["content"].([]any); ok {
			parts := make([]string, 0, len(content))
			for _, item := range content {
				obj, ok := item.(map[string]any)
				if !ok {
					continue
				}
				if text := jsonString(obj["text"]); text != "" {
					parts = append(parts, text)
				}
			}
			return strings.Join(parts, "")
		}
	default:
		if choices, ok := payload["choices"].([]any); ok && len(choices) > 0 {
			if choice, ok := choices[0].(map[string]any); ok {
				if message, ok := choice["message"].(map[string]any); ok {
					if text := jsonString(message["content"]); text != "" {
						return text
					}
					if text := extractResponsesText(message["content"]); text != "" {
						return text
					}
				}
				if text := jsonString(choice["text"]); text != "" {
					return text
				}
			}
		}
	}
	if message := jsonString(nestedValue(payload, "error", "message")); message != "" {
		return message
	}
	return "No text content returned."
}

func extractImagesGenerationSummary(payload map[string]any) string {
	data, ok := payload["data"].([]any)
	if !ok || len(data) == 0 {
		if url := jsonString(payload["url"]); url != "" {
			return "Image URL: " + url
		}
		if b64 := jsonString(payload["b64_json"]); b64 != "" {
			return fmt.Sprintf("Image b64_json returned (%d bytes).", len(b64))
		}
		return ""
	}

	parts := make([]string, 0, len(data))
	for i, item := range data {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		prefix := fmt.Sprintf("Image %d", i+1)
		if url := jsonString(obj["url"]); url != "" {
			parts = append(parts, prefix+": "+url)
			continue
		}
		if b64 := jsonString(obj["b64_json"]); b64 != "" {
			parts = append(parts, fmt.Sprintf("%s: b64_json returned (%d bytes)", prefix, len(b64)))
			continue
		}
		if revisedPrompt := jsonString(obj["revised_prompt"]); revisedPrompt != "" {
			parts = append(parts, prefix+" revised prompt: "+revisedPrompt)
		}
	}
	return strings.Join(parts, "\n")
}

func extractTestConversationImages(mode TestConversationMode, payload map[string]any) []TestImage {
	if mode != TestConversationModeOpenAIImage || payload == nil {
		return nil
	}

	images := make([]TestImage, 0)
	seen := make(map[string]struct{})
	add := func(item TestImage) {
		item.URL = strings.TrimSpace(item.URL)
		item.B64JSON = strings.TrimSpace(item.B64JSON)
		item.MimeType = strings.TrimSpace(item.MimeType)
		item.RevisedPrompt = strings.TrimSpace(item.RevisedPrompt)
		if item.URL == "" && item.B64JSON == "" {
			return
		}
		if item.MimeType == "" {
			item.MimeType = "image/png"
		}
		key := item.URL
		if key == "" {
			key = fmt.Sprintf("%s:%d:%s", item.MimeType, len(item.B64JSON), firstN(item.B64JSON, 64))
		}
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		images = append(images, item)
	}
	addFromMap := func(obj map[string]any) {
		if obj == nil {
			return
		}
		add(TestImage{
			URL:           jsonString(obj["url"]),
			B64JSON:       jsonString(obj["b64_json"]),
			MimeType:      imageMimeTypeFromPayload(obj),
			RevisedPrompt: jsonString(obj["revised_prompt"]),
		})
	}

	addFromMap(payload)
	if data, ok := payload["data"].([]any); ok {
		for _, item := range data {
			if obj, ok := item.(map[string]any); ok {
				addFromMap(obj)
			}
		}
	}
	if data, ok := payload["data"].(map[string]any); ok {
		addFromMap(data)
		if nested, ok := data["data"].([]any); ok {
			for _, item := range nested {
				if obj, ok := item.(map[string]any); ok {
					addFromMap(obj)
				}
			}
		}
	}
	return images
}

func imageMimeTypeFromPayload(payload map[string]any) string {
	raw := firstNonEmptyString(jsonString(payload["mime_type"]), jsonString(payload["mimeType"]))
	if strings.HasPrefix(raw, "image/") {
		return raw
	}
	format := strings.ToLower(firstNonEmptyString(jsonString(payload["output_format"]), jsonString(payload["format"])))
	switch format {
	case "jpg", "jpeg":
		return "image/jpeg"
	case "webp":
		return "image/webp"
	case "gif":
		return "image/gif"
	default:
		return "image/png"
	}
}

func firstN(value string, n int) string {
	if n <= 0 || len(value) <= n {
		return value
	}
	return value[:n]
}
