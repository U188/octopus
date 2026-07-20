package op

import (
	"context"
	"path/filepath"
	"testing"

	dbpkg "github.com/U188/octopus/internal/db"
	"github.com/U188/octopus/internal/model"
)

func TestChannelCreateManagedRollsBackChannelWhenBindingFails(t *testing.T) {
	if dbpkg.GetDB() != nil {
		_ = dbpkg.Close()
	}
	if err := dbpkg.InitDB("sqlite", filepath.Join(t.TempDir(), "managed-channel.db"), false); err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	t.Cleanup(func() { _ = dbpkg.Close() })
	ctx := context.Background()

	site := model.Site{Name: "managed-test", Platform: model.SitePlatformAPI, BaseURL: "https://example.com", Enabled: true}
	if err := dbpkg.GetDB().WithContext(ctx).Create(&site).Error; err != nil {
		t.Fatalf("create site: %v", err)
	}
	account := model.SiteAccount{SiteID: site.ID, Name: "account", CredentialType: model.SiteCredentialTypeAPIKey, Enabled: true}
	if err := dbpkg.GetDB().WithContext(ctx).Create(&account).Error; err != nil {
		t.Fatalf("create account: %v", err)
	}

	existing := model.Channel{Name: "existing-managed", Enabled: true}
	if err := ChannelCreate(&existing, ctx); err != nil {
		t.Fatalf("create existing channel: %v", err)
	}
	existingBinding := model.SiteChannelBinding{
		SiteID: site.ID, SiteAccountID: account.ID,
		GroupKey: model.SiteDefaultGroupKey, ChannelID: existing.ID,
	}
	if err := dbpkg.GetDB().WithContext(ctx).Create(&existingBinding).Error; err != nil {
		t.Fatalf("create existing binding: %v", err)
	}

	candidate := model.Channel{Name: "must-rollback", Enabled: true}
	conflictingBinding := model.SiteChannelBinding{
		SiteID: site.ID, SiteAccountID: account.ID,
		GroupKey: model.SiteDefaultGroupKey,
	}
	if err := ChannelCreateManaged(&candidate, &conflictingBinding, 0, ctx); err == nil {
		t.Fatal("expected unique binding conflict")
	}

	var count int64
	if err := dbpkg.GetDB().WithContext(ctx).Model(&model.Channel{}).Where("name = ?", candidate.Name).Count(&count).Error; err != nil {
		t.Fatalf("count rolled-back channel: %v", err)
	}
	if count != 0 {
		t.Fatalf("channel survived failed binding transaction: count=%d", count)
	}
}
