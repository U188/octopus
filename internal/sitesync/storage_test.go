package sitesync

import (
	"context"
	"testing"
	"time"

	dbpkg "github.com/U188/octopus/internal/db"
	"github.com/U188/octopus/internal/model"
	"github.com/U188/octopus/internal/op"
)

func TestSiteMaskedTokenMatchesIgnoresOptionalSKPrefix(t *testing.T) {
	tests := []struct {
		name      string
		fullToken string
		masked    string
	}{
		{name: "full has sk prefix", fullToken: "sk-yzFyREALREALOTkb", masked: "yzFy**********OTkb"},
		{name: "masked has sk prefix", fullToken: "yzFyREALREALOTkb", masked: "sk-yzFy**********OTkb"},
		{name: "both have sk prefix", fullToken: "sk-yzFyREALREALOTkb", masked: "sk-yzFy**********OTkb"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !siteMaskedTokenMatches(tt.fullToken, tt.masked) {
				t.Fatalf("expected %q to match %q", tt.fullToken, tt.masked)
			}
		})
	}
}

func TestApplyPersistedRouteStateGuessesLegacyUnknownRoute(t *testing.T) {
	legacyPayload := model.SiteModelRouteMetadata{
		Source:                 "/api/pricing",
		RouteSupported:         false,
		SupportedEndpointTypes: []string{"/vendor/embeddings"},
		UnsupportedReason:      "site reports endpoint types outside current supported route buckets",
	}.Marshal()
	existing := &model.SiteModel{
		ModelName:       "vendor-embedding-x",
		RouteType:       model.SiteModelRouteTypeUnknown,
		RouteSource:     model.SiteModelRouteSourceSyncInferred,
		RouteRawPayload: legacyPayload,
	}
	item := &model.SiteModel{ModelName: "vendor-embedding-x"}

	applyPersistedRouteState(item, existing, time.Unix(1711929600, 0))

	if item.RouteType != model.SiteModelRouteTypeOpenAIEmbedding {
		t.Fatalf("expected legacy unknown route to be guessed as %q, got %q", model.SiteModelRouteTypeOpenAIEmbedding, item.RouteType)
	}
	metadata, ok := model.ParseSiteModelRouteMetadata(item.RouteRawPayload)
	if !ok {
		t.Fatalf("expected guessed route metadata to parse")
	}
	if !metadata.RouteSupported || !metadata.RouteGuessed {
		t.Fatalf("expected guessed route metadata to mark supported name guess, got %+v", metadata)
	}
	if metadata.RouteType != model.SiteModelRouteTypeOpenAIEmbedding {
		t.Fatalf("expected guessed metadata route type %q, got %q", model.SiteModelRouteTypeOpenAIEmbedding, metadata.RouteType)
	}
}

func TestApplyPersistedRouteStateKeepsManualOverrideUntouched(t *testing.T) {
	existing := &model.SiteModel{
		ModelName:      "vendor-embedding-x",
		RouteType:      model.SiteModelRouteTypeOpenAIChat,
		RouteSource:    model.SiteModelRouteSourceManualOverride,
		ManualOverride: true,
	}
	item := &model.SiteModel{ModelName: "vendor-embedding-x"}

	applyPersistedRouteState(item, existing, time.Unix(1711929600, 0))

	if item.RouteType != model.SiteModelRouteTypeOpenAIChat {
		t.Fatalf("expected manual override route to be preserved, got %q", item.RouteType)
	}
	if !item.ManualOverride {
		t.Fatalf("expected manual override flag to be preserved")
	}
}

func TestMergePersistedSiteTokensPreservesManualFullTokenWhenIncomingIsMasked(t *testing.T) {
	now := time.Unix(1711929600, 0)
	existing := []model.SiteToken{{
		ID:            41,
		SiteAccountID: 9,
		Name:          "primary",
		Token:         "sk-yzFyREALREALOTkb",
		GroupKey:      model.SiteDefaultGroupKey,
		GroupName:     model.SiteDefaultGroupName,
		Enabled:       true,
		ValueStatus:   model.SiteTokenValueStatusReady,
		Source:        "manual",
	}}
	incoming := []model.SiteToken{{
		Name:        "primary",
		Token:       "yzFy**********OTkb",
		GroupKey:    model.SiteDefaultGroupKey,
		GroupName:   model.SiteDefaultGroupName,
		Enabled:     true,
		ValueStatus: model.SiteTokenValueStatusMaskedPending,
		Source:      "sync",
	}}

	merged := mergePersistedSiteTokens(9, existing, incoming, now, nil)
	if len(merged) != 1 {
		t.Fatalf("expected exactly one merged token, got %+v", merged)
	}
	if merged[0].Token != "sk-yzFyREALREALOTkb" {
		t.Fatalf("expected merged token to keep full manual value, got %q", merged[0].Token)
	}
	if merged[0].ValueStatus != model.SiteTokenValueStatusReady {
		t.Fatalf("expected merged token to remain ready, got %q", merged[0].ValueStatus)
	}
	if !merged[0].Enabled {
		t.Fatalf("expected merged token to remain enabled")
	}
}

func TestMergePersistedSiteTokensTreatsOptionalSKPrefixAsSameReadyToken(t *testing.T) {
	now := time.Unix(1711929600, 0)
	existing := []model.SiteToken{{
		ID:            7,
		SiteAccountID: 9,
		Name:          "primary",
		Token:         "sk-abc123",
		GroupKey:      model.SiteDefaultGroupKey,
		GroupName:     model.SiteDefaultGroupName,
		Enabled:       true,
		ValueStatus:   model.SiteTokenValueStatusReady,
		Source:        "manual",
	}}
	incoming := []model.SiteToken{{
		Name:      "primary",
		Token:     "abc123",
		GroupKey:  model.SiteDefaultGroupKey,
		GroupName: model.SiteDefaultGroupName,
		Enabled:   true,
		Source:    "sync",
	}}

	merged := mergePersistedSiteTokens(9, existing, incoming, now, nil)
	if len(merged) != 1 {
		t.Fatalf("expected exactly one merged token, got %+v", merged)
	}
	if merged[0].Token != "sk-abc123" {
		t.Fatalf("expected merged token to preserve stored full token format, got %q", merged[0].Token)
	}
	if merged[0].ValueStatus != model.SiteTokenValueStatusReady {
		t.Fatalf("expected merged token to remain ready, got %q", merged[0].ValueStatus)
	}
}

func TestMergePersistedSiteTokensKeepsSyncedDuplicateOfAccountToken(t *testing.T) {
	now := time.Unix(1711929600, 0)
	existing := []model.SiteToken{{
		ID:            7,
		SiteAccountID: 9,
		Name:          "account",
		Token:         "sk-same-account-key",
		GroupKey:      model.SiteDefaultGroupKey,
		GroupName:     model.SiteDefaultGroupName,
		Enabled:       true,
		ValueStatus:   model.SiteTokenValueStatusReady,
		Source:        "account",
		IsDefault:     true,
	}}
	incoming := []model.SiteToken{{
		Name:        "me",
		Token:       "sk-same-account-key",
		GroupKey:    model.SiteDefaultGroupKey,
		GroupName:   model.SiteDefaultGroupName,
		Enabled:     true,
		ValueStatus: model.SiteTokenValueStatusReady,
		Source:      "sync",
		IsDefault:   true,
	}}

	merged := mergePersistedSiteTokens(9, existing, incoming, now, nil)
	if len(merged) != 1 {
		t.Fatalf("expected synced duplicate account token to be kept separately, got %+v", merged)
	}
	if merged[0].ID != 0 {
		t.Fatalf("expected synced duplicate to be inserted as a separate token, got id=%d", merged[0].ID)
	}
	if merged[0].Source != "sync" || merged[0].GroupKey != model.SiteDefaultGroupKey || merged[0].Token != "sk-same-account-key" {
		t.Fatalf("unexpected synced duplicate token: %+v", merged[0])
	}
}

func TestMergePersistedSiteTokensKeepsMaskedSyncedDuplicateOfAccountToken(t *testing.T) {
	now := time.Unix(1711929600, 0)
	existing := []model.SiteToken{{
		ID:            7,
		SiteAccountID: 9,
		Name:          "account",
		Token:         "sk-SHPk123456789030T0",
		GroupKey:      model.SiteDefaultGroupKey,
		GroupName:     model.SiteDefaultGroupName,
		Enabled:       true,
		ValueStatus:   model.SiteTokenValueStatusReady,
		Source:        "account",
		IsDefault:     true,
	}}
	incoming := []model.SiteToken{{
		Name:        "me",
		Token:       "SHPk**********30T0",
		GroupKey:    "vip",
		GroupName:   "VIP",
		Enabled:     true,
		ValueStatus: model.SiteTokenValueStatusMaskedPending,
		Source:      "sync",
		IsDefault:   true,
	}}

	merged := mergePersistedSiteTokens(9, existing, incoming, now, nil)
	if len(merged) != 1 {
		t.Fatalf("expected masked synced duplicate account token to be kept separately, got %+v", merged)
	}
	if merged[0].ID != 0 {
		t.Fatalf("expected synced duplicate to be inserted as a separate token, got id=%d", merged[0].ID)
	}
	if merged[0].Token != "sk-SHPk123456789030T0" || merged[0].ValueStatus != model.SiteTokenValueStatusReady {
		t.Fatalf("expected synced duplicate to keep restored full account value, got %+v", merged[0])
	}
	if merged[0].Source != "sync" || merged[0].GroupKey != "vip" || !merged[0].Enabled {
		t.Fatalf("unexpected synced duplicate token: %+v", merged[0])
	}
}

func TestMergePersistedSiteTokensMovesReadyTokenAcrossGroups(t *testing.T) {
	now := time.Unix(1711929600, 0)
	existing := []model.SiteToken{{
		ID:            7,
		SiteAccountID: 9,
		Name:          "primary",
		Token:         "sk-moved-key",
		GroupKey:      "old",
		GroupName:     "Old",
		Enabled:       false,
		ValueStatus:   model.SiteTokenValueStatusReady,
		Source:        "manual",
	}}
	incoming := []model.SiteToken{{
		Name:      "primary",
		Token:     "sk-moved-key",
		GroupKey:  "new",
		GroupName: "New",
		Enabled:   true,
		Source:    "sync",
	}}

	merged := mergePersistedSiteTokens(9, existing, incoming, now, nil)
	if len(merged) != 1 {
		t.Fatalf("expected moved token to replace stale group copy, got %+v", merged)
	}
	if merged[0].GroupKey != "new" || merged[0].GroupName != "New" {
		t.Fatalf("expected token to move to new group, got %+v", merged[0])
	}
	if merged[0].Enabled {
		t.Fatalf("expected local disabled state to move with token")
	}
}

func TestMergePersistedSiteTokensDropsManualTokenForRemovedGroup(t *testing.T) {
	now := time.Unix(1711929600, 0)
	existing := []model.SiteToken{{
		ID:            7,
		SiteAccountID: 9,
		Name:          "old",
		Token:         "sk-old-group-key",
		GroupKey:      "old",
		GroupName:     "Old",
		Enabled:       true,
		ValueStatus:   model.SiteTokenValueStatusReady,
		Source:        "manual",
	}}

	merged := mergePersistedSiteTokens(9, existing, nil, now, map[string]struct{}{"old": {}})
	if len(merged) != 0 {
		t.Fatalf("expected removed group manual token to be dropped, got %+v", merged)
	}
}

func TestFinalizeSiteGroupSyncResultsTreatsMaskedTokenAsMissingKey(t *testing.T) {
	results := finalizeSiteGroupSyncResults(
		&model.SiteAccount{},
		[]model.SiteUserGroup{{GroupKey: "vip", Name: "VIP"}},
		[]model.SiteToken{{
			Name:        "masked",
			Token:       "sk-ab***xyz",
			GroupKey:    "vip",
			GroupName:   "VIP",
			Enabled:     false,
			ValueStatus: model.SiteTokenValueStatusMaskedPending,
		}},
		[]model.SiteModel{{GroupKey: "vip", ModelName: "gpt-4.1"}},
		nil,
	)

	if len(results) != 1 {
		t.Fatalf("expected one group result, got %+v", results)
	}
	if results[0].HasKey {
		t.Fatalf("expected masked token not to count as has_key: %+v", results[0])
	}
	if results[0].Status != siteGroupSyncStatusMissingKey {
		t.Fatalf("expected missing_key status for masked token, got %+v", results[0])
	}
}

func TestMergePersistedSiteTokensDropsStaleManualTokenWithSameGroupAndName(t *testing.T) {
	now := time.Unix(1711929600, 0)
	existing := []model.SiteToken{
		{
			ID:            7,
			SiteAccountID: 9,
			Name:          "default",
			Token:         "chat-api-key",
			GroupKey:      model.SiteDefaultGroupKey,
			GroupName:     model.SiteDefaultGroupName,
			Enabled:       true,
			ValueStatus:   model.SiteTokenValueStatusReady,
			Source:        "manual",
		},
		{
			ID:            8,
			SiteAccountID: 9,
			Name:          "default",
			Token:         "stale-session-token",
			GroupKey:      model.SiteDefaultGroupKey,
			GroupName:     model.SiteDefaultGroupName,
			Enabled:       true,
			ValueStatus:   model.SiteTokenValueStatusReady,
			Source:        "manual",
		},
	}
	incoming := []model.SiteToken{{
		Name:      "default",
		Token:     "chat-api-key",
		GroupKey:  model.SiteDefaultGroupKey,
		GroupName: model.SiteDefaultGroupName,
		Enabled:   true,
		Source:    "manual",
	}}

	merged := mergePersistedSiteTokens(9, existing, incoming, now, nil)
	if len(merged) != 1 {
		t.Fatalf("expected stale duplicate to be dropped, got %+v", merged)
	}
	if merged[0].Token != "chat-api-key" {
		t.Fatalf("expected chat api key to remain, got %q", merged[0].Token)
	}
}

func TestMergePersistedSiteTokensPreservesLocalDisabledStateForReadyToken(t *testing.T) {
	now := time.Unix(1711929600, 0)
	existing := []model.SiteToken{{
		ID:            8,
		SiteAccountID: 9,
		Name:          "primary",
		Token:         "sk-local-disabled",
		GroupKey:      model.SiteDefaultGroupKey,
		GroupName:     model.SiteDefaultGroupName,
		Enabled:       false,
		ValueStatus:   model.SiteTokenValueStatusReady,
		Source:        "manual",
	}}
	incoming := []model.SiteToken{{
		Name:      "primary",
		Token:     "sk-local-disabled",
		GroupKey:  model.SiteDefaultGroupKey,
		GroupName: model.SiteDefaultGroupName,
		Enabled:   true,
		Source:    "sync",
	}}

	merged := mergePersistedSiteTokens(9, existing, incoming, now, nil)
	if len(merged) != 1 {
		t.Fatalf("expected exactly one merged token, got %+v", merged)
	}
	if merged[0].Enabled {
		t.Fatalf("expected local disabled state to be preserved, got enabled token: %+v", merged[0])
	}
}

func TestMergePersistedSiteTokensPreservesLocalEnabledStateWhenIncomingDisabled(t *testing.T) {
	now := time.Unix(1711929600, 0)
	existing := []model.SiteToken{{
		ID:            9,
		SiteAccountID: 9,
		Name:          "primary",
		Token:         "sk-local-enabled",
		GroupKey:      model.SiteDefaultGroupKey,
		GroupName:     model.SiteDefaultGroupName,
		Enabled:       true,
		ValueStatus:   model.SiteTokenValueStatusReady,
		Source:        "sync",
	}}
	incoming := []model.SiteToken{{
		Name:      "primary",
		Token:     "sk-local-enabled",
		GroupKey:  model.SiteDefaultGroupKey,
		GroupName: model.SiteDefaultGroupName,
		Enabled:   false,
		Source:    "sync",
	}}

	merged := mergePersistedSiteTokens(9, existing, incoming, now, nil)
	if len(merged) != 1 {
		t.Fatalf("expected exactly one merged token, got %+v", merged)
	}
	if !merged[0].Enabled {
		t.Fatalf("expected local enabled state to be preserved, got disabled token: %+v", merged[0])
	}
}

func TestMergePersistedSiteTokensKeepsMaskedPendingWhenMatchIsAmbiguous(t *testing.T) {
	now := time.Unix(1711929600, 0)
	existing := []model.SiteToken{
		{
			ID:            1,
			SiteAccountID: 9,
			Name:          "alpha",
			Token:         "sk-yzFyONEOTkb",
			GroupKey:      model.SiteDefaultGroupKey,
			GroupName:     model.SiteDefaultGroupName,
			Enabled:       true,
			ValueStatus:   model.SiteTokenValueStatusReady,
			Source:        "manual",
		},
		{
			ID:            2,
			SiteAccountID: 9,
			Name:          "beta",
			Token:         "sk-yzFyTWOOTkb",
			GroupKey:      model.SiteDefaultGroupKey,
			GroupName:     model.SiteDefaultGroupName,
			Enabled:       true,
			ValueStatus:   model.SiteTokenValueStatusReady,
			Source:        "manual",
		},
	}
	incoming := []model.SiteToken{{
		Name:        "",
		Token:       "yzFy**********OTkb",
		GroupKey:    model.SiteDefaultGroupKey,
		GroupName:   model.SiteDefaultGroupName,
		Enabled:     true,
		ValueStatus: model.SiteTokenValueStatusMaskedPending,
		Source:      "sync",
	}}

	merged := mergePersistedSiteTokens(9, existing, incoming, now, nil)
	if len(merged) != 3 {
		t.Fatalf("expected masked pending token plus two preserved manual tokens, got %+v", merged)
	}
	maskedCount := 0
	for _, item := range merged {
		if item.Token == "yzFy**********OTkb" {
			maskedCount++
			if item.ValueStatus != model.SiteTokenValueStatusMaskedPending {
				t.Fatalf("expected ambiguous incoming token to remain masked_pending, got %+v", item)
			}
			if item.Enabled {
				t.Fatalf("expected ambiguous masked_pending token to stay disabled")
			}
		}
	}
	if maskedCount != 1 {
		t.Fatalf("expected exactly one preserved masked_pending token, got %+v", merged)
	}
}

func TestMergePersistedSiteTokensDemotesReadyTokenWhenMaskedPatternMismatches(t *testing.T) {
	now := time.Unix(1711929600, 0)
	existing := []model.SiteToken{{
		ID:            5,
		SiteAccountID: 9,
		Name:          "primary",
		Token:         "sk-different-full-token",
		GroupKey:      model.SiteDefaultGroupKey,
		GroupName:     model.SiteDefaultGroupName,
		Enabled:       true,
		ValueStatus:   model.SiteTokenValueStatusReady,
		Source:        "manual",
	}}
	incoming := []model.SiteToken{{
		Name:        "primary",
		Token:       "yzFy**********OTkb",
		GroupKey:    model.SiteDefaultGroupKey,
		GroupName:   model.SiteDefaultGroupName,
		Enabled:     true,
		ValueStatus: model.SiteTokenValueStatusMaskedPending,
		Source:      "sync",
	}}

	merged := mergePersistedSiteTokens(9, existing, incoming, now, nil)
	if len(merged) != 1 {
		t.Fatalf("expected exactly one merged token, got %+v", merged)
	}
	if merged[0].Token != "yzFy**********OTkb" {
		t.Fatalf("expected stale ready token to be replaced by incoming masked value, got %q", merged[0].Token)
	}
	if merged[0].ValueStatus != model.SiteTokenValueStatusMaskedPending {
		t.Fatalf("expected merged token to be demoted to masked_pending, got %q", merged[0].ValueStatus)
	}
	if merged[0].Enabled {
		t.Fatalf("expected demoted token to be disabled until the user re-fills it")
	}
}

func TestPersistSyncSnapshotPreservesGroupProjectionDisabled(t *testing.T) {
	ctx := setupProjectTestDB(t)
	_, account := createProjectionFixture(t, ctx)

	vipGroup := model.SiteUserGroup{SiteAccountID: account.ID, GroupKey: "vip", Name: "VIP", ProjectionDisabled: true}
	if err := dbpkg.GetDB().WithContext(ctx).Create(&vipGroup).Error; err != nil {
		t.Fatalf("create vip group failed: %v", err)
	}

	snapshot := &syncSnapshot{
		accessToken: account.AccessToken,
		groups: []model.SiteUserGroup{
			{GroupKey: "vip", Name: "VIP Renamed"},
		},
		tokens: []model.SiteToken{
			{Name: "vip", Token: "key-vip", GroupKey: "vip", GroupName: "VIP", Enabled: true, Source: "sync"},
		},
		status:  model.SiteExecutionStatusSuccess,
		message: "ok",
	}

	if err := persistSyncSnapshot(ctx, account.ID, snapshot); err != nil {
		t.Fatalf("persistSyncSnapshot returned error: %v", err)
	}

	var reloaded model.SiteUserGroup
	if err := dbpkg.GetDB().WithContext(ctx).Where("site_account_id = ? AND group_key = ?", account.ID, "vip").First(&reloaded).Error; err != nil {
		t.Fatalf("query reloaded group failed: %v", err)
	}
	if !reloaded.ProjectionDisabled {
		t.Fatalf("expected projection_disabled to be preserved")
	}
	if reloaded.Name != "VIP Renamed" {
		t.Fatalf("expected synced group metadata to be updated, got %q", reloaded.Name)
	}
}

func TestPersistSyncSnapshotPrunesRemovedGroupManualKeysAndProjection(t *testing.T) {
	ctx := setupProjectTestDB(t)
	site, account := createProjectionFixture(t, ctx)

	vipGroup := model.SiteUserGroup{SiteAccountID: account.ID, GroupKey: "vip", Name: "VIP"}
	if err := dbpkg.GetDB().WithContext(ctx).Create(&vipGroup).Error; err != nil {
		t.Fatalf("create vip group failed: %v", err)
	}
	vipToken := model.SiteToken{
		SiteAccountID: account.ID,
		Name:          "vip",
		Token:         "key-vip",
		GroupKey:      "vip",
		GroupName:     "VIP",
		Enabled:       true,
		ValueStatus:   model.SiteTokenValueStatusReady,
		Source:        "manual",
	}
	if err := dbpkg.GetDB().WithContext(ctx).Create(&vipToken).Error; err != nil {
		t.Fatalf("create vip token failed: %v", err)
	}
	vipModel := model.SiteModel{SiteAccountID: account.ID, GroupKey: "vip", ModelName: "gpt-4o-vip", Source: "sync", RouteType: model.SiteModelRouteTypeOpenAIChat, RouteSource: model.SiteModelRouteSourceSyncInferred}
	if err := dbpkg.GetDB().WithContext(ctx).Create(&vipModel).Error; err != nil {
		t.Fatalf("create vip model failed: %v", err)
	}
	if _, err := ProjectAccount(ctx, account.ID); err != nil {
		t.Fatalf("initial ProjectAccount failed: %v", err)
	}
	if channelsByGroup := loadProjectedChannelsByGroupKey(t, ctx, account.ID); channelsByGroup["vip"].ID == 0 {
		t.Fatalf("expected initial vip projected channel, got %+v", channelsByGroup)
	}

	snapshot := &syncSnapshot{
		accessToken: account.AccessToken,
		groups:      []model.SiteUserGroup{{GroupKey: model.SiteDefaultGroupKey, Name: model.SiteDefaultGroupName}},
		tokens:      []model.SiteToken{{Name: "primary", Token: "key-primary", GroupKey: model.SiteDefaultGroupKey, GroupName: model.SiteDefaultGroupName, Enabled: true, Source: "sync"}},
		models:      []model.SiteModel{{GroupKey: model.SiteDefaultGroupKey, ModelName: "gpt-4o-mini", Source: "sync", RouteType: model.SiteModelRouteTypeOpenAIChat, RouteSource: model.SiteModelRouteSourceSyncInferred}},
		groupResults: []siteGroupSyncResult{
			{GroupKey: model.SiteDefaultGroupKey, GroupName: model.SiteDefaultGroupName, HasKey: true, Status: siteGroupSyncStatusSynced, Authoritative: true, ModelCount: 1},
			{GroupKey: "vip", GroupName: "VIP", HasKey: false, Status: siteGroupSyncStatusRemoved, Authoritative: true, Message: "上游已移除此分组，已清理历史模型"},
		},
		status:  model.SiteExecutionStatusSuccess,
		message: "同步完成",
	}
	if err := persistSyncSnapshot(ctx, account.ID, snapshot); err != nil {
		t.Fatalf("persistSyncSnapshot returned error: %v", err)
	}
	if _, err := ProjectAccount(ctx, account.ID); err != nil {
		t.Fatalf("ProjectAccount after removed group failed: %v", err)
	}

	var vipTokenCount int64
	if err := dbpkg.GetDB().WithContext(ctx).Model(&model.SiteToken{}).Where("site_account_id = ? AND group_key = ?", account.ID, "vip").Count(&vipTokenCount).Error; err != nil {
		t.Fatalf("count vip tokens failed: %v", err)
	}
	if vipTokenCount != 0 {
		t.Fatalf("expected vip tokens to be pruned, got %d", vipTokenCount)
	}
	if channelsByGroup := loadProjectedChannelsByGroupKey(t, ctx, account.ID); channelsByGroup["vip"].ID != 0 {
		t.Fatalf("expected vip projected channel binding to be removed, got %+v", channelsByGroup)
	}
	channelView, err := op.SiteChannelAccountGet(site.ID, account.ID, ctx)
	if err != nil {
		t.Fatalf("SiteChannelAccountGet failed: %v", err)
	}
	for _, group := range channelView.Groups {
		if group.GroupKey == "vip" {
			t.Fatalf("expected removed vip group to disappear from channel view, got %+v", channelView.Groups)
		}
	}
}

func TestPersistSyncSnapshotReplacesOnlyAuthoritativeGroups(t *testing.T) {
	ctx := setupProjectTestDB(t)
	_, account := createProjectionFixture(t, ctx)

	vipGroup := model.SiteUserGroup{SiteAccountID: account.ID, GroupKey: "vip", Name: "VIP"}
	if err := dbpkg.GetDB().WithContext(ctx).Create(&vipGroup).Error; err != nil {
		t.Fatalf("create vip group failed: %v", err)
	}
	vipToken := model.SiteToken{SiteAccountID: account.ID, Name: "vip", Token: "key-vip", GroupKey: "vip", GroupName: "VIP", Enabled: true}
	if err := dbpkg.GetDB().WithContext(ctx).Create(&vipToken).Error; err != nil {
		t.Fatalf("create vip token failed: %v", err)
	}
	vipModel := model.SiteModel{SiteAccountID: account.ID, GroupKey: "vip", ModelName: "gpt-4o-vip-old", Source: "sync", RouteType: model.SiteModelRouteTypeOpenAIChat, RouteSource: model.SiteModelRouteSourceSyncInferred}
	if err := dbpkg.GetDB().WithContext(ctx).Create(&vipModel).Error; err != nil {
		t.Fatalf("create vip model failed: %v", err)
	}

	snapshot := &syncSnapshot{
		accessToken: account.AccessToken,
		groups: []model.SiteUserGroup{
			{GroupKey: model.SiteDefaultGroupKey, Name: model.SiteDefaultGroupName},
			{GroupKey: "vip", Name: "VIP"},
		},
		tokens: []model.SiteToken{
			{Name: "primary", Token: "key-primary-new", GroupKey: model.SiteDefaultGroupKey, GroupName: model.SiteDefaultGroupName, Enabled: true, Source: "sync"},
			{Name: "vip", Token: "key-vip-new", GroupKey: "vip", GroupName: "VIP", Enabled: true, Source: "sync"},
		},
		models: []model.SiteModel{
			{GroupKey: model.SiteDefaultGroupKey, ModelName: "gpt-4.1", Source: "sync", RouteType: model.SiteModelRouteTypeOpenAIChat, RouteSource: model.SiteModelRouteSourceSyncInferred},
		},
		groupResults: []siteGroupSyncResult{
			{GroupKey: model.SiteDefaultGroupKey, GroupName: model.SiteDefaultGroupName, HasKey: true, Status: siteGroupSyncStatusSynced, Authoritative: true, ModelCount: 1, Message: "同步到 1 个模型"},
			{GroupKey: "vip", GroupName: "VIP", HasKey: true, Status: siteGroupSyncStatusFailed, Authoritative: false, Message: "unauthorized"},
		},
		status:  model.SiteExecutionStatusPartial,
		message: "部分分组同步完成：更新 1 个分组，保留 1 个分组的历史投影",
	}

	if err := persistSyncSnapshot(ctx, account.ID, snapshot); err != nil {
		t.Fatalf("persistSyncSnapshot returned error: %v", err)
	}

	var models []model.SiteModel
	if err := dbpkg.GetDB().WithContext(ctx).Where("site_account_id = ?", account.ID).Order("group_key ASC, model_name ASC").Find(&models).Error; err != nil {
		t.Fatalf("query models failed: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("expected one refreshed default model and one preserved vip model, got %+v", models)
	}
	modelsByGroup := make(map[string][]string)
	for _, item := range models {
		modelsByGroup[item.GroupKey] = append(modelsByGroup[item.GroupKey], item.ModelName)
	}
	if len(modelsByGroup[model.SiteDefaultGroupKey]) != 1 || modelsByGroup[model.SiteDefaultGroupKey][0] != "gpt-4.1" {
		t.Fatalf("expected default group to be fully replaced, got %+v", modelsByGroup)
	}
	if len(modelsByGroup["vip"]) != 1 || modelsByGroup["vip"][0] != "gpt-4o-vip-old" {
		t.Fatalf("expected vip group to keep historical model, got %+v", modelsByGroup)
	}

	reloaded, err := op.SiteAccountGet(account.ID, context.Background())
	if err != nil {
		t.Fatalf("SiteAccountGet failed: %v", err)
	}
	if reloaded.LastSyncStatus != model.SiteExecutionStatusPartial {
		t.Fatalf("expected partial last_sync_status, got %q", reloaded.LastSyncStatus)
	}
	if reloaded.LastSyncMessage != snapshot.message {
		t.Fatalf("expected last_sync_message %q, got %q", snapshot.message, reloaded.LastSyncMessage)
	}

	var vipReloaded model.SiteUserGroup
	if err := dbpkg.GetDB().WithContext(ctx).Where("site_account_id = ? AND group_key = ?", account.ID, "vip").First(&vipReloaded).Error; err != nil {
		t.Fatalf("query vip group failed: %v", err)
	}
	if vipReloaded.ProjectionSuspended {
		t.Fatalf("expected failed vip group projection to keep historical projection active")
	}
	if vipReloaded.ModelSyncStatus != model.SiteGroupModelSyncStatusFailed {
		t.Fatalf("expected failed vip model sync status, got %q", vipReloaded.ModelSyncStatus)
	}
	if vipReloaded.ModelSyncFailureCount != 1 {
		t.Fatalf("expected vip failure count 1, got %d", vipReloaded.ModelSyncFailureCount)
	}
}

func TestPersistSyncSnapshotEmptySuspendsWithoutAdvancingSuccessTime(t *testing.T) {
	ctx := setupProjectTestDB(t)
	_, account := createProjectionFixture(t, ctx)

	previousSuccess := time.Unix(1700000000, 0)
	group := model.SiteUserGroup{
		SiteAccountID:          account.ID,
		GroupKey:               model.SiteDefaultGroupKey,
		Name:                   model.SiteDefaultGroupName,
		ModelSyncStatus:        model.SiteGroupModelSyncStatusSynced,
		LastModelSyncSuccessAt: &previousSuccess,
	}
	if err := dbpkg.GetDB().WithContext(ctx).Create(&group).Error; err != nil {
		t.Fatalf("create group failed: %v", err)
	}

	snapshot := &syncSnapshot{
		accessToken: account.AccessToken,
		groups:      []model.SiteUserGroup{{GroupKey: model.SiteDefaultGroupKey, Name: model.SiteDefaultGroupName}},
		tokens:      []model.SiteToken{{Name: "primary", Token: "key-primary", GroupKey: model.SiteDefaultGroupKey, GroupName: model.SiteDefaultGroupName, Enabled: true, Source: "sync"}},
		groupResults: []siteGroupSyncResult{
			{GroupKey: model.SiteDefaultGroupKey, GroupName: model.SiteDefaultGroupName, HasKey: true, Status: siteGroupSyncStatusEmpty, Authoritative: true, Message: "上游当前没有可用模型"},
		},
		status:  model.SiteExecutionStatusSuccess,
		message: "上游当前无可用模型，已清空历史模型",
	}
	if err := persistSyncSnapshot(ctx, account.ID, snapshot); err != nil {
		t.Fatalf("persistSyncSnapshot returned error: %v", err)
	}

	var reloaded model.SiteUserGroup
	if err := dbpkg.GetDB().WithContext(ctx).Where("site_account_id = ? AND group_key = ?", account.ID, model.SiteDefaultGroupKey).First(&reloaded).Error; err != nil {
		t.Fatalf("query reloaded group failed: %v", err)
	}
	if !reloaded.ProjectionSuspended {
		t.Fatalf("expected empty group projection to be suspended")
	}
	if reloaded.LastModelSyncSuccessAt == nil || !reloaded.LastModelSyncSuccessAt.Equal(previousSuccess) {
		t.Fatalf("expected empty sync to preserve last success time %v, got %v", previousSuccess, reloaded.LastModelSyncSuccessAt)
	}
}
