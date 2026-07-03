package op

import (
	"context"
	"fmt"
	"strings"

	"github.com/U188/octopus/internal/db"
	"github.com/U188/octopus/internal/model"
)

const (
	AuditStatusSuccess = "success"
	AuditStatusFailed  = "failed"
)

type AuditListFilter struct {
	Limit int
}

func AuditCreate(ctx context.Context, entry *model.AuditLog) error {
	if entry == nil {
		return fmt.Errorf("audit entry is nil")
	}
	entry.Action = strings.TrimSpace(entry.Action)
	entry.Status = strings.TrimSpace(entry.Status)
	if entry.Action == "" {
		return fmt.Errorf("audit action is required")
	}
	if entry.Status == "" {
		entry.Status = AuditStatusSuccess
	}
	return db.GetDB().WithContext(ctx).Create(entry).Error
}

func AuditList(ctx context.Context, filter AuditListFilter) ([]model.AuditLog, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	var logs []model.AuditLog
	if err := db.GetDB().WithContext(ctx).
		Order("created_at DESC").
		Order("id DESC").
		Limit(limit).
		Find(&logs).Error; err != nil {
		return nil, err
	}
	return logs, nil
}
