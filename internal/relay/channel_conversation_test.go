package relay

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/U188/octopus/internal/model"
	"github.com/U188/octopus/internal/transformer/outbound"
)

func TestChannelConversationUsesChannelConfiguration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" || r.Header.Get("X-Test") != "configured" {
			t.Fatalf("channel headers were not applied: %#v", r.Header)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["model"] != "gpt-test" || body["temperature"] != 0.25 {
			t.Fatalf("unexpected request body: %#v", body)
		}
		if body["stream"] != true {
			t.Fatalf("expected streaming channel test, got %#v", body["stream"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"test","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"pong"}}]}`))
	}))
	defer server.Close()

	override := `{"temperature":0.25}`
	channel := &model.Channel{
		ID:            1,
		Name:          "manual",
		Type:          outbound.OutboundTypeOpenAIChat,
		Enabled:       true,
		BaseUrls:      []model.BaseUrl{{URL: server.URL}},
		Keys:          []model.ChannelKey{{ID: 1, Enabled: true, ChannelKey: "test-key"}},
		Model:         "gpt-test",
		CustomHeader:  []model.CustomHeader{{HeaderKey: "X-Test", HeaderValue: "configured"}},
		ParamOverride: &override,
	}

	result, err := TestChannelConversation(context.Background(), channel, "gpt-test", "ping")
	if err != nil {
		t.Fatal(err)
	}
	if result.Reply != "pong" || result.Greeting != "ping" || result.Model != "gpt-test" {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestChannelConversationCollectsStreamingResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"test\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"po\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"id\":\"test\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ng\"}}]}\n\ndata: [DONE]\n\n"))
	}))
	defer server.Close()

	channel := &model.Channel{
		ID: 1, Name: "manual", Type: outbound.OutboundTypeOpenAIChat, Enabled: true,
		BaseUrls: []model.BaseUrl{{URL: server.URL}},
		Keys:     []model.ChannelKey{{ID: 1, Enabled: true, ChannelKey: "test-key"}},
		Model:    "gpt-test",
	}
	result, err := TestChannelConversation(context.Background(), channel, "gpt-test", "ping")
	if err != nil {
		t.Fatal(err)
	}
	if result.Reply != "pong" {
		t.Fatalf("unexpected streamed reply: %q", result.Reply)
	}
}

func TestChannelConversationRejectsUnconfiguredModel(t *testing.T) {
	channel := &model.Channel{Enabled: true, Type: outbound.OutboundTypeOpenAIChat, Model: "allowed"}
	if _, err := TestChannelConversation(context.Background(), channel, "other", "ping"); err == nil {
		t.Fatal("expected unconfigured model to be rejected")
	}
}
