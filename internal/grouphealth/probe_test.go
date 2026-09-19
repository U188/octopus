package grouphealth

import (
	"context"
	"testing"

	"github.com/U188/octopus/internal/model"
	"github.com/U188/octopus/internal/transformer/outbound"
)

func TestBuildProbeRequestForResponses(t *testing.T) {
	channel := &model.Channel{
		Type:     outbound.OutboundTypeOpenAIResponse,
		BaseUrls: []model.BaseUrl{{URL: "https://example.com/v1"}},
	}
	usedKey := &model.ChannelKey{ID: 1, ChannelKey: "sk-test"}

	req, err := buildProbeRequest(context.Background(), channel, usedKey, "gpt-5.4")
	if err != nil {
		t.Fatalf("buildProbeRequest returned error: %v", err)
	}
	if req.URL.Path != "/v1/responses" {
		t.Fatalf("expected /v1/responses, got %s", req.URL.Path)
	}
}

func TestBuildProbeRequestForEmbeddings(t *testing.T) {
	channel := &model.Channel{
		Type:     outbound.OutboundTypeOpenAIEmbedding,
		BaseUrls: []model.BaseUrl{{URL: "https://example.com/v1"}},
	}
	usedKey := &model.ChannelKey{ID: 1, ChannelKey: "sk-test"}

	req, err := buildProbeRequest(context.Background(), channel, usedKey, "text-embedding-3-large")
	if err != nil {
		t.Fatalf("buildProbeRequest returned error: %v", err)
	}
	if req.URL.Path != "/v1/embeddings" {
		t.Fatalf("expected /v1/embeddings, got %s", req.URL.Path)
	}
}

func TestBuildProbeRequestNoAuth(t *testing.T) {
	channel := &model.Channel{Type: outbound.OutboundTypeOpenAIChat, NoAuth: true, BaseUrls: []model.BaseUrl{{URL: "https://example.com/gemini/v1"}}}
	req, err := buildProbeRequest(context.Background(), channel, &model.ChannelKey{}, "gemini-3.6-flash")
	if err != nil {
		t.Fatal(err)
	}
	channel.StripUpstreamAuth(req.Header)
	if req.URL.Path != "/gemini/v1/chat/completions" || len(req.Header.Values("Authorization")) != 0 {
		t.Fatalf("unexpected probe request %s headers=%v", req.URL, req.Header)
	}
}
