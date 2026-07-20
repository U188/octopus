package task

import (
	"context"
	"time"

	"github.com/U188/octopus/internal/model"
	"github.com/U188/octopus/internal/op"
	"github.com/U188/octopus/internal/price"
	"github.com/U188/octopus/internal/tgbot"
	"github.com/U188/octopus/internal/utils/log"
)

const (
	TaskPriceUpdate       = "price_update"
	TaskStatsSave         = "stats_save"
	TaskRelayLogSave      = "relay_log_save"
	TaskSyncLLM           = "sync_llm"
	TaskCleanLLM          = "clean_llm"
	TaskBaseUrlDelay      = "base_url_delay"
	TaskSiteSync          = "site_sync"
	TaskSiteCheckin       = "site_checkin"
	TaskTelegramOps       = "telegram_ops"
	TaskWSAffinityCleanup = "ws_affinity_cleanup"
)

func Init() {
	// getIntSetting 读取整型设置项；单个设置项非法（如用户填了非数字）时仅告警并回退默认值，
	// 绝不因某一项出错就中止 Init，避免统计/日志持久化等后续任务全部失联。
	getIntSetting := func(key model.SettingKey, def int) int {
		v, err := op.SettingGetInt(key)
		if err != nil {
			log.Warnf("failed to get setting %s, using default %d: %v", key, def, err)
			return def
		}
		return v
	}

	priceUpdateIntervalHours := getIntSetting(model.SettingKeyModelInfoUpdateInterval, 24)
	priceUpdateInterval := time.Duration(priceUpdateIntervalHours) * time.Hour
	// 注册价格更新任务
	Register(string(model.SettingKeyModelInfoUpdateInterval), priceUpdateInterval, true, func() {
		if err := price.UpdateLLMPrice(context.Background()); err != nil {
			log.Warnf("failed to update price info: %v", err)
		}
	})

	// 注册基础URL延迟任务
	Register(TaskBaseUrlDelay, 24*time.Hour, true, ChannelBaseUrlDelayTask)

	// 注册LLM同步任务
	syncLLMIntervalHours := getIntSetting(model.SettingKeySyncLLMInterval, 24)
	syncLLMInterval := time.Duration(syncLLMIntervalHours) * time.Hour
	Register(string(model.SettingKeySyncLLMInterval), syncLLMInterval, true, SyncModelsTask)

	siteSyncIntervalHours := getIntSetting(model.SettingKeySiteSyncInterval, 12)
	siteSyncInterval := time.Duration(siteSyncIntervalHours) * time.Hour
	Register(string(model.SettingKeySiteSyncInterval), siteSyncInterval, true, SiteSyncTask)

	siteCheckinIntervalHours := getIntSetting(model.SettingKeySiteCheckinInterval, 24)
	siteCheckinInterval := time.Duration(siteCheckinIntervalHours) * time.Hour
	Register(string(model.SettingKeySiteCheckinInterval), siteCheckinInterval, true, SiteCheckinTask)

	// 注册统计保存任务
	statsSaveIntervalMinutes := getIntSetting(model.SettingKeyStatsSaveInterval, 10)
	statsSaveInterval := time.Duration(statsSaveIntervalMinutes) * time.Minute
	Register(TaskStatsSave, statsSaveInterval, false, op.StatsSaveDBTask)
	// 注册中继日志保存任务
	Register(TaskRelayLogSave, time.Hour, false, func() {
		if err := op.RelayLogSaveDBTask(context.Background()); err != nil {
			log.Warnf("relay log save db task failed: %v", err)
		}
	})

	Register(TaskWSAffinityCleanup, 10*time.Minute, false, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		deleted, err := op.WSResponseAffinityCleanup(ctx, time.Now())
		if err != nil {
			log.Warnf("ws response affinity cleanup failed: %v", err)
			return
		}
		if deleted > 0 {
			log.Debugf("ws response affinity cleanup removed %d expired rows", deleted)
		}
	})

	Register(TaskTelegramOps, time.Minute, false, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := tgbot.RunOpsNotifications(ctx); err != nil {
			log.Warnf("telegram ops task failed: %v", err)
		}
	})

	// 注册被动离群退役(POR)任务（默认间隔 2 分钟，总开关在任务内判断）
	outlierIntervalMinutes, err := op.SettingGetInt(model.SettingKeyOutlierRetireInterval)
	if err != nil || outlierIntervalMinutes <= 0 {
		outlierIntervalMinutes = 2
	}
	Register(string(model.SettingKeyOutlierRetireInterval), time.Duration(outlierIntervalMinutes)*time.Minute, false, SiteOutlierRetireTask)

	webDAVAutoBackupIntervalHours, err := op.SettingGetInt(model.SettingKeyWebDAVAutoBackupIntervalHours)
	if err != nil || webDAVAutoBackupIntervalHours <= 0 {
		webDAVAutoBackupIntervalHours = 24
	}
	Register(TaskWebDAVAutoBackup, time.Duration(webDAVAutoBackupIntervalHours)*time.Hour, false, WebDAVAutoBackupTask)
}
