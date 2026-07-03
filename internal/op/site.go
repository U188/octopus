package op

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/U188/octopus/internal/db"
	"github.com/U188/octopus/internal/model"
	"gorm.io/gorm"
)

func SiteList(ctx context.Context) ([]model.Site, error) {
	var sites []model.Site
	if err := db.GetDB().WithContext(ctx).
		Preload("Accounts").
		Preload("Accounts.Tokens", "purpose = ?", model.SiteCredentialPurposeChat).
		Preload("Accounts.Credentials", "purpose <> ?", model.SiteCredentialPurposeChat).
		Preload("Accounts.UserGroups").
		Preload("Accounts.Models").
		Preload("Accounts.ChannelBindings").
		Where("archived = ?", false).
		Order("is_pinned DESC, sort_order ASC, id ASC").
		Find(&sites).Error; err != nil {
		return nil, err
	}
	for i := range sites {
		normalizeSiteProxyFields(&sites[i])
		hydrateSiteAccountCredentialViews(&sites[i])
	}
	return sites, nil
}

func SiteListArchived(ctx context.Context) ([]model.Site, error) {
	var sites []model.Site
	if err := db.GetDB().WithContext(ctx).
		Preload("Accounts").
		Preload("Accounts.Tokens", "purpose = ?", model.SiteCredentialPurposeChat).
		Preload("Accounts.Credentials", "purpose <> ?", model.SiteCredentialPurposeChat).
		Preload("Accounts.UserGroups").
		Preload("Accounts.Models").
		Preload("Accounts.ChannelBindings").
		Where("archived = ?", true).
		Order("archived_at DESC, id ASC").
		Find(&sites).Error; err != nil {
		return nil, err
	}
	for i := range sites {
		normalizeSiteProxyFields(&sites[i])
		hydrateSiteAccountCredentialViews(&sites[i])
	}
	return sites, nil
}

func SiteGet(id int, ctx context.Context) (*model.Site, error) {
	var site model.Site
	if err := db.GetDB().WithContext(ctx).
		Preload("Accounts").
		Preload("Accounts.Tokens", "purpose = ?", model.SiteCredentialPurposeChat).
		Preload("Accounts.Credentials", "purpose <> ?", model.SiteCredentialPurposeChat).
		Preload("Accounts.UserGroups").
		Preload("Accounts.Models").
		Preload("Accounts.ChannelBindings").
		First(&site, id).Error; err != nil {
		return nil, err
	}
	normalizeSiteProxyFields(&site)
	hydrateSiteAccountCredentialViews(&site)
	return &site, nil
}

func normalizeSiteProxyFields(site *model.Site) {
	if site == nil {
		return
	}
	if site.ProxyMode == "" {
		site.ProxyMode = model.ProxyUsageModeDirect
	}
	if site.ProxyMode != model.ProxyUsageModePool {
		site.ProxyConfigID = nil
	}
	site.Proxy = site.ProxyMode != model.ProxyUsageModeDirect
	site.UseSystemProxy = site.ProxyMode == model.ProxyUsageModeSystem
	site.SiteProxy = nil
	for i := range site.Accounts {
		normalizeSiteAccountProxyFields(&site.Accounts[i])
	}
}

func hydrateSiteAccountCredentialViews(site *model.Site) {
	if site == nil {
		return
	}
	for i := range site.Accounts {
		hydrateSiteAccountCredentialView(&site.Accounts[i])
	}
}

func hydrateSiteAccountCredentialView(account *model.SiteAccount) {
	if account == nil {
		return
	}
	account.AccessToken = ""
	account.APIKey = ""
	account.RefreshToken = ""
	account.TokenExpiresAt = 0
	for _, credential := range account.Credentials {
		if !credential.Enabled && credential.Purpose != model.SiteCredentialPurposeSession && credential.Purpose != model.SiteCredentialPurposeRefresh {
			continue
		}
		switch credential.Purpose {
		case model.SiteCredentialPurposeSession:
			if account.AccessToken == "" {
				account.AccessToken = strings.TrimSpace(credential.Token)
				account.TokenExpiresAt = credential.ExpiresAt
			}
		case model.SiteCredentialPurposeRefresh:
			if account.RefreshToken == "" {
				account.RefreshToken = strings.TrimSpace(credential.Token)
			}
		}
	}
	account.APIKey = firstAccountAPIKeyCredential(account.Tokens)
}

func firstAccountAPIKeyCredential(tokens []model.SiteToken) string {
	for _, token := range tokens {
		if token.Purpose != "" && token.Purpose != model.SiteCredentialPurposeChat {
			continue
		}
		if strings.TrimSpace(token.Source) != "account" {
			continue
		}
		if token.IsDefault || model.NormalizeSiteGroupKey(token.GroupKey) == model.SiteDefaultGroupKey {
			return strings.TrimSpace(token.Token)
		}
	}
	return ""
}

func normalizeSiteAccountProxyFields(account *model.SiteAccount) {
	if account == nil {
		return
	}
	if account.ProxyMode == "" {
		account.ProxyMode = model.ProxyUsageModeInherit
	}
	if account.ProxyMode != model.ProxyUsageModePool {
		account.ProxyConfigID = nil
	}
	account.AccountProxy = nil
}

func SiteCreate(site *model.Site, ctx context.Context) error {
	if site == nil {
		return fmt.Errorf("site is nil")
	}
	if err := site.Validate(); err != nil {
		return err
	}
	if site.ProxyMode == model.ProxyUsageModePool && site.ProxyConfigID != nil {
		if _, err := ProxyURLForConfig(*site.ProxyConfigID, ctx); err != nil {
			return err
		}
	}
	if site.EnabledSet && !site.Enabled {
		err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := tx.Create(site).Error; err != nil {
				return err
			}
			return tx.Model(&model.Site{}).Where("id = ?", site.ID).Update("enabled", false).Error
		})
		site.Enabled = false
		return err
	}
	return db.GetDB().WithContext(ctx).Create(site).Error
}

func SiteUpdate(req *model.SiteUpdateRequest, ctx context.Context) (*model.Site, error) {
	if req == nil {
		return nil, fmt.Errorf("site update request is nil")
	}
	var site model.Site
	if err := db.GetDB().WithContext(ctx).First(&site, req.ID).Error; err != nil {
		return nil, fmt.Errorf("site not found")
	}

	merged := site
	var selectFields []string
	updates := model.Site{ID: req.ID}

	if req.Name != nil {
		merged.Name = *req.Name
		selectFields = append(selectFields, "name")
	}
	if req.Platform != nil {
		merged.Platform = *req.Platform
		selectFields = append(selectFields, "platform")
	}
	if req.BaseURL != nil {
		merged.BaseURL = *req.BaseURL
		selectFields = append(selectFields, "base_url")
	}
	if req.Enabled != nil {
		merged.Enabled = *req.Enabled
		selectFields = append(selectFields, "enabled")
	}
	if req.ProxyMode != nil {
		merged.ProxyMode = *req.ProxyMode
		selectFields = append(selectFields, "proxy_mode")
	}
	if req.ProxyConfigIDSet || (req.ProxyMode != nil && *req.ProxyMode != model.ProxyUsageModePool) {
		if req.ProxyMode != nil && *req.ProxyMode != model.ProxyUsageModePool {
			merged.ProxyConfigID = nil
		} else {
			merged.ProxyConfigID = req.ProxyConfigID
		}
		selectFields = append(selectFields, "proxy_config_id")
	}
	if req.ExternalCheckinSet {
		merged.ExternalCheckinURL = req.ExternalCheckinURL
		selectFields = append(selectFields, "external_checkin_url")
	}
	if req.IsPinned != nil {
		merged.IsPinned = *req.IsPinned
		selectFields = append(selectFields, "is_pinned")
	}
	if req.SortOrder != nil {
		merged.SortOrder = *req.SortOrder
		selectFields = append(selectFields, "sort_order")
	}
	if req.GlobalWeight != nil {
		merged.GlobalWeight = *req.GlobalWeight
		selectFields = append(selectFields, "global_weight")
	}
	if req.CustomHeader != nil {
		merged.CustomHeader = *req.CustomHeader
		selectFields = append(selectFields, "custom_header")
	}
	if req.RouteBaseURLs != nil {
		merged.RouteBaseURLs = *req.RouteBaseURLs
		selectFields = append(selectFields, "route_base_urls")
	}
	if req.CodexMode != nil {
		merged.CodexMode = *req.CodexMode
		selectFields = append(selectFields, "codex_mode")
	}
	if req.ClaudeMode != nil {
		merged.ClaudeMode = *req.ClaudeMode
		selectFields = append(selectFields, "claude_mode")
	}
	if req.Tags != nil {
		merged.Tags = *req.Tags
		selectFields = append(selectFields, "tags")
	}
	if len(selectFields) > 0 {
		if err := merged.Validate(); err != nil {
			return nil, err
		}
		if merged.ProxyMode == model.ProxyUsageModePool && merged.ProxyConfigID != nil {
			if _, err := ProxyURLForConfig(*merged.ProxyConfigID, ctx); err != nil {
				return nil, err
			}
		}
	}
	if req.Name != nil {
		updates.Name = merged.Name
	}
	if req.Platform != nil {
		updates.Platform = merged.Platform
	}
	if req.BaseURL != nil {
		updates.BaseURL = merged.BaseURL
	}
	if req.Enabled != nil {
		updates.Enabled = merged.Enabled
	}
	if req.ProxyMode != nil {
		updates.ProxyMode = merged.ProxyMode
	}
	if req.ProxyConfigIDSet || (req.ProxyMode != nil && *req.ProxyMode != model.ProxyUsageModePool) {
		updates.ProxyConfigID = merged.ProxyConfigID
	}
	if req.ExternalCheckinSet {
		updates.ExternalCheckinURL = merged.ExternalCheckinURL
	}
	if req.IsPinned != nil {
		updates.IsPinned = merged.IsPinned
	}
	if req.SortOrder != nil {
		updates.SortOrder = merged.SortOrder
	}
	if req.GlobalWeight != nil {
		updates.GlobalWeight = merged.GlobalWeight
	}
	if req.CustomHeader != nil {
		updates.CustomHeader = merged.CustomHeader
	}
	if req.RouteBaseURLs != nil {
		updates.RouteBaseURLs = merged.RouteBaseURLs
	}
	if req.CodexMode != nil {
		updates.CodexMode = merged.CodexMode
	}
	if req.ClaudeMode != nil {
		updates.ClaudeMode = merged.ClaudeMode
	}
	if req.Tags != nil {
		updates.Tags = merged.Tags
	}
	if len(selectFields) > 0 {
		if err := db.GetDB().WithContext(ctx).
			Model(&model.Site{}).
			Where("id = ?", req.ID).
			Select(selectFields).
			Updates(&updates).Error; err != nil {
			return nil, fmt.Errorf("failed to update site: %w", err)
		}
	}
	return SiteGet(req.ID, ctx)
}

// mergeHeaders 将 upserts 合并进 existing：按 header key 大小写不敏感匹配，命中则仅
// 更新值并保留已存的原始 key 大小写，未命中则追加；随后按 deleteKeys（大小写不敏感）
// 删除，delete 在 upsert 之后执行（优先）。输出顺序稳定，空白 key 跳过。
func mergeHeaders(existing, upserts []model.CustomHeader, deleteKeys []string) []model.CustomHeader {
	order := make([]string, 0, len(existing)+len(upserts))
	byLower := make(map[string]model.CustomHeader, len(existing)+len(upserts))

	put := func(key, value string) {
		k := strings.TrimSpace(key)
		if k == "" {
			return
		}
		lk := strings.ToLower(k)
		if cur, ok := byLower[lk]; ok {
			cur.HeaderValue = value
			byLower[lk] = cur
			return
		}
		order = append(order, lk)
		byLower[lk] = model.CustomHeader{HeaderKey: k, HeaderValue: value}
	}

	for _, h := range existing {
		put(h.HeaderKey, h.HeaderValue)
	}
	for _, u := range upserts {
		put(u.HeaderKey, strings.TrimSpace(u.HeaderValue))
	}
	for _, dk := range deleteKeys {
		lk := strings.ToLower(strings.TrimSpace(dk))
		if lk == "" {
			continue
		}
		delete(byLower, lk)
	}

	out := make([]model.CustomHeader, 0, len(order))
	for _, lk := range order {
		if h, ok := byLower[lk]; ok {
			out = append(out, h)
		}
	}
	return out
}

// SiteBatchEdit 将一组修改补丁（添加/移除标签、设置/删除 Header）逐站点合并落库，
// 每站点一次加载、一次更新；单站失败计入 FailedItems 后继续。
// 标签先添加后移除（同名时移除优先），传入的标签需已规范化。
// 返回结果与需要刷新投影的站点 ID（仅 Header 变更影响投影）。
func SiteBatchEdit(req *model.SiteBatchEditRequest, ctx context.Context) (*model.SiteBatchResult, []int, error) {
	if req == nil {
		return nil, nil, fmt.Errorf("site batch edit request is nil")
	}
	editTags := len(req.AddTags) > 0 || len(req.RemoveTags) > 0
	editHeader := len(req.Upserts) > 0 || len(req.DeleteKeys) > 0
	if !editTags && !editHeader {
		return nil, nil, fmt.Errorf("nothing to edit")
	}
	removed := make(map[string]struct{}, len(req.RemoveTags))
	for _, tag := range req.RemoveTags {
		removed[tag] = struct{}{}
	}
	result := &model.SiteBatchResult{
		SuccessIDs:  make([]int, 0, len(req.IDs)),
		FailedItems: make([]model.SiteBatchFailure, 0),
	}
	affected := make([]int, 0, len(req.IDs))
	for _, id := range req.IDs {
		err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var site model.Site
			if err := tx.First(&site, id).Error; err != nil {
				return fmt.Errorf("site not found")
			}
			columns := make([]string, 0, 2)
			updates := model.Site{ID: id}
			if editTags {
				merged := make([]string, 0, len(site.Tags)+len(req.AddTags))
				for _, tag := range site.Tags {
					if _, ok := removed[tag]; !ok {
						merged = append(merged, tag)
					}
				}
				for _, tag := range req.AddTags {
					if _, ok := removed[tag]; !ok {
						merged = append(merged, tag)
					}
				}
				next := model.NormalizeSiteTags(merged)
				if err := model.ValidateSiteTags(next); err != nil {
					return err
				}
				updates.Tags = next
				columns = append(columns, "tags")
			}
			if editHeader {
				updates.CustomHeader = mergeHeaders(site.CustomHeader, req.Upserts, req.DeleteKeys)
				columns = append(columns, "custom_header")
			}
			return tx.Model(&model.Site{}).
				Where("id = ?", id).
				Select(columns).
				Updates(&updates).Error
		})
		if err != nil {
			result.FailedItems = append(result.FailedItems, model.SiteBatchFailure{ID: id, Message: err.Error()})
			continue
		}
		result.SuccessIDs = append(result.SuccessIDs, id)
		if editHeader {
			affected = append(affected, id)
		}
	}
	return result, affected, nil
}

// SiteBatchApply 对一组站点逐个执行批量动作（enable/disable/delete），
// 单站失败计入 FailedItems 后继续。deleteSite 由调用方注入，避免 op 反向依赖站点同步层。
// 返回结果与需要刷新投影的站点 ID（仅 enable/disable 影响投影）。
func SiteBatchApply(req *model.SiteBatchRequest, deleteSite func(context.Context, int) error, ctx context.Context) (*model.SiteBatchResult, []int, error) {
	if req == nil {
		return nil, nil, fmt.Errorf("site batch request is nil")
	}
	result := &model.SiteBatchResult{
		SuccessIDs:  make([]int, 0, len(req.IDs)),
		FailedItems: make([]model.SiteBatchFailure, 0),
	}
	for _, id := range req.IDs {
		var err error
		switch req.Action {
		case "enable":
			err = SiteEnabled(id, true, ctx)
		case "disable":
			err = SiteEnabled(id, false, ctx)
		case "delete":
			err = deleteSite(ctx, id)
		default:
			err = fmt.Errorf("invalid action: %s", req.Action)
		}
		if err != nil {
			result.FailedItems = append(result.FailedItems, model.SiteBatchFailure{ID: id, Message: err.Error()})
			continue
		}
		result.SuccessIDs = append(result.SuccessIDs, id)
	}
	var affected []int
	if req.Action == "enable" || req.Action == "disable" {
		affected = result.SuccessIDs
	}
	return result, affected, nil
}

func SiteEnabled(id int, enabled bool, ctx context.Context) error {
	return db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.Site{}).Where("id = ?", id).Update("enabled", enabled).Error; err != nil {
			return err
		}
		return tx.Model(&model.SiteAccount{}).Where("site_id = ?", id).Update("enabled", enabled).Error
	})
}

func SiteDel(id int, ctx context.Context) error {
	var affectedAccountIDs []int
	if err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var accountIDs []int
		if err := tx.Model(&model.SiteAccount{}).Where("site_id = ?", id).Pluck("id", &accountIDs).Error; err != nil {
			return err
		}
		affectedAccountIDs = accountIDs
		if len(accountIDs) > 0 {
			// Delete bindings before groups/accounts so FK-constrained databases do not
			// reject removing rows that bindings may still reference.
			if err := tx.Where("site_account_id IN ?", accountIDs).Delete(&model.SiteChannelBinding{}).Error; err != nil {
				return err
			}
			if err := tx.Where("site_account_id IN ?", accountIDs).Delete(&model.SiteToken{}).Error; err != nil {
				return err
			}
			if err := tx.Where("site_account_id IN ?", accountIDs).Delete(&model.SiteUserGroup{}).Error; err != nil {
				return err
			}
			if err := tx.Where("site_account_id IN ?", accountIDs).Delete(&model.SiteModel{}).Error; err != nil {
				return err
			}
			if err := tx.Where("site_account_id IN ?", accountIDs).Delete(&model.StatsSiteModelHourly{}).Error; err != nil {
				return err
			}
			if err := deleteLegacySitePricesByAccountIDs(tx, accountIDs); err != nil {
				return err
			}
			if err := tx.Where("id IN ?", accountIDs).Delete(&model.SiteAccount{}).Error; err != nil {
				return err
			}
		}
		return tx.Delete(&model.Site{}, id).Error
	}); err != nil {
		return err
	}
	if len(affectedAccountIDs) > 0 {
		invalidateSiteBindingCache()
		deleteSiteModelHourlyCacheForAccounts(affectedAccountIDs)
	}
	return nil
}

func SiteArchive(id int, ctx context.Context) error {
	now := time.Now()
	return db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.Site{}).Where("id = ?", id).Updates(map[string]any{
			"archived":    true,
			"archived_at": &now,
			"enabled":     false,
		}).Error; err != nil {
			return err
		}
		return tx.Model(&model.SiteAccount{}).Where("site_id = ?", id).Update("enabled", false).Error
	})
}

func SiteRestore(id int, ctx context.Context) error {
	return db.GetDB().WithContext(ctx).Model(&model.Site{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"archived":    false,
			"archived_at": gorm.Expr("NULL"),
		}).Error
}

func SiteAccountGet(id int, ctx context.Context) (*model.SiteAccount, error) {
	var account model.SiteAccount
	if err := db.GetDB().WithContext(ctx).
		Preload("Tokens", "purpose = ?", model.SiteCredentialPurposeChat).
		Preload("Credentials", "purpose <> ?", model.SiteCredentialPurposeChat).
		Preload("UserGroups").
		Preload("Models").
		Preload("ChannelBindings").
		First(&account, id).Error; err != nil {
		return nil, err
	}
	normalizeSiteAccountProxyFields(&account)
	hydrateSiteAccountCredentialView(&account)
	return &account, nil
}

func SiteAccountCreate(account *model.SiteAccount, ctx context.Context) error {
	if account == nil {
		return fmt.Errorf("site account is nil")
	}
	if err := account.Validate(); err != nil {
		return err
	}
	if account.ProxyMode == model.ProxyUsageModePool && account.ProxyConfigID != nil {
		if _, err := ProxyURLForConfig(*account.ProxyConfigID, ctx); err != nil {
			return err
		}
	}
	if (account.EnabledSet && !account.Enabled) || (account.AutoSyncSet && !account.AutoSync) || (account.AutoCheckinSet && !account.AutoCheckin) {
		explicitEnabled := account.Enabled
		explicitAutoSync := account.AutoSync
		explicitAutoCheckin := account.AutoCheckin
		updates := map[string]any{}
		if account.EnabledSet && !account.Enabled {
			updates["enabled"] = false
		}
		if account.AutoSyncSet && !account.AutoSync {
			updates["auto_sync"] = false
		}
		if account.AutoCheckinSet && !account.AutoCheckin {
			updates["auto_checkin"] = false
		}
		err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := tx.Omit("Tokens", "Credentials").Create(account).Error; err != nil {
				return err
			}
			if err := tx.Model(&model.SiteAccount{}).Where("id = ?", account.ID).Updates(updates).Error; err != nil {
				return err
			}
			return persistAccountFieldCredentialsTx(tx, account.ID, account.AccessToken, account.APIKey, account.RefreshToken, account.TokenExpiresAt, true, true, true, true)
		})
		if account.EnabledSet {
			account.Enabled = explicitEnabled
		}
		if account.AutoSyncSet {
			account.AutoSync = explicitAutoSync
		}
		if account.AutoCheckinSet {
			account.AutoCheckin = explicitAutoCheckin
		}
		return err
	}
	return db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Omit("Tokens", "Credentials").Create(account).Error; err != nil {
			return err
		}
		return persistAccountFieldCredentialsTx(tx, account.ID, account.AccessToken, account.APIKey, account.RefreshToken, account.TokenExpiresAt, true, true, true, true)
	})
}

func SiteAccountUpdate(req *model.SiteAccountUpdateRequest, ctx context.Context) (*model.SiteAccount, error) {
	if req == nil {
		return nil, fmt.Errorf("site account update request is nil")
	}

	var account model.SiteAccount
	if err := db.GetDB().WithContext(ctx).First(&account, req.ID).Error; err != nil {
		return nil, fmt.Errorf("site account not found")
	}
	if loaded, err := SiteAccountGet(req.ID, ctx); err == nil {
		account = *loaded
	}

	merged := account
	var selectFields []string
	updates := model.SiteAccount{ID: req.ID}

	if req.Name != nil {
		merged.Name = *req.Name
		selectFields = append(selectFields, "name")
	}
	if req.CredentialType != nil {
		merged.CredentialType = *req.CredentialType
		selectFields = append(selectFields, "credential_type")
	}
	if req.Username != nil {
		merged.Username = *req.Username
		selectFields = append(selectFields, "username")
	}
	if req.Password != nil {
		merged.Password = *req.Password
		selectFields = append(selectFields, "password")
	}
	if req.AccessToken != nil {
		merged.AccessToken = *req.AccessToken
	}
	if req.APIKey != nil {
		merged.APIKey = *req.APIKey
	}
	if req.RefreshToken != nil {
		merged.RefreshToken = *req.RefreshToken
	}
	if req.TokenExpiresAt != nil {
		merged.TokenExpiresAt = *req.TokenExpiresAt
	}
	if req.PlatformUserIDSet {
		merged.PlatformUserID = req.PlatformUserID
		selectFields = append(selectFields, "platform_user_id")
	}
	if req.ProxyMode != nil {
		merged.ProxyMode = *req.ProxyMode
		selectFields = append(selectFields, "proxy_mode")
	}
	if req.ProxyConfigIDSet || (req.ProxyMode != nil && *req.ProxyMode != model.ProxyUsageModePool) {
		if req.ProxyMode != nil && *req.ProxyMode != model.ProxyUsageModePool {
			merged.ProxyConfigID = nil
		} else {
			merged.ProxyConfigID = req.ProxyConfigID
		}
		selectFields = append(selectFields, "proxy_config_id")
	}
	if req.Enabled != nil {
		merged.Enabled = *req.Enabled
		selectFields = append(selectFields, "enabled")
	}
	if req.AutoSync != nil {
		merged.AutoSync = *req.AutoSync
		selectFields = append(selectFields, "auto_sync")
	}
	if req.AutoCheckin != nil {
		merged.AutoCheckin = *req.AutoCheckin
		selectFields = append(selectFields, "auto_checkin")
	}
	if req.RandomCheckin != nil {
		merged.RandomCheckin = *req.RandomCheckin
		selectFields = append(selectFields, "random_checkin")
	}
	if req.CheckinIntervalHours != nil {
		merged.CheckinIntervalHours = *req.CheckinIntervalHours
		selectFields = append(selectFields, "checkin_interval_hours")
	}
	if req.CheckinRandomWindowMinutes != nil {
		merged.CheckinRandomWindowMinutes = *req.CheckinRandomWindowMinutes
		selectFields = append(selectFields, "checkin_random_window_minutes")
	}

	if len(selectFields) > 0 {
		if err := merged.Validate(); err != nil {
			return nil, err
		}
		if merged.ProxyMode == model.ProxyUsageModePool && merged.ProxyConfigID != nil {
			if _, err := ProxyURLForConfig(*merged.ProxyConfigID, ctx); err != nil {
				return nil, err
			}
		}
	}
	if req.Name != nil {
		updates.Name = merged.Name
	}
	if req.CredentialType != nil {
		updates.CredentialType = merged.CredentialType
	}
	if req.Username != nil {
		updates.Username = merged.Username
	}
	if req.Password != nil {
		updates.Password = merged.Password
	}
	if req.AccessToken != nil {
		updates.AccessToken = merged.AccessToken
	}
	if req.APIKey != nil {
		updates.APIKey = merged.APIKey
	}
	if req.RefreshToken != nil {
		updates.RefreshToken = merged.RefreshToken
	}
	if req.TokenExpiresAt != nil {
		updates.TokenExpiresAt = merged.TokenExpiresAt
	}
	if req.PlatformUserIDSet {
		updates.PlatformUserID = merged.PlatformUserID
	}
	if req.ProxyMode != nil {
		updates.ProxyMode = merged.ProxyMode
	}
	if req.ProxyConfigIDSet || (req.ProxyMode != nil && *req.ProxyMode != model.ProxyUsageModePool) {
		updates.ProxyConfigID = merged.ProxyConfigID
	}
	if req.Enabled != nil {
		updates.Enabled = merged.Enabled
	}
	if req.AutoSync != nil {
		updates.AutoSync = merged.AutoSync
	}
	if req.AutoCheckin != nil {
		updates.AutoCheckin = merged.AutoCheckin
	}
	if req.RandomCheckin != nil {
		updates.RandomCheckin = merged.RandomCheckin
	}
	if req.CheckinIntervalHours != nil {
		updates.CheckinIntervalHours = merged.CheckinIntervalHours
	}
	if req.CheckinRandomWindowMinutes != nil {
		updates.CheckinRandomWindowMinutes = merged.CheckinRandomWindowMinutes
	}

	if len(selectFields) > 0 || req.AccessToken != nil || req.APIKey != nil || req.RefreshToken != nil || req.TokenExpiresAt != nil {
		if err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if len(selectFields) > 0 {
				if err := tx.
					Model(&model.SiteAccount{}).
					Where("id = ?", req.ID).
					Select(selectFields).
					Updates(&updates).Error; err != nil {
					return fmt.Errorf("failed to update site account: %w", err)
				}
			}
			return persistAccountFieldCredentialsTx(
				tx,
				req.ID,
				merged.AccessToken,
				merged.APIKey,
				merged.RefreshToken,
				merged.TokenExpiresAt,
				req.AccessToken != nil,
				req.APIKey != nil,
				req.RefreshToken != nil,
				req.TokenExpiresAt != nil,
			)
		}); err != nil {
			return nil, err
		}
	}
	return SiteAccountGet(req.ID, ctx)
}

func persistAccountFieldCredentialsTx(tx *gorm.DB, accountID int, accessToken string, apiKey string, refreshToken string, tokenExpiresAt int64, setAccessToken bool, setAPIKey bool, setRefreshToken bool, setTokenExpiresAt bool) error {
	if tx == nil {
		return fmt.Errorf("db transaction is nil")
	}
	if setAccessToken || setTokenExpiresAt {
		if err := replaceAccountCredentialTx(tx, model.SiteCredential{
			SiteAccountID: accountID,
			Purpose:       model.SiteCredentialPurposeSession,
			Name:          "session",
			Token:         strings.TrimSpace(accessToken),
			ValueStatus:   model.SiteTokenValueStatusReady,
			Enabled:       true,
			Source:        "account",
			ExpiresAt:     tokenExpiresAt,
		}, setAccessToken); err != nil {
			return err
		}
	}
	if setAPIKey {
		if err := tx.Where("site_account_id = ? AND purpose = ? AND source = ?", accountID, model.SiteCredentialPurposeChat, "account").Delete(&model.SiteCredential{}).Error; err != nil {
			return err
		}
		apiKey = strings.TrimSpace(apiKey)
		if apiKey != "" {
			row := model.SiteCredential{
				SiteAccountID: accountID,
				Purpose:       model.SiteCredentialPurposeChat,
				Name:          "account",
				Token:         apiKey,
				ValueStatus:   model.SiteTokenValueStatusReady,
				GroupKey:      model.SiteDefaultGroupKey,
				GroupName:     model.SiteDefaultGroupName,
				Enabled:       true,
				Source:        "account",
				IsDefault:     true,
			}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
		}
	}
	if setRefreshToken {
		if err := replaceAccountCredentialTx(tx, model.SiteCredential{
			SiteAccountID: accountID,
			Purpose:       model.SiteCredentialPurposeRefresh,
			Name:          "refresh",
			Token:         strings.TrimSpace(refreshToken),
			ValueStatus:   model.SiteTokenValueStatusReady,
			Enabled:       true,
			Source:        "account",
		}, true); err != nil {
			return err
		}
	}
	return nil
}

func replaceAccountCredentialTx(tx *gorm.DB, row model.SiteCredential, replaceValue bool) error {
	if row.SiteAccountID == 0 || row.Purpose == "" {
		return fmt.Errorf("invalid site credential")
	}
	var existing model.SiteCredential
	err := tx.Where("site_account_id = ? AND purpose = ?", row.SiteAccountID, row.Purpose).First(&existing).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if strings.TrimSpace(row.Token) == "" && replaceValue {
		return tx.Where("site_account_id = ? AND purpose = ?", row.SiteAccountID, row.Purpose).Delete(&model.SiteCredential{}).Error
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		if strings.TrimSpace(row.Token) == "" {
			return nil
		}
		return tx.Create(&row).Error
	}
	updates := map[string]any{
		"name":         row.Name,
		"enabled":      row.Enabled,
		"source":       row.Source,
		"value_status": row.ValueStatus,
		"expires_at":   row.ExpiresAt,
	}
	if replaceValue {
		updates["value"] = strings.TrimSpace(row.Token)
	}
	return tx.Model(&model.SiteCredential{}).Where("id = ?", existing.ID).Updates(updates).Error
}

func SiteAccountEnabled(id int, enabled bool, ctx context.Context) error {
	return db.GetDB().WithContext(ctx).Model(&model.SiteAccount{}).Where("id = ?", id).Update("enabled", enabled).Error
}

func deleteLegacySitePricesByAccountIDs(tx *gorm.DB, accountIDs []int) error {
	if tx == nil || len(accountIDs) == 0 {
		return nil
	}
	if !tx.Migrator().HasTable("site_prices") {
		return nil
	}
	if err := tx.Exec("DELETE FROM site_prices WHERE site_account_id IN ?", accountIDs).Error; err != nil {
		return fmt.Errorf("failed to delete legacy site prices: %w", err)
	}
	return nil
}

func SiteAccountDel(id int, ctx context.Context) error {
	if err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Delete bindings before groups/accounts so FK-constrained databases do not
		// reject removing rows that bindings may still reference.
		if err := tx.Where("site_account_id = ?", id).Delete(&model.SiteChannelBinding{}).Error; err != nil {
			return err
		}
		if err := tx.Where("site_account_id = ?", id).Delete(&model.SiteToken{}).Error; err != nil {
			return err
		}
		if err := tx.Where("site_account_id = ?", id).Delete(&model.SiteCredential{}).Error; err != nil {
			return err
		}
		if err := tx.Where("site_account_id = ?", id).Delete(&model.SiteUserGroup{}).Error; err != nil {
			return err
		}
		if err := tx.Where("site_account_id = ?", id).Delete(&model.SiteModel{}).Error; err != nil {
			return err
		}
		if err := tx.Where("site_account_id = ?", id).Delete(&model.StatsSiteModelHourly{}).Error; err != nil {
			return err
		}
		if err := deleteLegacySitePricesByAccountIDs(tx, []int{id}); err != nil {
			return err
		}
		return tx.Delete(&model.SiteAccount{}, id).Error
	}); err != nil {
		return err
	}
	invalidateSiteBindingCache()
	deleteSiteModelHourlyCacheForAccounts([]int{id})
	return nil
}

func SiteAvailableModels(siteID int, ctx context.Context) ([]string, error) {
	var rows []model.SiteModel
	if err := db.GetDB().WithContext(ctx).
		Joins("JOIN site_accounts ON site_accounts.id = site_models.site_account_id").
		Where("site_accounts.site_id = ? AND site_models.disabled = ?", siteID, false).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	seen := make(map[string]struct{})
	models := make([]string, 0, len(rows))
	for _, row := range rows {
		trimmed := strings.TrimSpace(row.ModelName)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		models = append(models, trimmed)
	}
	sort.Strings(models)
	return models, nil
}

func SiteModelRouteUpdate(accountID int, groupKey string, modelName string, routeType model.SiteModelRouteType, source model.SiteModelRouteSource, manualOverride bool, routeRawPayload string, ctx context.Context) error {
	now := time.Now()
	updates := map[string]any{
		"route_type":        model.NormalizeSiteModelRouteType(routeType),
		"route_source":      model.NormalizeSiteModelRouteSource(source, manualOverride),
		"manual_override":   manualOverride,
		"route_raw_payload": strings.TrimSpace(routeRawPayload),
		"route_updated_at":  &now,
	}
	return db.GetDB().WithContext(ctx).
		Model(&model.SiteModel{}).
		Where("site_account_id = ? AND group_key = ? AND model_name = ?", accountID, model.NormalizeSiteGroupKey(groupKey), strings.TrimSpace(modelName)).
		Updates(updates).Error
}

func SiteModelRouteUpdateIfNotManual(accountID int, groupKey string, modelName string, routeType model.SiteModelRouteType, source model.SiteModelRouteSource, routeRawPayload string, ctx context.Context) (bool, error) {
	now := time.Now()
	updates := map[string]any{
		"route_type":        model.NormalizeSiteModelRouteType(routeType),
		"route_source":      model.NormalizeSiteModelRouteSource(source, false),
		"manual_override":   false,
		"route_raw_payload": strings.TrimSpace(routeRawPayload),
		"route_updated_at":  &now,
	}
	result := db.GetDB().WithContext(ctx).
		Model(&model.SiteModel{}).
		Where("site_account_id = ? AND group_key = ? AND model_name = ? AND manual_override = ?", accountID, model.NormalizeSiteGroupKey(groupKey), strings.TrimSpace(modelName), false).
		Updates(updates)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

func SiteModelDisabledUpdate(accountID int, groupKey string, modelName string, disabled bool, ctx context.Context) error {
	return db.GetDB().WithContext(ctx).
		Model(&model.SiteModel{}).
		Where("site_account_id = ? AND group_key = ? AND model_name = ?", accountID, model.NormalizeSiteGroupKey(groupKey), strings.TrimSpace(modelName)).
		Update("disabled", disabled).Error
}

func SiteModelContext1MUpdate(accountID int, groupKey string, modelName string, context1M bool, ctx context.Context) error {
	return db.GetDB().WithContext(ctx).
		Model(&model.SiteModel{}).
		Where("site_account_id = ? AND group_key = ? AND model_name = ?", accountID, model.NormalizeSiteGroupKey(groupKey), strings.TrimSpace(modelName)).
		Update("context_1m", context1M).Error
}

func SiteModelContext1MForAccountModel(accountID int, groupKey string, modelName string, ctx context.Context) (bool, error) {
	var row model.SiteModel
	err := db.GetDB().WithContext(ctx).
		Where("site_account_id = ? AND group_key = ? AND model_name = ?", accountID, model.NormalizeSiteGroupKey(groupKey), strings.TrimSpace(modelName)).
		First(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	return row.Context1M, nil
}

func SiteModelContext1MForChannelModel(channelID int, modelName string, ctx context.Context) (bool, error) {
	binding, err := SiteChannelBindingGetByChannelID(channelID, ctx)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	groupKey, _ := model.ParseSiteChannelBindingKey(binding.GroupKey)
	return SiteModelContext1MForAccountModel(binding.SiteAccountID, groupKey, modelName, ctx)
}
