package sitesync

import (
	"testing"

	"github.com/U188/octopus/internal/model"
)

func TestDropTokensDuplicatingAccountCredentials(t *testing.T) {
	existing := []model.SiteToken{
		{ID: 1, Token: "sk-account", Source: "account"},
		{ID: 2, Token: "sk-old", Source: "sync"},
	}
	synced := []model.SiteToken{
		{Token: "sk-account", Source: "sync"},
		{Token: "sk-other", Source: "sync"},
	}

	filtered := dropTokensDuplicatingAccountCredentials(existing, synced)
	if len(filtered) != 1 || filtered[0].Token != "sk-other" {
		t.Fatalf("unexpected filtered tokens: %#v", filtered)
	}
}

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

func TestBuildSub2APITokensSkipsInactiveNestedGroup(t *testing.T) {
	tokens := buildSub2APITokensFromItems([]map[string]any{
		{
			"name":     "free",
			"key":      "sk-free",
			"group_id": 14,
			"group": map[string]any{
				"id":     14,
				"name":   "免费分组",
				"status": "inactive",
			},
		},
		{
			"name":     "pro",
			"key":      "sk-pro",
			"group_id": 2,
			"group": map[string]any{
				"id":     2,
				"name":   "纯Pro号池",
				"status": "active",
			},
		},
	})

	if len(tokens) != 1 {
		t.Fatalf("expected only active group token, got %+v", tokens)
	}
	if tokens[0].GroupKey != "2" || tokens[0].Token != "sk-pro" {
		t.Fatalf("unexpected active group token: %+v", tokens[0])
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

func TestAddAccountAPIKeyForMissingGroupsDoesNotFanOutAccountKey(t *testing.T) {
	tokens := []model.SiteToken{
		{
			Name:        "ready",
			Token:       "ready-chat-key",
			GroupKey:    "default",
			GroupName:   "default",
			Enabled:     true,
			ValueStatus: model.SiteTokenValueStatusReady,
			Source:      "sync",
		},
		{
			Name:        "masked",
			Token:       "sk-****1234",
			GroupKey:    "vip",
			GroupName:   "VIP",
			Enabled:     false,
			ValueStatus: model.SiteTokenValueStatusMaskedPending,
			Source:      "sync",
		},
	}
	groups := []model.SiteUserGroup{
		{GroupKey: "default", Name: "default"},
		{GroupKey: "vip", Name: "VIP"},
		{GroupKey: "svip", Name: "SVIP"},
	}

	completed := addAccountAPIKeyForMissingGroups(tokens, groups, "sk-account")

	if len(completed) != len(tokens) {
		t.Fatalf("expected account key not to be copied into unrelated groups, got %+v", completed)
	}
	for _, token := range completed {
		if token.Source == siteTokenSourceAccountFallback {
			t.Fatalf("did not expect account fallback token, got %+v", completed)
		}
	}
}

func TestConstrainTokensToAvailableGroupsDropsTokenOnlyDeletedGroup(t *testing.T) {
	tokens := []model.SiteToken{
		{Name: "free", Token: "sk-free", GroupKey: "14", GroupName: "免费分组", Enabled: true},
		{Name: "pro", Token: "sk-pro", GroupKey: "2", GroupName: "纯Pro号池", Enabled: true},
	}
	groups := []model.SiteUserGroup{
		{GroupKey: "2", Name: "纯Pro号池"},
		{GroupKey: "4", Name: "纯Plus号池"},
	}

	filtered := constrainTokensToAvailableGroups(tokens, groups)

	if len(filtered) != 1 {
		t.Fatalf("expected token-only deleted group to be dropped, got %+v", filtered)
	}
	if filtered[0].GroupKey != "2" {
		t.Fatalf("expected available group token to remain, got %+v", filtered[0])
	}
}

func TestCompleteMaskedTokensFromExistingReadyTokensMatchesMovedGroup(t *testing.T) {
	tokens := []model.SiteToken{{
		Name:        "me",
		Token:       "sk-abc**********wxyz",
		GroupKey:    "new",
		GroupName:   "New",
		Enabled:     true,
		ValueStatus: model.SiteTokenValueStatusMaskedPending,
		Source:      "sync",
	}}
	existing := []model.SiteToken{{
		ID:          7,
		Name:        "me",
		Token:       "sk-abcREALVALUEwxyz",
		GroupKey:    "old",
		GroupName:   "Old",
		Enabled:     true,
		ValueStatus: model.SiteTokenValueStatusReady,
		Source:      "manual",
	}}

	completed := completeMaskedTokensFromExistingReadyTokens(tokens, existing)

	if completed[0].Token != "sk-abcREALVALUEwxyz" {
		t.Fatalf("expected moved masked token to be completed from old group key, got %+v", completed[0])
	}
	if completed[0].ValueStatus != model.SiteTokenValueStatusReady {
		t.Fatalf("expected moved token to become ready, got %+v", completed[0])
	}
}
