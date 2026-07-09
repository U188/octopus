package op

import (
	"testing"

	dbpkg "github.com/U188/octopus/internal/db"
	"github.com/U188/octopus/internal/model"
	"github.com/U188/octopus/internal/transformer/outbound"
)

func TestGroupGetEnabledMapMatchesRegexWhenExactGroupMissing(t *testing.T) {
	ctx := setupSiteOpTestDB(t)
	groupCache.Clear()
	groupMap.Clear()
	channelCache.Clear()

	channel := &model.Channel{
		Name:     "group-regex-enabled-channel",
		Type:     outbound.OutboundTypeOpenAIChat,
		Enabled:  true,
		BaseUrls: []model.BaseUrl{{URL: "https://example.com/v1"}},
		Model:    "gpt-5.5",
		Keys:     []model.ChannelKey{{Enabled: true, ChannelKey: "test-key"}},
	}
	if err := ChannelCreate(channel, ctx); err != nil {
		t.Fatalf("ChannelCreate failed: %v", err)
	}

	disabledChannel := &model.Channel{
		Name:     "group-regex-disabled-channel",
		Type:     outbound.OutboundTypeOpenAIChat,
		Enabled:  true,
		BaseUrls: []model.BaseUrl{{URL: "https://example.com/v1"}},
		Model:    "gpt-5.5",
		Keys:     []model.ChannelKey{{Enabled: true, ChannelKey: "test-key"}},
	}
	if err := ChannelCreate(disabledChannel, ctx); err != nil {
		t.Fatalf("ChannelCreate disabled failed: %v", err)
	}
	if err := ChannelEnabled(disabledChannel.ID, false, ctx); err != nil {
		t.Fatalf("ChannelEnabled disabled failed: %v", err)
	}

	group := &model.Group{
		Name:       "regex-target-group",
		Mode:       model.GroupModeFailover,
		MatchRegex: `^gpt-5(\.\d+)?$`,
	}
	if err := GroupCreate(group, ctx); err != nil {
		t.Fatalf("GroupCreate failed: %v", err)
	}
	if err := GroupItemAdd(&model.GroupItem{GroupID: group.ID, ChannelID: channel.ID, ModelName: "gpt-5.5", Priority: 1, Weight: 1}, ctx); err != nil {
		t.Fatalf("GroupItemAdd enabled failed: %v", err)
	}
	if err := GroupItemAdd(&model.GroupItem{GroupID: group.ID, ChannelID: disabledChannel.ID, ModelName: "gpt-5.5", Priority: 2, Weight: 1}, ctx); err != nil {
		t.Fatalf("GroupItemAdd disabled failed: %v", err)
	}

	got, err := GroupGetEnabledMap("gpt-5.5", ctx)
	if err != nil {
		t.Fatalf("GroupGetEnabledMap failed: %v", err)
	}
	if got.ID != group.ID {
		t.Fatalf("expected regex group %d, got %d", group.ID, got.ID)
	}
	if len(got.Items) != 1 || got.Items[0].ChannelID != channel.ID {
		t.Fatalf("expected only enabled channel item, got %+v", got.Items)
	}
}

func TestGroupGetEnabledMapPrefersExactNameOverRegex(t *testing.T) {
	ctx := setupSiteOpTestDB(t)
	groupCache.Clear()
	groupMap.Clear()
	channelCache.Clear()

	exact := &model.Group{Name: "gpt-5.5", Mode: model.GroupModeFailover}
	if err := GroupCreate(exact, ctx); err != nil {
		t.Fatalf("GroupCreate exact failed: %v", err)
	}
	regex := &model.Group{Name: "regex-gpt", Mode: model.GroupModeFailover, MatchRegex: `^gpt-5\.5$`}
	if err := GroupCreate(regex, ctx); err != nil {
		t.Fatalf("GroupCreate regex failed: %v", err)
	}

	got, err := GroupGetEnabledMap("gpt-5.5", ctx)
	if err != nil {
		t.Fatalf("GroupGetEnabledMap failed: %v", err)
	}
	if got.ID != exact.ID {
		t.Fatalf("expected exact group %d, got %d", exact.ID, got.ID)
	}
}

func TestGroupListHidesProjectedModelsWithoutUsableSiteKey(t *testing.T) {
	ctx := setupSiteOpTestDB(t)
	groupCache.Clear()
	groupMap.Clear()
	channelCache.Clear()

	site := &model.Site{
		Name:     "group-projected-hidden-site",
		Platform: model.SitePlatformNewAPI,
		BaseURL:  "https://example.com",
		Enabled:  true,
	}
	if err := SiteCreate(site, ctx); err != nil {
		t.Fatalf("SiteCreate failed: %v", err)
	}
	account := &model.SiteAccount{
		SiteID:         site.ID,
		Name:           "group-projected-hidden-account",
		CredentialType: model.SiteCredentialTypeAccessToken,
		AccessToken:    "token",
		Enabled:        true,
	}
	if err := SiteAccountCreate(account, ctx); err != nil {
		t.Fatalf("SiteAccountCreate failed: %v", err)
	}
	siteGroup := model.SiteUserGroup{
		SiteAccountID:         account.ID,
		GroupKey:              "free",
		Name:                  "Free",
		ModelSyncStatus:       model.SiteGroupModelSyncStatusMissingKey,
		ModelSyncMessage:      "Key 已失效或被上游拒绝，已暂停投影",
		ProjectionSuspended:   true,
		ModelSyncModelCount:   0,
		ModelSyncFailureCount: 1,
	}
	if err := dbpkg.GetDB().WithContext(ctx).Create(&siteGroup).Error; err != nil {
		t.Fatalf("create site group failed: %v", err)
	}

	channel := &model.Channel{
		Name:     "managed-free-channel",
		Type:     outbound.OutboundTypeOpenAIChat,
		Enabled:  true,
		BaseUrls: []model.BaseUrl{{URL: "https://example.com/v1"}},
		Model:    "gpt-hidden",
		Keys:     []model.ChannelKey{{Enabled: true, ChannelKey: "projected-key"}},
	}
	if err := ChannelCreate(channel, ctx); err != nil {
		t.Fatalf("ChannelCreate failed: %v", err)
	}
	if err := dbpkg.GetDB().WithContext(ctx).Create(&model.SiteChannelBinding{
		SiteID:          site.ID,
		SiteAccountID:   account.ID,
		SiteUserGroupID: &siteGroup.ID,
		GroupKey:        "free",
		ChannelID:       channel.ID,
	}).Error; err != nil {
		t.Fatalf("create binding failed: %v", err)
	}

	group := &model.Group{Name: "gpt-hidden", Mode: model.GroupModeFailover}
	if err := GroupCreate(group, ctx); err != nil {
		t.Fatalf("GroupCreate failed: %v", err)
	}
	if err := GroupItemAdd(&model.GroupItem{GroupID: group.ID, ChannelID: channel.ID, ModelName: "gpt-hidden", Priority: 1, Weight: 1}, ctx); err != nil {
		t.Fatalf("GroupItemAdd failed: %v", err)
	}

	groups, err := GroupList(ctx)
	if err != nil {
		t.Fatalf("GroupList failed: %v", err)
	}
	for _, item := range groups {
		if item.ID == group.ID && len(item.Items) != 0 {
			t.Fatalf("expected group list to hide projected item without usable key, got %+v", item.Items)
		}
	}

	routeGroup, err := GroupGetEnabledMap("gpt-hidden", ctx)
	if err != nil {
		t.Fatalf("GroupGetEnabledMap failed: %v", err)
	}
	if len(routeGroup.Items) != 0 {
		t.Fatalf("expected routing candidates to hide projected item without usable key, got %+v", routeGroup.Items)
	}

	models, err := ChannelLLMList(ctx)
	if err != nil {
		t.Fatalf("ChannelLLMList failed: %v", err)
	}
	for _, item := range models {
		if item.ChannelID == channel.ID {
			t.Fatalf("expected model picker to hide projected channel without usable key, got %+v", item)
		}
	}
}
