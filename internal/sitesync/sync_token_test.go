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
	account.Tokens = []model.SiteToken{
		{Token: "cached-chat-key", Enabled: true, ValueStatus: model.SiteTokenValueStatusReady, GroupKey: model.SiteDefaultGroupKey, IsDefault: true},
	}
	if got := resolveDirectToken(account); got != "cached-chat-key" {
		t.Fatalf("expected cached ready site token, got %q", got)
	}

	account.Tokens = nil
	if got := resolveDirectToken(account); got != "" {
		t.Fatalf("expected empty direct token without api key or cached token, got %q", got)
	}
}

func TestResolveDirectTokenSkipsAccessTokenAndMaskedTokens(t *testing.T) {
	account := &model.SiteAccount{
		CredentialType: model.SiteCredentialTypeAccessToken,
		AccessToken:    "session-token",
		Tokens: []model.SiteToken{
			{Token: "sk-****1234", Enabled: true, ValueStatus: model.SiteTokenValueStatusMaskedPending, GroupKey: model.SiteDefaultGroupKey, IsDefault: true},
			{Token: "ready-chat-key", Enabled: true, ValueStatus: model.SiteTokenValueStatusReady, GroupKey: "vip"},
		},
	}

	if got := resolveDirectToken(account); got != "ready-chat-key" {
		t.Fatalf("expected ready cached site token and not access token, got %q", got)
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

func TestCompleteMaskedTokensFromAccountAPIKeyRestoresMatchingToken(t *testing.T) {
	tokens := []model.SiteToken{
		{
			Name:        "me",
			Token:       "SHPk**********30T0",
			GroupKey:    "Free",
			Enabled:     true,
			ValueStatus: model.SiteTokenValueStatusMaskedPending,
		},
	}

	completed := completeMaskedTokensFromAccountAPIKey(tokens, "sk-SHPk123456789030T0")

	if completed[0].Token != "sk-SHPk123456789030T0" {
		t.Fatalf("expected masked token to be restored from account api key, got %q", completed[0].Token)
	}
	if completed[0].ValueStatus != model.SiteTokenValueStatusReady {
		t.Fatalf("expected restored token to be ready, got %q", completed[0].ValueStatus)
	}
	if tokens[0].Token != "SHPk**********30T0" {
		t.Fatalf("expected input token slice to remain unchanged, got %q", tokens[0].Token)
	}
}

func TestCompleteMaskedTokensFromAccountAPIKeyLeavesNonMatchingTokenPending(t *testing.T) {
	tokens := []model.SiteToken{
		{
			Name:        "other",
			Token:       "SHPk**********30T0",
			GroupKey:    "Free",
			Enabled:     true,
			ValueStatus: model.SiteTokenValueStatusMaskedPending,
		},
	}

	completed := completeMaskedTokensFromAccountAPIKey(tokens, "sk-OTHER12345678930T0")

	if completed[0].Token != "SHPk**********30T0" {
		t.Fatalf("expected nonmatching masked token to remain masked, got %q", completed[0].Token)
	}
	if completed[0].ValueStatus != model.SiteTokenValueStatusMaskedPending {
		t.Fatalf("expected nonmatching masked token to remain pending, got %q", completed[0].ValueStatus)
	}
}

func TestCompleteMaskedTokensFromAccountAPIKeyLeavesReadyTokenUnchanged(t *testing.T) {
	tokens := []model.SiteToken{
		{
			Name:        "ready",
			Token:       "ready-chat-key",
			GroupKey:    "Free",
			Enabled:     true,
			ValueStatus: model.SiteTokenValueStatusReady,
		},
	}

	completed := completeMaskedTokensFromAccountAPIKey(tokens, "sk-SHPk123456789030T0")

	if completed[0].Token != "ready-chat-key" {
		t.Fatalf("expected ready token to remain unchanged, got %q", completed[0].Token)
	}
	if completed[0].ValueStatus != model.SiteTokenValueStatusReady {
		t.Fatalf("expected ready token to stay ready, got %q", completed[0].ValueStatus)
	}
}
