package task

import (
	"context"
	"encoding/json"
	"time"

	"github.com/U188/octopus/internal/model"
	"github.com/U188/octopus/internal/op"
	"github.com/U188/octopus/internal/utils/log"
)

const TaskWebDAVAutoBackup = "webdav_auto_backup"

func WebDAVAutoBackupTask() {
	enabled, err := op.SettingGetBool(model.SettingKeyWebDAVAutoBackupEnabled)
	if err != nil {
		log.Warnf("failed to get webdav auto backup enabled: %v", err)
		return
	}
	if !enabled {
		return
	}

	url, _ := op.SettingGetString(model.SettingKeyWebDAVAutoBackupURL)
	username, _ := op.SettingGetString(model.SettingKeyWebDAVAutoBackupUsername)
	password, _ := op.SettingGetString(model.SettingKeyWebDAVAutoBackupPassword)
	retention, err := op.SettingGetInt(model.SettingKeyWebDAVAutoBackupRetention)
	if err != nil || retention <= 0 {
		retention = 7
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	result, err := op.WebDAVAutoBackupSQLite(ctx, model.WebDAVCredentials{
		URL:      url,
		Username: username,
		Password: password,
	}, retention)
	if err != nil {
		recordTaskAudit("webdav.auto_backup", op.AuditStatusFailed, map[string]any{
			"retention": retention,
		}, err)
		log.Warnf("webdav auto backup failed: %v", err)
		return
	}

	recordTaskAudit("webdav.auto_backup", op.AuditStatusSuccess, map[string]any{
		"filename":  result.Filename,
		"size":      result.Size,
		"retention": retention,
	}, nil)
}

func recordTaskAudit(action, status string, detail map[string]any, auditErr error) {
	detailJSON := ""
	if detail != nil {
		if data, err := json.Marshal(detail); err == nil {
			detailJSON = string(data)
		}
	}
	errText := ""
	if auditErr != nil {
		errText = auditErr.Error()
	}
	if err := op.AuditCreate(context.Background(), &model.AuditLog{
		Action: action,
		Status: status,
		Actor:  "system",
		Detail: detailJSON,
		Error:  errText,
	}); err != nil {
		log.Warnf("failed to write audit log: %v", err)
	}
}
