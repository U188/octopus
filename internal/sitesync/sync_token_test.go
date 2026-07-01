package sitesync

import (
	"testing"

	"github.com/U188/octopus/internal/model"
)

func TestResolveDirectTokenUsesAPIKeyOnly(t *testing.T) {
	account := &model.SiteAccount{
		CredentialType: model.SiteCredentialTypeAccessToken,
		AccessToken:    "session-token",
		APIKey:         "chat-api-key",
	}

	if got := resolveDirectToken(account); got != "chat-api-key" {
		t.Fatalf("expected api key for direct token, got %q", got)
	}

	account.APIKey = ""
	if got := resolveDirectToken(account); got != "" {
		t.Fatalf("expected empty direct token without api key, got %q", got)
	}
}

func TestBuildSub2APITokensIgnoresAccessTokenFields(t *testing.T) {
	tokens := buildSub2APITokensFromItems([]map[string]any{
		{
			"name":         "session",
			"access_token": "session-token",
			"group_id":     "default",
		},
		{
			"name":     "api",
			"api_key":  "chat-api-key",
			"group_id": "default",
		},
	})

	if len(tokens) != 1 {
		t.Fatalf("expected only api key token, got %+v", tokens)
	}
	if tokens[0].Token != "chat-api-key" {
		t.Fatalf("expected chat api key token, got %q", tokens[0].Token)
	}
}
