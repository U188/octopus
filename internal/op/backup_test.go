package op

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	dbpkg "github.com/U188/octopus/internal/db"
	"github.com/U188/octopus/internal/model"
)

func setupBackupTestDB(t *testing.T) context.Context {
	t.Helper()

	if dbpkg.GetDB() != nil {
		_ = dbpkg.Close()
	}

	dbPath := filepath.Join(t.TempDir(), "octopus-backup-test.db")
	if err := dbpkg.InitDB("sqlite", dbPath, false); err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	t.Cleanup(func() {
		_ = dbpkg.Close()
	})

	return context.Background()
}

func TestDBImportPreservesAllAccountsOnCleanDB(t *testing.T) {
	ctx := setupBackupTestDB(t)

	dump := buildTestDump()
	result, err := DBImportIncremental(ctx, dump)
	if err != nil {
		t.Fatalf("DBImportIncremental failed: %v", err)
	}

	if result.RowsAffected["sites"] != 1 {
		t.Fatalf("expected 1 site created, got %d", result.RowsAffected["sites"])
	}
	if result.RowsAffected["site_accounts"] != 3 {
		t.Fatalf("expected 3 site accounts created, got %d", result.RowsAffected["site_accounts"])
	}

	site, err := SiteGet(1, ctx)
	if err != nil {
		// Site might have a different ID after import; query by platform+url
		var sites []model.Site
		if qerr := dbpkg.GetDB().Where("platform = ? AND base_url = ?", "new-api", "https://example.com").Find(&sites).Error; qerr != nil {
			t.Fatalf("query sites failed: %v", qerr)
		}
		if len(sites) != 1 {
			t.Fatalf("expected 1 site, got %d", len(sites))
		}
		site, err = SiteGet(sites[0].ID, ctx)
		if err != nil {
			t.Fatalf("SiteGet failed: %v", err)
		}
	}
	if len(site.Accounts) != 3 {
		t.Fatalf("expected site to have 3 accounts, got %d", len(site.Accounts))
	}
}

func TestDBImportWithIDCollisionPreservesAllAccounts(t *testing.T) {
	ctx := setupBackupTestDB(t)

	// Create pre-existing data that will cause ID collisions
	preexistingSite := &model.Site{
		Name:     "other-site",
		Platform: model.SitePlatformOneAPI,
		BaseURL:  "https://other.com",
		Enabled:  true,
	}
	if err := SiteCreate(preexistingSite, ctx); err != nil {
		t.Fatalf("create pre-existing site failed: %v", err)
	}
	preexistingAccount := &model.SiteAccount{
		SiteID:         preexistingSite.ID,
		Name:           "other-account",
		CredentialType: model.SiteCredentialTypeAPIKey,
		APIKey:         "sk-other",
		Enabled:        true,
		AutoSync:       true,
	}
	if err := SiteAccountCreate(preexistingAccount, ctx); err != nil {
		t.Fatalf("create pre-existing account failed: %v", err)
	}

	// Now import a dump that has records with IDs that overlap
	dump := buildTestDump()
	result, err := DBImportIncremental(ctx, dump)
	if err != nil {
		t.Fatalf("DBImportIncremental failed: %v", err)
	}

	// All 3 accounts from the dump should be imported
	if result.RowsAffected["site_accounts"] != 3 {
		t.Fatalf("expected 3 site accounts created, got %d", result.RowsAffected["site_accounts"])
	}

	// The pre-existing data should still be intact
	var totalAccounts int64
	if err := dbpkg.GetDB().Model(&model.SiteAccount{}).Count(&totalAccounts).Error; err != nil {
		t.Fatalf("count accounts failed: %v", err)
	}
	if totalAccounts != 4 { // 1 pre-existing + 3 imported
		t.Fatalf("expected 4 total accounts, got %d", totalAccounts)
	}

	// Verify the imported site has all 3 accounts
	var importedSite model.Site
	if err := dbpkg.GetDB().Where("platform = ? AND base_url = ?", "new-api", "https://example.com").First(&importedSite).Error; err != nil {
		t.Fatalf("query imported site failed: %v", err)
	}
	var importedAccountCount int64
	if err := dbpkg.GetDB().Model(&model.SiteAccount{}).Where("site_id = ?", importedSite.ID).Count(&importedAccountCount).Error; err != nil {
		t.Fatalf("count imported accounts failed: %v", err)
	}
	if importedAccountCount != 3 {
		t.Fatalf("expected imported site to have 3 accounts, got %d", importedAccountCount)
	}
}

func TestDBImportDeduplicatesOnSecondImport(t *testing.T) {
	ctx := setupBackupTestDB(t)

	dump := buildTestDump()

	// First import
	if _, err := DBImportIncremental(ctx, dump); err != nil {
		t.Fatalf("first DBImportIncremental failed: %v", err)
	}

	// Second import of the same data
	dump2 := buildTestDump()
	result, err := DBImportIncremental(ctx, dump2)
	if err != nil {
		t.Fatalf("second DBImportIncremental failed: %v", err)
	}

	// Nothing new should be created (all deduped)
	if result.RowsAffected["sites"] != 0 {
		t.Fatalf("expected 0 new sites on second import, got %d", result.RowsAffected["sites"])
	}
	if result.RowsAffected["site_accounts"] != 0 {
		t.Fatalf("expected 0 new accounts on second import, got %d", result.RowsAffected["site_accounts"])
	}
	if result.RowsAffected["channels"] != 0 {
		t.Fatalf("expected 0 new channels on second import, got %d", result.RowsAffected["channels"])
	}

	// Total counts should remain the same
	var siteCount, accountCount, channelCount int64
	dbpkg.GetDB().Model(&model.Site{}).Count(&siteCount)
	dbpkg.GetDB().Model(&model.SiteAccount{}).Count(&accountCount)
	dbpkg.GetDB().Model(&model.Channel{}).Count(&channelCount)

	if siteCount != 1 {
		t.Fatalf("expected 1 site after double import, got %d", siteCount)
	}
	if accountCount != 3 {
		t.Fatalf("expected 3 accounts after double import, got %d", accountCount)
	}
	if channelCount != 1 {
		t.Fatalf("expected 1 channel after double import, got %d", channelCount)
	}
}

func TestDBImportSkipsOrphanedStats(t *testing.T) {
	ctx := setupBackupTestDB(t)

	dump := buildTestDump()
	dump.IncludeStats = true
	dump.StatsChannel = []model.StatsChannel{
		{ChannelID: 1, StatsMetrics: model.StatsMetrics{RequestSuccess: 1}},
		{ChannelID: 999, StatsMetrics: model.StatsMetrics{RequestSuccess: 2}},
	}
	dump.StatsModel = []model.StatsModel{
		{ID: 1, ChannelID: 1, Name: "gpt-4", StatsMetrics: model.StatsMetrics{RequestSuccess: 1}},
		{ID: 2, ChannelID: 999, Name: "orphan", StatsMetrics: model.StatsMetrics{RequestSuccess: 2}},
	}
	dump.StatsAPIKey = []model.StatsAPIKey{
		{APIKeyID: 999, StatsMetrics: model.StatsMetrics{RequestSuccess: 2}},
	}

	result, err := DBImportIncremental(ctx, dump)
	if err != nil {
		t.Fatalf("DBImportIncremental failed: %v", err)
	}
	if result.RowsAffected["stats_channel"] != 1 {
		t.Fatalf("expected 1 stats_channel imported, got %d", result.RowsAffected["stats_channel"])
	}
	if result.RowsAffected["stats_model"] != 1 {
		t.Fatalf("expected 1 stats_model imported, got %d", result.RowsAffected["stats_model"])
	}
	if result.RowsAffected["stats_api_key"] != 0 {
		t.Fatalf("expected 0 stats_api_key imported, got %d", result.RowsAffected["stats_api_key"])
	}
}

func TestDBImportSkipsChildrenWhenParentMappingIsMissing(t *testing.T) {
	ctx := setupBackupTestDB(t)

	existing := model.Channel{Name: "existing-channel", Enabled: true}
	if err := ChannelCreate(&existing, ctx); err != nil {
		t.Fatalf("create existing channel failed: %v", err)
	}

	dump := &model.DBDump{
		Version: dbDumpVersion,
		ChannelKeys: []model.ChannelKey{
			{ID: 10, ChannelID: existing.ID, ChannelKey: "must-not-attach", Enabled: true},
		},
		SiteAccounts: []model.SiteAccount{
			{ID: 10, SiteID: 999, Name: "orphan-account", CredentialType: model.SiteCredentialTypeAPIKey},
		},
		SiteCredentials: []model.SiteCredential{
			{ID: 10, SiteAccountID: 999, Purpose: model.SiteCredentialPurposeChat, Token: "orphan-token"},
		},
		GroupItems: []model.GroupItem{
			{ID: 10, GroupID: 999, ChannelID: existing.ID, ModelName: "orphan-model"},
		},
	}
	if _, err := DBImportIncremental(ctx, dump); err != nil {
		t.Fatalf("DBImportIncremental failed: %v", err)
	}

	var keyCount, accountCount, credentialCount, itemCount int64
	dbpkg.GetDB().Model(&model.ChannelKey{}).Where("channel_key = ?", "must-not-attach").Count(&keyCount)
	dbpkg.GetDB().Model(&model.SiteAccount{}).Where("name = ?", "orphan-account").Count(&accountCount)
	dbpkg.GetDB().Model(&model.SiteCredential{}).Where("value = ?", "orphan-token").Count(&credentialCount)
	dbpkg.GetDB().Model(&model.GroupItem{}).Where("model_name = ?", "orphan-model").Count(&itemCount)
	if keyCount != 0 || accountCount != 0 || credentialCount != 0 || itemCount != 0 {
		t.Fatalf("orphan rows were imported: keys=%d accounts=%d credentials=%d items=%d", keyCount, accountCount, credentialCount, itemCount)
	}
}

func TestDBImportRemapsRelayLogChannelAndKeyIDs(t *testing.T) {
	ctx := setupBackupTestDB(t)

	existing := model.Channel{Name: "existing-channel", Enabled: true}
	if err := ChannelCreate(&existing, ctx); err != nil {
		t.Fatalf("create existing channel failed: %v", err)
	}

	dump := &model.DBDump{
		Version:     dbDumpVersion,
		IncludeLogs: true,
		Channels: []model.Channel{
			{ID: 1, Name: "imported-channel", Enabled: true},
		},
		ChannelKeys: []model.ChannelKey{
			{ID: 7, ChannelID: 1, ChannelKey: "imported-key", Enabled: true},
		},
		RelayLogs: []model.RelayLog{{
			ID:        9001,
			Time:      1,
			ChannelId: 1,
			Attempts: []model.ChannelAttempt{{
				ChannelID:    1,
				ChannelKeyID: 7,
				Status:       model.AttemptSuccess,
			}},
		}},
	}
	if _, err := DBImportIncremental(ctx, dump); err != nil {
		t.Fatalf("DBImportIncremental failed: %v", err)
	}

	var importedChannel model.Channel
	if err := dbpkg.GetDB().Where("name = ?", "imported-channel").First(&importedChannel).Error; err != nil {
		t.Fatalf("query imported channel failed: %v", err)
	}
	var importedKey model.ChannelKey
	if err := dbpkg.GetDB().Where("channel_id = ? AND channel_key = ?", importedChannel.ID, "imported-key").First(&importedKey).Error; err != nil {
		t.Fatalf("query imported key failed: %v", err)
	}
	var relayLog model.RelayLog
	if err := dbpkg.GetDB().First(&relayLog, "id = ?", 9001).Error; err != nil {
		t.Fatalf("query relay log failed: %v", err)
	}
	if relayLog.ChannelId != importedChannel.ID {
		t.Fatalf("relay channel id = %d, want %d", relayLog.ChannelId, importedChannel.ID)
	}
	if len(relayLog.Attempts) != 1 ||
		relayLog.Attempts[0].ChannelID != importedChannel.ID ||
		relayLog.Attempts[0].ChannelKeyID != importedKey.ID {
		t.Fatalf("relay attempts not remapped: %+v", relayLog.Attempts)
	}
}

func TestDBImportRejectsInvalidSettings(t *testing.T) {
	ctx := setupBackupTestDB(t)

	dump := &model.DBDump{
		Version: dbDumpVersion,
		Settings: []model.Setting{{
			Key:   model.SettingKeyTelegramBotPollInterval,
			Value: "0",
		}},
	}
	if _, err := DBImportIncremental(ctx, dump); err == nil || !strings.Contains(err.Error(), "telegram_bot_poll_interval_seconds") {
		t.Fatalf("expected invalid setting import to fail with setting key, got %v", err)
	}
}

func TestDBImportRejectsUnknownSettings(t *testing.T) {
	ctx := setupBackupTestDB(t)

	dump := &model.DBDump{
		Version: dbDumpVersion,
		Settings: []model.Setting{{
			Key:   model.SettingKey("unknown_setting"),
			Value: "1",
		}},
	}
	if _, err := DBImportIncremental(ctx, dump); err == nil || !strings.Contains(err.Error(), "unknown setting key") {
		t.Fatalf("expected unknown setting import to fail, got %v", err)
	}
}

func TestDBExportThenImportRoundtrip(t *testing.T) {
	ctx := setupBackupTestDB(t)

	// Create test data
	site := &model.Site{
		Name:     "roundtrip-site",
		Platform: model.SitePlatformNewAPI,
		BaseURL:  "https://roundtrip.example.com",
		Enabled:  true,
	}
	if err := SiteCreate(site, ctx); err != nil {
		t.Fatalf("SiteCreate failed: %v", err)
	}
	for i := 0; i < 5; i++ {
		account := &model.SiteAccount{
			SiteID:         site.ID,
			Name:           mustSprintf("account-%d", i),
			CredentialType: model.SiteCredentialTypeAPIKey,
			APIKey:         mustSprintf("sk-key-%d", i),
			Enabled:        true,
			AutoSync:       true,
		}
		if err := SiteAccountCreate(account, ctx); err != nil {
			t.Fatalf("SiteAccountCreate failed: %v", err)
		}
	}

	// Export
	dump, err := DBExportAll(ctx, false, false)
	if err != nil {
		t.Fatalf("DBExportAll failed: %v", err)
	}

	// Verify export contains all accounts
	if len(dump.SiteAccounts) != 5 {
		t.Fatalf("expected 5 accounts in export, got %d", len(dump.SiteAccounts))
	}

	// Close and re-create a fresh DB
	_ = dbpkg.Close()
	freshDBPath := filepath.Join(t.TempDir(), "octopus-fresh.db")
	if err := dbpkg.InitDB("sqlite", freshDBPath, false); err != nil {
		t.Fatalf("InitDB for fresh DB failed: %v", err)
	}

	// Import to fresh DB
	result, err := DBImportIncremental(ctx, dump)
	if err != nil {
		t.Fatalf("DBImportIncremental to fresh DB failed: %v", err)
	}
	if result.RowsAffected["sites"] != 1 {
		t.Fatalf("expected 1 site imported, got %d", result.RowsAffected["sites"])
	}
	if result.RowsAffected["site_accounts"] != 5 {
		t.Fatalf("expected 5 accounts imported, got %d", result.RowsAffected["site_accounts"])
	}

	// Verify all accounts are present
	var freshSite model.Site
	if err := dbpkg.GetDB().Where("platform = ? AND base_url = ?", "new-api", "https://roundtrip.example.com").First(&freshSite).Error; err != nil {
		t.Fatalf("query imported site failed: %v", err)
	}
	var accountCount int64
	if err := dbpkg.GetDB().Model(&model.SiteAccount{}).Where("site_id = ?", freshSite.ID).Count(&accountCount).Error; err != nil {
		t.Fatalf("count accounts failed: %v", err)
	}
	if accountCount != 5 {
		t.Fatalf("expected 5 accounts for imported site, got %d", accountCount)
	}
}

func buildTestDump() *model.DBDump {
	return &model.DBDump{
		Version:      dbDumpVersion,
		IncludeLogs:  false,
		IncludeStats: false,
		Channels: []model.Channel{
			{ID: 1, Name: "test-channel", Enabled: true},
		},
		ChannelKeys: []model.ChannelKey{
			{ID: 1, ChannelID: 1, Enabled: true, ChannelKey: "sk-chan-1"},
		},
		Sites: []model.Site{
			{ID: 1, Name: "test-site", Platform: model.SitePlatformNewAPI, BaseURL: "https://example.com", Enabled: true},
		},
		SiteAccounts: []model.SiteAccount{
			{ID: 1, SiteID: 1, Name: "account-1", CredentialType: model.SiteCredentialTypeAPIKey, Enabled: true, AutoSync: true},
			{ID: 2, SiteID: 1, Name: "account-2", CredentialType: model.SiteCredentialTypeAPIKey, Enabled: true, AutoSync: true},
			{ID: 3, SiteID: 1, Name: "account-3", CredentialType: model.SiteCredentialTypeAPIKey, Enabled: true, AutoSync: true},
		},
		SiteCredentials: []model.SiteCredential{
			{ID: 1, SiteAccountID: 1, Purpose: model.SiteCredentialPurposeChat, Name: "account", Token: "sk-1", ValueStatus: model.SiteTokenValueStatusReady, GroupKey: model.SiteDefaultGroupKey, GroupName: model.SiteDefaultGroupName, Enabled: true, Source: "account", IsDefault: true},
			{ID: 2, SiteAccountID: 2, Purpose: model.SiteCredentialPurposeChat, Name: "account", Token: "sk-2", ValueStatus: model.SiteTokenValueStatusReady, GroupKey: model.SiteDefaultGroupKey, GroupName: model.SiteDefaultGroupName, Enabled: true, Source: "account", IsDefault: true},
			{ID: 3, SiteAccountID: 3, Purpose: model.SiteCredentialPurposeChat, Name: "account", Token: "sk-3", ValueStatus: model.SiteTokenValueStatusReady, GroupKey: model.SiteDefaultGroupKey, GroupName: model.SiteDefaultGroupName, Enabled: true, Source: "account", IsDefault: true},
		},
		Groups: []model.Group{
			{ID: 1, Name: "test-group", Mode: 0},
		},
		GroupItems: []model.GroupItem{
			{ID: 1, GroupID: 1, ChannelID: 1, ModelName: "gpt-4", Priority: 1, Weight: 1},
		},
	}
}

func mustSprintf(format string, args ...any) string {
	return fmt.Sprintf(format, args...)
}

func TestDBExportZipContainsRelayLogsNDJSON(t *testing.T) {
	ctx := setupBackupTestDB(t)

	if err := dbpkg.GetDB().Create(&[]model.RelayLog{
		{ID: 1001, Time: 1, RequestModelName: "a", Success: true},
		{ID: 1002, Time: 2, RequestModelName: "b", Success: true},
	}).Error; err != nil {
		t.Fatalf("seed relay logs failed: %v", err)
	}

	var buf bytesBuffer
	if err := DBExportZip(ctx, &buf, true, false); err != nil {
		t.Fatalf("DBExportZip failed: %v", err)
	}

	zr, err := zipReaderFromBytes(buf.Bytes())
	if err != nil {
		t.Fatalf("zip open failed: %v", err)
	}
	names := make(map[string]bool)
	for _, f := range zr.File {
		names[f.Name] = true
	}
	for _, required := range []string{"manifest.json", "channels.json", "relay_logs.ndjson"} {
		if !names[required] {
			t.Fatalf("zip missing %q (have %v)", required, names)
		}
	}

	ndjson := readZipFile(t, zr, "relay_logs.ndjson")
	if ndjson == "" {
		t.Fatalf("relay_logs.ndjson is empty")
	}
	if linesCount(ndjson) != 2 {
		t.Fatalf("expected 2 ndjson lines, got %d (%q)", linesCount(ndjson), ndjson)
	}
}
