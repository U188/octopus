package op

import (
	"context"
	"errors"

	"github.com/U188/octopus/internal/db"
	"github.com/U188/octopus/internal/model"
	"gorm.io/gorm"
)

// siteChannelBindingQueryBatchSize keeps channel-id IN clauses below the
// parameter limits of SQLite (and leaves headroom for MySQL/PostgreSQL
// deployments). Cache refreshes and admin list endpoints can otherwise fail
// once a large installation has more channels than the database accepts in a
// single statement.
const siteChannelBindingQueryBatchSize = 500

func SiteChannelBindingGetByChannelID(channelID int, ctx context.Context) (*model.SiteChannelBinding, error) {
	var binding model.SiteChannelBinding
	if err := db.GetDB().WithContext(ctx).Where("channel_id = ?", channelID).First(&binding).Error; err != nil {
		return nil, err
	}
	return &binding, nil
}

func SiteChannelBindingMapByChannelIDs(channelIDs []int, ctx context.Context) (map[int]model.SiteChannelBinding, error) {
	result := make(map[int]model.SiteChannelBinding)
	if len(channelIDs) == 0 {
		return result, nil
	}

	// De-duplicate IDs before batching. Besides reducing SQL parameters, this
	// makes callers that assemble IDs from multiple cache views deterministic.
	uniqueIDs := make([]int, 0, len(channelIDs))
	seen := make(map[int]struct{}, len(channelIDs))
	for _, channelID := range channelIDs {
		if channelID <= 0 {
			continue
		}
		if _, ok := seen[channelID]; ok {
			continue
		}
		seen[channelID] = struct{}{}
		uniqueIDs = append(uniqueIDs, channelID)
	}
	if len(uniqueIDs) == 0 {
		return result, nil
	}

	database := db.GetDB().WithContext(ctx)
	for start := 0; start < len(uniqueIDs); start += siteChannelBindingQueryBatchSize {
		end := start + siteChannelBindingQueryBatchSize
		if end > len(uniqueIDs) {
			end = len(uniqueIDs)
		}
		var bindings []model.SiteChannelBinding
		if err := database.Where("channel_id IN ?", uniqueIDs[start:end]).Find(&bindings).Error; err != nil {
			return nil, err
		}
		for _, binding := range bindings {
			result[binding.ChannelID] = binding
		}
	}
	return result, nil
}

// SiteChannelBindingListAll 返回全部站点投影渠道绑定，供 POR 任务按 siteAccountID 分组兄弟渠道。
func SiteChannelBindingListAll(ctx context.Context) ([]model.SiteChannelBinding, error) {
	var bindings []model.SiteChannelBinding
	err := db.GetDB().WithContext(ctx).Find(&bindings).Error
	return bindings, err
}

func ChannelManagedBinding(channelID int, ctx context.Context) (*model.SiteChannelBinding, bool, error) {
	binding, err := SiteChannelBindingGetByChannelID(channelID, ctx)
	if err == nil {
		return binding, true, nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, nil
	}
	return nil, false, err
}
