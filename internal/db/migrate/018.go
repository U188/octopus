package migrate

import (
	"fmt"

	"github.com/U188/octopus/internal/model"
	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 18,
		Up:      migrateSiteCredentials,
	})
}

func migrateSiteCredentials(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	if !db.Migrator().HasTable(&model.SiteAccount{}) {
		return nil
	}
	if !db.Migrator().HasTable(&model.SiteCredential{}) {
		if err := db.Migrator().CreateTable(&model.SiteCredential{}); err != nil {
			return err
		}
	}

	type legacyAccountCredential struct {
		ID             int
		AccessToken    string
		APIKey         string
		RefreshToken   string
		TokenExpiresAt int64
	}
	var accounts []legacyAccountCredential
	hasAccessToken := db.Migrator().HasColumn("site_accounts", "access_token")
	hasAPIKey := db.Migrator().HasColumn("site_accounts", "api_key")
	hasRefreshToken := db.Migrator().HasColumn("site_accounts", "refresh_token")
	hasTokenExpiresAt := db.Migrator().HasColumn("site_accounts", "token_expires_at")
	if hasAccessToken || hasAPIKey || hasRefreshToken || hasTokenExpiresAt {
		selects := "id"
		if hasAccessToken {
			selects += ", access_token"
		}
		if hasAPIKey {
			selects += ", api_key"
		}
		if hasRefreshToken {
			selects += ", refresh_token"
		}
		if hasTokenExpiresAt {
			selects += ", token_expires_at"
		}
		if err := db.Table("site_accounts").Select(selects).Scan(&accounts).Error; err != nil {
			return err
		}
	}
	accountAPIKeys := make(map[int]string, len(accounts))
	for _, account := range accounts {
		if account.APIKey != "" {
			accountAPIKeys[account.ID] = account.APIKey
		}
	}

	return db.Transaction(func(tx *gorm.DB) error {
		for _, account := range accounts {
			rows := []model.SiteCredential{}
			if account.AccessToken != "" {
				rows = append(rows, model.SiteCredential{
					SiteAccountID: account.ID,
					Purpose:       model.SiteCredentialPurposeSession,
					Name:          "session",
					Token:         account.AccessToken,
					ValueStatus:   model.SiteTokenValueStatusReady,
					Enabled:       true,
					Source:        "migration",
					ExpiresAt:     account.TokenExpiresAt,
				})
			}
			if account.APIKey != "" {
				rows = append(rows, model.SiteCredential{
					SiteAccountID: account.ID,
					Purpose:       model.SiteCredentialPurposeChat,
					Name:          "account",
					Token:         account.APIKey,
					ValueStatus:   model.SiteTokenValueStatusReady,
					GroupKey:      model.SiteDefaultGroupKey,
					GroupName:     model.SiteDefaultGroupName,
					Enabled:       true,
					Source:        "account",
					IsDefault:     true,
				})
			}
			if account.RefreshToken != "" {
				rows = append(rows, model.SiteCredential{
					SiteAccountID: account.ID,
					Purpose:       model.SiteCredentialPurposeRefresh,
					Name:          "refresh",
					Token:         account.RefreshToken,
					ValueStatus:   model.SiteTokenValueStatusReady,
					Enabled:       true,
					Source:        "migration",
				})
			}
			for _, row := range rows {
				var count int64
				if err := tx.Model(&model.SiteCredential{}).
					Where("site_account_id = ? AND purpose = ? AND value = ?", row.SiteAccountID, row.Purpose, row.Token).
					Count(&count).Error; err != nil {
					return err
				}
				if count > 0 {
					continue
				}
				if err := tx.Create(&row).Error; err != nil {
					return err
				}
			}
		}

		if tx.Migrator().HasTable("site_tokens") {
			type legacySiteToken struct {
				SiteAccountID int
				Name          string
				Token         string
				ValueStatus   model.SiteTokenValueStatus
				GroupKey      string
				GroupName     string
				Enabled       bool
				Source        string
				IsDefault     bool
			}
			var tokens []legacySiteToken
			if err := tx.Table("site_tokens").
				Select("site_account_id, name, token, value_status, group_key, group_name, enabled, source, is_default").
				Scan(&tokens).Error; err != nil {
				return err
			}
			for _, token := range tokens {
				if token.Token == "" {
					continue
				}
				row := model.SiteCredential{
					SiteAccountID: token.SiteAccountID,
					Purpose:       model.SiteCredentialPurposeChat,
					Name:          token.Name,
					Token:         token.Token,
					ValueStatus:   token.ValueStatus,
					GroupKey:      token.GroupKey,
					GroupName:     token.GroupName,
					Enabled:       token.Enabled,
					Source:        token.Source,
					IsDefault:     token.IsDefault,
				}
				if apiKey := accountAPIKeys[token.SiteAccountID]; apiKey != "" &&
					model.IsMaskedSiteTokenValue(row.Token) &&
					model.SiteMaskedTokenMatches(apiKey, row.Token) {
					row.Token = apiKey
					row.ValueStatus = model.SiteTokenValueStatusReady
					row.Enabled = true
				}
				var count int64
				if err := tx.Model(&model.SiteCredential{}).
					Where("site_account_id = ? AND purpose = ? AND value = ? AND group_key = ?", row.SiteAccountID, row.Purpose, row.Token, row.GroupKey).
					Count(&count).Error; err != nil {
					return err
				}
				if count > 0 {
					continue
				}
				if err := tx.Create(&row).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
}
