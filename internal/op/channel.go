package op

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/U188/octopus/internal/db"
	"github.com/U188/octopus/internal/model"
	model2 "github.com/U188/octopus/internal/transformer/outbound"
	"github.com/U188/octopus/internal/utils/cache"
	"github.com/U188/octopus/internal/utils/log"
	"github.com/U188/octopus/internal/utils/xstrings"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var channelCache = cache.New[int, model.Channel](16)
var channelKeyCache = cache.New[int, model.ChannelKey](16)
var channelKeyCacheNeedUpdate = make(map[int]struct{})
var channelKeyCacheNeedUpdateLock sync.Mutex

var ErrChannelNotFound = errors.New("channel not found")

func ChannelList(ctx context.Context) ([]model.Channel, error) {
	channels := make([]model.Channel, 0, channelCache.Len())
	now := time.Now().Unix()
	for _, channel := range channelCache.GetAll() {
		normalizeChannelProxyFields(&channel)
		channel.ResponsesToolAutoDenylist = normalizeResponsesToolAutoDenylist(channel.ResponsesToolAutoDenylist, now)
		channels = append(channels, channel)
	}
	return channels, nil
}

func normalizeChannelProxyFields(channel *model.Channel) {
	if channel == nil {
		return
	}
	if channel.ProxyMode == "" {
		channel.ProxyMode = model.ProxyUsageModeDirect
	}
	if channel.ProxyMode != model.ProxyUsageModePool {
		channel.ProxyConfigID = nil
	}
	channel.Proxy = channel.ProxyMode != model.ProxyUsageModeDirect
	channel.ChannelProxy = nil
}

func prepareChannelCreate(channel *model.Channel, ctx context.Context) error {
	if channel == nil {
		return fmt.Errorf("channel is nil")
	}
	if channel.ProxyMode == "" {
		channel.ProxyMode = model.ProxyUsageModeDirect
	}
	channel.ResponsesToolDenylist = normalizeResponsesToolDenylist(channel.ResponsesToolDenylist)
	channel.ResponsesToolAutoDenylist = normalizeResponsesToolAutoDenylist(channel.ResponsesToolAutoDenylist, time.Now().Unix())
	if err := channel.ProxyMode.Validate(false); err != nil {
		return err
	}
	if channel.ProxyMode == model.ProxyUsageModePool {
		if channel.ProxyConfigID == nil || *channel.ProxyConfigID <= 0 {
			return fmt.Errorf("proxy config id is required when proxy mode is pool")
		}
		if _, err := ProxyURLForConfig(*channel.ProxyConfigID, ctx); err != nil {
			return err
		}
	} else {
		channel.ProxyConfigID = nil
	}
	return nil
}

func cacheCreatedChannel(channel *model.Channel) {
	normalizeChannelProxyFields(channel)
	channelCache.Set(channel.ID, *channel)
	for _, k := range channel.Keys {
		if k.ID != 0 {
			channelKeyCache.Set(k.ID, k)
		}
	}

}

func ChannelCreate(channel *model.Channel, ctx context.Context) error {
	if err := prepareChannelCreate(channel, ctx); err != nil {
		return err
	}
	if err := db.GetDB().WithContext(ctx).Create(channel).Error; err != nil {
		return err
	}
	cacheCreatedChannel(channel)
	return nil
}

// ChannelCreateManaged creates a projected channel and its binding in one
// transaction. replaceBindingID is used when replacing a broken binding; it
// is deleted in the same transaction before the new binding is inserted.
// Caches are updated only after commit, so a failed transaction cannot expose
// a channel that has no durable binding.
func ChannelCreateManaged(channel *model.Channel, binding *model.SiteChannelBinding, replaceBindingID int, ctx context.Context) error {
	if binding == nil {
		return fmt.Errorf("site channel binding is nil")
	}
	if err := prepareChannelCreate(channel, ctx); err != nil {
		return err
	}
	tx := db.GetDB().WithContext(ctx).Begin()
	if tx.Error != nil {
		return tx.Error
	}
	if replaceBindingID != 0 {
		if err := tx.Delete(&model.SiteChannelBinding{}, replaceBindingID).Error; err != nil {
			tx.Rollback()
			return fmt.Errorf("delete replaced site channel binding: %w", err)
		}
	}
	if err := tx.Create(channel).Error; err != nil {
		tx.Rollback()
		return err
	}
	newBinding := *binding
	newBinding.ID = 0
	newBinding.ChannelID = channel.ID
	if err := tx.Create(&newBinding).Error; err != nil {
		tx.Rollback()
		return err
	}
	if err := tx.Commit().Error; err != nil {
		return err
	}
	*binding = newBinding
	cacheCreatedChannel(channel)
	return nil
}

// applyChannelKeyToChannel 以 copy-on-write 方式把 key 的最新值同步进 Channel.Keys，
// 供 GetChannelKey 的最低成本选 Key 策略读到实时运行时状态。
// 必须在 channelCache 的 UpdateExisting 锁内调用，且不得原地修改旧 slice（其他 goroutine 可能正在读）。
func applyChannelKeyToChannel(ch *model.Channel, key model.ChannelKey) {
	if len(ch.Keys) == 0 {
		return
	}
	keys := make([]model.ChannelKey, len(ch.Keys))
	copy(keys, ch.Keys)
	for i := range keys {
		if keys[i].ID == key.ID {
			keys[i] = key
			break
		}
	}
	ch.Keys = keys
}

func markChannelKeyNeedUpdate(keyID int) {
	channelKeyCacheNeedUpdateLock.Lock()
	channelKeyCacheNeedUpdate[keyID] = struct{}{}
	channelKeyCacheNeedUpdateLock.Unlock()
}

// ChannelKeyUpdate 仅更新 ChannelKey 的内存缓存（不落库），并标记为需要在 SaveCache 时写入数据库。
// Channel 侧通过 UpdateExisting 在锁内基于缓存当前值做最小修改，
// 不会用调用方手里的旧 Channel 快照覆盖管理员的并发变更（如禁用渠道）。
// 运行时用量累计请使用 ChannelKeyAddUsage，本接口的整结构覆盖在并发下会丢增量。
func ChannelKeyUpdate(key model.ChannelKey) error {
	if key.ID == 0 || key.ChannelID == 0 {
		return fmt.Errorf("invalid channel key")
	}
	if _, ok := channelCache.UpdateExisting(key.ChannelID, func(ch model.Channel) model.Channel {
		applyChannelKeyToChannel(&ch, key)
		return ch
	}); !ok {
		return fmt.Errorf("channel not found")
	}
	channelKeyCache.Set(key.ID, key)
	markChannelKeyNeedUpdate(key.ID)
	return nil
}

// ChannelKeyAddUsage 原子累加 ChannelKey 的运行时用量：成本增量、最近状态码与最后使用时间。
// 在缓存锁内基于当前值做增量，并发请求同时完成时计费累计不会互相覆盖丢失。
func ChannelKeyAddUsage(channelID, keyID int, costDelta float64, statusCode int, lastUse int64) error {
	if keyID == 0 || channelID == 0 {
		return fmt.Errorf("invalid channel key")
	}
	if !channelCache.Exists(channelID) {
		return fmt.Errorf("channel not found")
	}
	if _, ok := channelKeyCache.UpdateExisting(keyID, func(k model.ChannelKey) model.ChannelKey {
		k.TotalCost += costDelta
		k.StatusCode = statusCode
		k.LastUseTimeStamp = lastUse
		return k
	}); !ok {
		return fmt.Errorf("channel key not found")
	}
	// Channel.Keys 中的运行时视图在锁内从 channelKeyCache 取最新累计值同步，
	// 两次并发累加乱序到达时也不会把旧的中间值写回。
	channelCache.UpdateExisting(channelID, func(ch model.Channel) model.Channel {
		if cur, ok := channelKeyCache.Get(keyID); ok {
			applyChannelKeyToChannel(&ch, cur)
		}
		return ch
	})
	markChannelKeyNeedUpdate(keyID)
	return nil
}
func ChannelBaseUrlUpdate(channelID int, baseUrl []model.BaseUrl) error {
	ch, ok := channelCache.Get(channelID)
	if !ok {
		return fmt.Errorf("channel not found")
	}
	// Copy to decouple callers from internal cache storage.
	if baseUrl == nil {
		ch.BaseUrls = nil
	} else {
		cp := make([]model.BaseUrl, len(baseUrl))
		copy(cp, baseUrl)
		ch.BaseUrls = cp
	}
	channelCache.Set(channelID, ch)
	return nil
}

// ChannelKeySaveDB 将运行时更新过的 ChannelKey 缓存写入数据库。
func ChannelKeySaveDB(ctx context.Context) error {
	channelKeyCacheNeedUpdateLock.Lock()
	keyIDs := make([]int, 0, len(channelKeyCacheNeedUpdate))
	for id := range channelKeyCacheNeedUpdate {
		keyIDs = append(keyIDs, id)
	}
	channelKeyCacheNeedUpdate = make(map[int]struct{})
	channelKeyCacheNeedUpdateLock.Unlock()

	if len(keyIDs) == 0 {
		return nil
	}

	rows := make([]model.ChannelKey, 0, len(keyIDs))
	for _, id := range keyIDs {
		k, ok := channelKeyCache.Get(id)
		if ok {
			rows = append(rows, k)
		}
	}
	if len(rows) == 0 {
		return nil
	}
	if err := db.GetDB().WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		UpdateAll: true,
	}).CreateInBatches(&rows, 100).Error; err != nil {
		channelKeyCacheNeedUpdateLock.Lock()
		for _, id := range keyIDs {
			channelKeyCacheNeedUpdate[id] = struct{}{}
		}
		channelKeyCacheNeedUpdateLock.Unlock()
		return err
	}
	return nil
}

func ChannelUpdate(req *model.ChannelUpdateRequest, ctx context.Context) (*model.Channel, error) {
	existingChannel, ok := channelCache.Get(req.ID)
	if !ok {
		return nil, fmt.Errorf("channel not found")
	}
	normalizeChannelProxyFields(&existingChannel)
	if !req.BypassManagedCheck {
		if _, managed, err := ChannelManagedBinding(req.ID, ctx); err != nil {
			return nil, err
		} else if managed {
			return nil, fmt.Errorf("managed site channel is read-only; please edit it from the site account")
		}
	}

	tx := db.GetDB().WithContext(ctx).Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	var selectFields []string
	updates := model.Channel{ID: req.ID}

	if req.Name != nil {
		selectFields = append(selectFields, "name")
		updates.Name = *req.Name
	}
	if req.Type != nil {
		selectFields = append(selectFields, "type")
		updates.Type = *req.Type
	}
	if req.Enabled != nil {
		selectFields = append(selectFields, "enabled")
		updates.Enabled = *req.Enabled
	}
	if req.BaseUrls != nil {
		selectFields = append(selectFields, "base_urls")
		updates.BaseUrls = *req.BaseUrls
	}
	if req.Model != nil {
		selectFields = append(selectFields, "model")
		updates.Model = *req.Model
	}
	if req.CustomModel != nil {
		selectFields = append(selectFields, "custom_model")
		updates.CustomModel = *req.CustomModel
	}
	effectiveProxyMode := existingChannel.ProxyMode
	effectiveProxyConfigID := existingChannel.ProxyConfigID
	proxyTouched := false
	if req.ProxyMode != nil {
		proxyTouched = true
		effectiveProxyMode = *req.ProxyMode
		selectFields = append(selectFields, "proxy_mode")
		updates.ProxyMode = *req.ProxyMode
	}
	if req.ProxyConfigID != nil || req.ProxyMode != nil {
		proxyTouched = true
		if effectiveProxyMode == model.ProxyUsageModePool {
			if req.ProxyConfigID != nil {
				selectFields = append(selectFields, "proxy_config_id")
				effectiveProxyConfigID = req.ProxyConfigID
				updates.ProxyConfigID = req.ProxyConfigID
			}
		} else {
			selectFields = append(selectFields, "proxy_config_id")
			effectiveProxyConfigID = nil
			updates.ProxyConfigID = nil
		}
	}
	if proxyTouched {
		if effectiveProxyMode == "" {
			effectiveProxyMode = model.ProxyUsageModeDirect
		}
		if err := effectiveProxyMode.Validate(false); err != nil {
			tx.Rollback()
			return nil, err
		}
		if effectiveProxyMode == model.ProxyUsageModePool {
			if effectiveProxyConfigID == nil || *effectiveProxyConfigID <= 0 {
				tx.Rollback()
				return nil, fmt.Errorf("proxy config id is required when proxy mode is pool")
			}
			if _, err := ProxyURLForConfig(*effectiveProxyConfigID, ctx); err != nil {
				tx.Rollback()
				return nil, err
			}
		}
	}
	if req.AutoSync != nil {
		selectFields = append(selectFields, "auto_sync")
		updates.AutoSync = *req.AutoSync
	}
	if req.AutoGroup != nil {
		selectFields = append(selectFields, "auto_group")
		updates.AutoGroup = *req.AutoGroup
	}
	if req.CustomHeader != nil {
		selectFields = append(selectFields, "custom_header")
		updates.CustomHeader = *req.CustomHeader
	}
	if req.WSMode != nil {
		selectFields = append(selectFields, "ws_mode")
		updates.WSMode = req.WSMode.Normalize()
	}
	if req.CodexMode != nil {
		selectFields = append(selectFields, "codex_mode")
		updates.CodexMode = *req.CodexMode
	}
	if req.ClaudeMode != nil {
		selectFields = append(selectFields, "claude_mode")
		updates.ClaudeMode = *req.ClaudeMode
	}
	if req.ResponsesToolDenylist != nil {
		selectFields = append(selectFields, "responses_tool_denylist")
		updates.ResponsesToolDenylist = normalizeResponsesToolDenylist(*req.ResponsesToolDenylist)
	}
	if req.ParamOverride != nil {
		selectFields = append(selectFields, "param_override")
		updates.ParamOverride = req.ParamOverride
	}
	if req.MatchRegex != nil {
		selectFields = append(selectFields, "match_regex")
		updates.MatchRegex = req.MatchRegex
	}

	// 只有当有字段需要更新时才执行 UPDATE
	if len(selectFields) > 0 {
		if err := tx.Model(&model.Channel{}).Where("id = ?", req.ID).Select(selectFields).Updates(&updates).Error; err != nil {
			tx.Rollback()
			return nil, fmt.Errorf("failed to update channel: %w", err)
		}
	}

	// 删除 keys
	if len(req.KeysToDelete) > 0 {
		if err := tx.Where("id IN ? AND channel_id = ?", req.KeysToDelete, req.ID).Delete(&model.ChannelKey{}).Error; err != nil {
			tx.Rollback()
			return nil, fmt.Errorf("failed to delete channel keys: %w", err)
		}
	}

	// 更新 keys（逐条，只更新提供的字段）
	if len(req.KeysToUpdate) > 0 {
		for _, ku := range req.KeysToUpdate {
			updates := map[string]interface{}{}
			if ku.Enabled != nil {
				updates["enabled"] = *ku.Enabled
			}
			if ku.ChannelKey != nil {
				updates["channel_key"] = *ku.ChannelKey
			}
			if ku.Remark != nil {
				updates["remark"] = *ku.Remark
			}
			if len(updates) == 0 {
				continue
			}
			if err := tx.Model(&model.ChannelKey{}).
				Where("id = ? AND channel_id = ?", ku.ID, req.ID).
				Updates(updates).Error; err != nil {
				tx.Rollback()
				return nil, fmt.Errorf("failed to update channel key %d: %w", ku.ID, err)
			}
		}
	}

	// 新增 keys
	if len(req.KeysToAdd) > 0 {
		newKeys := make([]model.ChannelKey, 0, len(req.KeysToAdd))
		for _, ka := range req.KeysToAdd {
			newKeys = append(newKeys, model.ChannelKey{
				ChannelID:  req.ID,
				Enabled:    ka.Enabled,
				ChannelKey: ka.ChannelKey,
				Remark:     ka.Remark,
			})
		}
		if err := tx.Create(&newKeys).Error; err != nil {
			tx.Rollback()
			return nil, fmt.Errorf("failed to create channel keys: %w", err)
		}
	}

	if err := tx.Commit().Error; err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	// 刷新缓存并返回最新数据
	if err := channelRefreshCacheByID(req.ID, ctx); err != nil {
		return nil, err
	}

	channel, _ := channelCache.Get(req.ID)
	normalizeChannelProxyFields(&channel)
	channelCache.Set(req.ID, channel)
	resetBalancerStateForChannel(req.ID)
	return &channel, nil
}

func normalizeResponsesToolDenylist(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		value := strings.ToLower(strings.TrimSpace(item))
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func normalizeResponsesToolAutoDenylist(items []model.ResponsesToolAutoDeny, now int64) []model.ResponsesToolAutoDeny {
	if len(items) == 0 {
		return nil
	}
	seen := make(map[string]int, len(items))
	out := make([]model.ResponsesToolAutoDeny, 0, len(items))
	for _, item := range items {
		tool := strings.ToLower(strings.TrimSpace(item.Tool))
		if tool == "" || item.ExpiresAt <= now {
			continue
		}
		item.Tool = tool
		if len(item.LastError) > 500 {
			item.LastError = item.LastError[:500]
		}
		if idx, ok := seen[tool]; ok {
			if item.ExpiresAt > out[idx].ExpiresAt {
				out[idx] = item
			}
			continue
		}
		seen[tool] = len(out)
		out = append(out, item)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func ChannelAutoDenyResponsesTool(channelID int, tool string, reason string, lastError string, ttl time.Duration, ctx context.Context) error {
	tool = strings.ToLower(strings.TrimSpace(tool))
	if channelID <= 0 || tool == "" {
		return fmt.Errorf("invalid channel or tool")
	}
	if ttl <= 0 {
		return fmt.Errorf("invalid ttl")
	}
	ch, ok := channelCache.Get(channelID)
	if !ok {
		return fmt.Errorf("channel not found")
	}
	for _, manual := range normalizeResponsesToolDenylist(ch.ResponsesToolDenylist) {
		if manual == tool {
			return nil
		}
	}

	now := time.Now().Unix()
	expiresAt := time.Now().Add(ttl).Unix()
	items := normalizeResponsesToolAutoDenylist(ch.ResponsesToolAutoDenylist, now)
	if len(lastError) > 500 {
		lastError = lastError[:500]
	}
	replaced := false
	for i := range items {
		if items[i].Tool != tool {
			continue
		}
		items[i].Reason = reason
		items[i].LastError = lastError
		items[i].UpdatedAt = now
		items[i].ExpiresAt = expiresAt
		replaced = true
		break
	}
	if !replaced {
		items = append(items, model.ResponsesToolAutoDeny{
			Tool:      tool,
			Reason:    reason,
			LastError: lastError,
			UpdatedAt: now,
			ExpiresAt: expiresAt,
		})
	}

	payload, err := json.Marshal(items)
	if err != nil {
		return err
	}
	if err := db.GetDB().WithContext(ctx).Model(&model.Channel{}).
		Where("id = ?", channelID).
		Update("responses_tool_auto_denylist", string(payload)).Error; err != nil {
		return err
	}
	ch.ResponsesToolAutoDenylist = items
	channelCache.Set(channelID, ch)
	resetBalancerStateForChannel(channelID)
	return nil
}

func ChannelClearResponsesToolAutoDenylist(channelID int, ctx context.Context) error {
	if channelID <= 0 {
		return fmt.Errorf("invalid channel")
	}
	ch, ok := channelCache.Get(channelID)
	if !ok {
		return fmt.Errorf("channel not found")
	}
	if err := db.GetDB().WithContext(ctx).Model(&model.Channel{}).
		Where("id = ?", channelID).
		Update("responses_tool_auto_denylist", "[]").Error; err != nil {
		return err
	}
	ch.ResponsesToolAutoDenylist = nil
	channelCache.Set(channelID, ch)
	resetBalancerStateForChannel(channelID)
	return nil
}

func ChannelEnabled(id int, enabled bool, ctx context.Context) error {
	oldChannel, ok := channelCache.Get(id)
	if !ok {
		return fmt.Errorf("channel not found")
	}
	if _, managed, err := ChannelManagedBinding(id, ctx); err != nil {
		return err
	} else if managed {
		return fmt.Errorf("managed site channel is read-only; please enable or disable it from the site account")
	}
	if err := db.GetDB().WithContext(ctx).Model(&model.Channel{}).Where("id = ?", id).Update("enabled", enabled).Error; err != nil {
		return err
	}
	oldChannel.Enabled = enabled
	normalizeChannelProxyFields(&oldChannel)
	channelCache.Set(id, oldChannel)
	resetBalancerStateForChannel(id)
	return nil
}

func ChannelEnabledManaged(id int, enabled bool, ctx context.Context) error {
	oldChannel, ok := channelCache.Get(id)
	if !ok {
		return fmt.Errorf("channel not found")
	}
	if err := db.GetDB().WithContext(ctx).Model(&model.Channel{}).Where("id = ?", id).Update("enabled", enabled).Error; err != nil {
		return err
	}
	oldChannel.Enabled = enabled
	normalizeChannelProxyFields(&oldChannel)
	channelCache.Set(id, oldChannel)
	resetBalancerStateForChannel(id)
	return nil
}

func ChannelDel(id int, ctx context.Context) error {
	return channelDel(id, ctx, false)
}

func ChannelDelManaged(id int, ctx context.Context) error {
	if _, managed, err := ChannelManagedBinding(id, ctx); err != nil {
		return err
	} else if !managed {
		return fmt.Errorf("channel is not a managed site channel")
	}
	return channelDel(id, ctx, true)
}

func channelDel(id int, ctx context.Context, bypassManagedCheck bool) error {
	ch, ok := channelCache.Get(id)
	if !ok {
		return fmt.Errorf("channel not found")
	}
	if !bypassManagedCheck {
		if _, managed, err := ChannelManagedBinding(id, ctx); err != nil {
			return err
		} else if managed {
			return fmt.Errorf("managed site channel cannot be deleted directly; delete the site account or site binding instead")
		}
	}

	// 开启事务
	tx := db.GetDB().WithContext(ctx).Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// 获取所有受影响的 GroupID，用于刷新缓存
	var affectedGroupIDs []int
	if err := tx.Model(&model.GroupItem{}).
		Where("channel_id = ?", id).
		Pluck("group_id", &affectedGroupIDs).Error; err != nil {
		tx.Rollback()
		return fmt.Errorf("failed to get affected groups: %w", err)
	}

	// 删除所有引用该渠道的 GroupItem
	if err := tx.Where("channel_id = ?", id).Delete(&model.GroupItem{}).Error; err != nil {
		tx.Rollback()
		return fmt.Errorf("failed to delete group items: %w", err)
	}

	// 删除渠道 keys
	if err := tx.Where("channel_id = ?", id).Delete(&model.ChannelKey{}).Error; err != nil {
		tx.Rollback()
		return fmt.Errorf("failed to delete channel keys: %w", err)
	}

	// 删除统计数据
	if err := tx.Where("channel_id = ?", id).Delete(&model.StatsChannel{}).Error; err != nil {
		tx.Rollback()
		return fmt.Errorf("failed to delete channel stats: %w", err)
	}

	// 删除渠道
	if err := tx.Delete(&model.Channel{}, id).Error; err != nil {
		tx.Rollback()
		return fmt.Errorf("failed to delete channel: %w", err)
	}

	if err := tx.Commit().Error; err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	// 删除缓存
	channelCache.Del(id)
	for _, k := range ch.Keys {
		if k.ID != 0 {
			channelKeyCache.Del(k.ID)
		}
	}
	StatsChannelDel(id)
	resetBalancerStateForChannel(id)

	// 刷新受影响的分组缓存
	for _, groupID := range affectedGroupIDs {
		if err := groupRefreshCacheByID(groupID, ctx); err != nil {
			log.Warnf("failed to refresh group cache for group %d: %v", groupID, err)
		}
	}

	return nil
}

func ChannelLLMList(ctx context.Context) ([]model.LLMChannel, error) {
	channelsByID := channelCache.GetAll()
	channelIDs := make([]int, 0, len(channelsByID))
	for channelID := range channelsByID {
		channelIDs = append(channelIDs, channelID)
	}
	bindingMap, err := SiteChannelBindingMapByChannelIDs(channelIDs, ctx)
	if err != nil {
		return nil, err
	}
	siteCache := make(map[int]*model.Site)
	accountCache := make(map[int]*model.SiteAccount)

	models := []model.LLMChannel{}
	for _, channel := range channelsByID {
		var binding *model.SiteChannelBinding
		if item, ok := bindingMap[channel.ID]; ok {
			copy := item
			binding = &copy
		}
		if binding != nil {
			allowed, err := siteProjectedBindingAllowsModels(*binding, ctx)
			if err != nil {
				return nil, err
			}
			if !allowed {
				continue
			}
		}
		siteName := ""
		siteAccountName := ""
		siteGroupKey := ""
		siteGroupName := ""
		endpointType := "openai"
		var siteID *int
		var siteAccountID *int
		if binding != nil {
			siteID = &binding.SiteID
			siteAccountID = &binding.SiteAccountID
			siteGroupKey = model.NormalizeSiteGroupKey(binding.GroupKey)
			if site, ok := siteCache[binding.SiteID]; ok {
				siteName = site.Name
			} else if site, getErr := SiteGet(binding.SiteID, ctx); getErr == nil {
				siteCache[binding.SiteID] = site
				siteName = site.Name
			}
			if account, ok := accountCache[binding.SiteAccountID]; ok {
				siteAccountName = account.Name
			} else if account, getErr := SiteAccountGet(binding.SiteAccountID, ctx); getErr == nil {
				accountCache[binding.SiteAccountID] = account
				siteAccountName = account.Name
			}
			siteGroupName = siteGroupKey
			if binding.SiteUserGroupID != nil && *binding.SiteUserGroupID > 0 {
				if account := accountCache[binding.SiteAccountID]; account != nil {
					for _, group := range account.UserGroups {
						if group.ID == *binding.SiteUserGroupID {
							siteGroupName = model.NormalizeSiteGroupName(group.GroupKey, group.Name)
							siteGroupKey = model.NormalizeSiteGroupKey(group.GroupKey)
							break
						}
					}
				}
			}
			if siteGroupName == "" {
				siteGroupName = model.NormalizeSiteGroupName(siteGroupKey, "")
			}
			switch channel.Type {
			case model2.OutboundTypeAnthropic:
				endpointType = "anthropic"
			case model2.OutboundTypeGemini:
				endpointType = "gemini"
			default:
				endpointType = "openai"
			}
		}
		modelNames, err := ChannelVisibleModelNames(channel.ID, ctx)
		if err != nil {
			return nil, err
		}
		for _, modelName := range modelNames {
			if modelName == "" {
				continue
			}
			models = append(models, model.LLMChannel{
				Name:            modelName,
				Enabled:         channel.Enabled,
				ChannelID:       channel.ID,
				ChannelName:     channel.Name,
				SiteID:          siteID,
				SiteAccountID:   siteAccountID,
				SiteGroupKey:    siteGroupKey,
				SiteGroupName:   siteGroupName,
				SiteName:        siteName,
				SiteAccountName: siteAccountName,
				EndpointType:    endpointType,
			})
		}
	}
	return models, nil
}

func ChannelVisibleModelNames(channelID int, ctx context.Context) ([]string, error) {
	channel, ok := channelCache.Get(channelID)
	if !ok {
		return nil, fmt.Errorf("channel not found")
	}
	binding, managed, err := ChannelManagedBinding(channelID, ctx)
	if err != nil {
		return nil, err
	}
	if managed {
		allowed, err := siteProjectedBindingAllowsModels(*binding, ctx)
		if err != nil {
			return nil, err
		}
		if !allowed {
			return nil, nil
		}
	}
	return xstrings.SplitTrimCompact(",", channel.Model, channel.CustomModel), nil
}

func ChannelGet(id int, ctx context.Context) (*model.Channel, error) {
	channel, ok := channelCache.Get(id)
	if !ok {
		return nil, ErrChannelNotFound
	}
	normalizeChannelProxyFields(&channel)
	channel.ResponsesToolAutoDenylist = normalizeResponsesToolAutoDenylist(channel.ResponsesToolAutoDenylist, time.Now().Unix())
	return &channel, nil
}

func ChannelGetByName(name string, ctx context.Context) (*model.Channel, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return nil, fmt.Errorf("channel name is empty")
	}

	var channel model.Channel
	if err := db.GetDB().WithContext(ctx).
		Preload("Keys").
		Where("name = ?", trimmed).
		First(&channel).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			for id, cached := range channelCache.GetAll() {
				if cached.Name != trimmed {
					continue
				}
				channelCache.Del(id)
				for _, key := range cached.Keys {
					if key.ID != 0 {
						channelKeyCache.Del(key.ID)
					}
				}
			}
		}
		return nil, err
	}

	normalizeChannelProxyFields(&channel)
	channel.Stats = nil
	channelCache.Set(channel.ID, channel)
	for _, k := range channel.Keys {
		if k.ID != 0 {
			channelKeyCache.Set(k.ID, k)
		}
	}

	return &channel, nil
}

func channelRefreshCache(ctx context.Context) error {
	channels := []model.Channel{}
	if err := db.GetDB().WithContext(ctx).
		Preload("Keys").
		Find(&channels).Error; err != nil {
		log.Warnf("failed to get channels: %v", err)
		return err
	}
	channelKeyCache.Clear()
	channelKeyCacheNeedUpdateLock.Lock()
	channelKeyCacheNeedUpdate = make(map[int]struct{})
	channelKeyCacheNeedUpdateLock.Unlock()
	for _, channel := range channels {
		normalizeChannelProxyFields(&channel)
		channelCache.Set(channel.ID, channel)
		for _, k := range channel.Keys {
			if k.ID != 0 {
				channelKeyCache.Set(k.ID, k)
			}
		}
	}
	return nil
}

func channelRefreshCacheByID(id int, ctx context.Context) error {
	// 先读库，成功后再替换缓存：若读库失败就提前清理旧 Key，
	// 会把带有待落库运行时状态（channelKeyCacheNeedUpdate 脏集）的条目剥离，导致数据丢失。
	var channel model.Channel
	if err := db.GetDB().WithContext(ctx).
		Preload("Keys").
		First(&channel, id).Error; err != nil {
		return err
	}
	newKeyIDs := make(map[int]struct{}, len(channel.Keys))
	for _, k := range channel.Keys {
		if k.ID != 0 {
			newKeyIDs[k.ID] = struct{}{}
		}
	}
	if old, ok := channelCache.Get(id); ok {
		for _, k := range old.Keys {
			if k.ID != 0 {
				if _, still := newKeyIDs[k.ID]; !still {
					channelKeyCache.Del(k.ID)
				}
			}
		}
	}
	normalizeChannelProxyFields(&channel)
	channel.Stats = nil
	channelCache.Set(channel.ID, channel)
	for _, k := range channel.Keys {
		if k.ID != 0 {
			channelKeyCache.Set(k.ID, k)
		}
	}
	return nil
}
