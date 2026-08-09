package op

import (
	"strconv"
	"testing"

	dbpkg "github.com/U188/octopus/internal/db"
	"github.com/U188/octopus/internal/model"
)

func TestSiteChannelBindingMapByChannelIDsBatchesLargeInput(t *testing.T) {
	ctx := setupSiteOpTestDB(t)
	site := model.Site{Name: "binding-batch-site", Platform: model.SitePlatformAPI, BaseURL: "https://example.com", Enabled: true}
	if err := dbpkg.GetDB().WithContext(ctx).Create(&site).Error; err != nil {
		t.Fatalf("create site: %v", err)
	}
	account := model.SiteAccount{SiteID: site.ID, Name: "binding-batch-account", CredentialType: model.SiteCredentialTypeAPIKey, Enabled: true}
	if err := dbpkg.GetDB().WithContext(ctx).Create(&account).Error; err != nil {
		t.Fatalf("create account: %v", err)
	}

	const count = siteChannelBindingQueryBatchSize*2 + 17
	channels := make([]model.Channel, count)
	for i := range channels {
		channels[i] = model.Channel{Name: "binding-batch-channel-" + strconv.Itoa(i)}
	}
	if err := dbpkg.GetDB().WithContext(ctx).CreateInBatches(&channels, 200).Error; err != nil {
		t.Fatalf("create channels: %v", err)
	}

	bindings := make([]model.SiteChannelBinding, count)
	ids := make([]int, 0, count+3)
	ids = append(ids, 0, channels[0].ID, channels[0].ID)
	for i := range channels {
		bindings[i] = model.SiteChannelBinding{
			SiteID:        site.ID,
			SiteAccountID: account.ID,
			ChannelID:     channels[i].ID,
			GroupKey:      "group-" + strconv.Itoa(i),
		}
		ids = append(ids, channels[i].ID)
	}
	if err := dbpkg.GetDB().WithContext(ctx).CreateInBatches(&bindings, 200).Error; err != nil {
		t.Fatalf("create bindings: %v", err)
	}

	result, err := SiteChannelBindingMapByChannelIDs(ids, ctx)
	if err != nil {
		t.Fatalf("load binding map: %v", err)
	}
	if len(result) != count {
		t.Fatalf("binding map size = %d, want %d", len(result), count)
	}
	if got := result[channels[count-1].ID].GroupKey; got != "group-"+strconv.Itoa(count-1) {
		t.Fatalf("last binding group = %q", got)
	}
}
