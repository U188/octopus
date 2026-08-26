package relay

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/U188/octopus/internal/helper"
	dbmodel "github.com/U188/octopus/internal/model"
	transformerModel "github.com/U188/octopus/internal/transformer/model"
	"github.com/U188/octopus/internal/transformer/outbound"
)

type ChannelTestConversationResult struct {
	Model      string `json:"model"`
	Greeting   string `json:"greeting"`
	Reply      string `json:"reply"`
	DurationMS int64  `json:"duration_ms"`
}

func TestChannelConversation(ctx context.Context, channel *dbmodel.Channel, modelName, greeting string) (*ChannelTestConversationResult, error) {
	if channel == nil {
		return nil, fmt.Errorf("channel not found")
	}
	if !channel.Enabled {
		return nil, fmt.Errorf("channel is disabled")
	}
	if !outbound.IsChatChannelType(channel.Type) {
		return nil, fmt.Errorf("channel type does not support conversations")
	}
	modelName = strings.TrimSpace(modelName)
	if modelName == "" || !channelHasModel(channel, modelName) {
		return nil, fmt.Errorf("model is not configured for this channel")
	}
	greeting = strings.TrimSpace(greeting)
	if greeting == "" {
		return nil, fmt.Errorf("greeting is required")
	}
	if strings.TrimSpace(channel.GetBaseUrl()) == "" {
		return nil, fmt.Errorf("channel base url is required")
	}
	usedKey := channel.GetChannelKey()
	if strings.TrimSpace(usedKey.ChannelKey) == "" {
		return nil, fmt.Errorf("channel has no enabled api key")
	}

	internalRequest := channelTestRequest(channel.Type, modelName, greeting)
	adapter := outbound.Get(channel.Type)
	request, err := adapter.TransformRequest(ctx, internalRequest, channel.GetBaseUrl(), usedKey.ChannelKey)
	if err != nil {
		return nil, fmt.Errorf("build channel test request: %w", err)
	}
	if err := helper.ApplyParamOverride(request, channel.ParamOverride); err != nil {
		return nil, err
	}
	if err := helper.ApplyResponsesToolDenylist(request, channel.EffectiveResponsesToolDenylist(time.Now().Unix())); err != nil {
		return nil, err
	}

	attempt := &relayAttempt{
		relayRequest: &relayRequest{ctx: ctx, internalRequest: internalRequest, requestModel: modelName},
		outAdapter:   adapter,
		channel:      channel,
		usedKey:      usedKey,
	}
	attempt.copyHeaders(request)
	attempt.applyCodexResponseHeaders(request)
	attempt.applyClaudeAnthropicMode(request)

	startedAt := time.Now()
	response, err := attempt.sendRequest(request)
	if err != nil {
		return nil, fmt.Errorf("send channel test request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(response.Body, maxRelayErrorBodyBytes))
		return nil, fmt.Errorf("upstream error: %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}

	reply, err := decodeChannelTestResponse(ctx, adapter, response)
	if err != nil {
		return nil, fmt.Errorf("decode channel test response: %w", err)
	}
	if reply == "" {
		return nil, fmt.Errorf("upstream returned no text content")
	}
	return &ChannelTestConversationResult{
		Model:      modelName,
		Greeting:   greeting,
		Reply:      reply,
		DurationMS: time.Since(startedAt).Milliseconds(),
	}, nil
}

func channelTestRequest(channelType outbound.OutboundType, modelName, greeting string) *transformerModel.InternalLLMRequest {
	stream := true
	maxTokens := int64(512)
	format := transformerModel.APIFormatOpenAIChatCompletion
	switch channelType {
	case outbound.OutboundTypeOpenAIResponse, outbound.OutboundTypeVolcengine:
		format = transformerModel.APIFormatOpenAIResponse
	case outbound.OutboundTypeAnthropic:
		format = transformerModel.APIFormatAnthropicMessage
	case outbound.OutboundTypeGemini:
		format = transformerModel.APIFormatGeminiContents
	}
	return &transformerModel.InternalLLMRequest{
		Model:               modelName,
		RawAPIFormat:        format,
		Messages:            []transformerModel.Message{{Role: "user", Content: transformerModel.MessageContent{Content: &greeting}}},
		Stream:              &stream,
		MaxTokens:           &maxTokens,
		MaxCompletionTokens: &maxTokens,
	}
}

func decodeChannelTestResponse(ctx context.Context, adapter transformerModel.Outbound, response *http.Response) (string, error) {
	if !strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "text/event-stream") {
		decoded, err := adapter.TransformResponse(ctx, response)
		if err != nil {
			return "", err
		}
		if decoded.Error != nil {
			return "", decoded.Error
		}
		return channelTestReply(decoded), nil
	}

	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	dataLines := make([]string, 0, 1)
	var reply strings.Builder
	flush := func() error {
		if len(dataLines) == 0 {
			return nil
		}
		data := strings.Join(dataLines, "\n")
		dataLines = dataLines[:0]
		if data == "[DONE]" {
			return nil
		}
		decoded, err := adapter.TransformStream(ctx, []byte(data))
		if err != nil {
			return err
		}
		if decoded.Error != nil {
			return decoded.Error
		}
		reply.WriteString(channelTestReply(decoded))
		return nil
	}
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if line == "" {
			if err := flush(); err != nil {
				return "", err
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	if err := flush(); err != nil {
		return "", err
	}
	return strings.TrimSpace(reply.String()), nil
}

func channelHasModel(channel *dbmodel.Channel, modelName string) bool {
	for _, source := range []string{channel.Model, channel.CustomModel} {
		for _, item := range strings.Split(source, ",") {
			if strings.TrimSpace(item) == modelName {
				return true
			}
		}
	}
	return false
}

func channelTestReply(response *transformerModel.InternalLLMResponse) string {
	if response == nil {
		return ""
	}
	parts := make([]string, 0, len(response.Choices))
	for _, choice := range response.Choices {
		message := choice.Message
		if message == nil {
			message = choice.Delta
		}
		if message == nil {
			continue
		}
		if message.Content.Content != nil {
			parts = append(parts, *message.Content.Content)
		}
		for _, part := range message.Content.MultipleContent {
			if part.Text != nil {
				parts = append(parts, *part.Text)
			}
		}
	}
	return strings.TrimSpace(strings.Join(parts, ""))
}
