package tgbot

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/U188/octopus/internal/db"
	"github.com/U188/octopus/internal/model"
	"github.com/U188/octopus/internal/op"
	"github.com/U188/octopus/internal/utils/log"
)

type reportWindow struct {
	Label string
	Start time.Time
	End   time.Time
}

type usageSummary struct {
	Requests     int
	Success      int
	Failed       int
	InputTokens  int
	OutputTokens int
	Cost         float64
	FtutTotal    int
	UseTimeTotal int
}

type topUsageItem struct {
	Name         string
	LastTime     int64
	Requests     int
	Success      int
	Failed       int
	InputTokens  int
	OutputTokens int
	Cost         float64
}

type siteOpsSummary struct {
	Sites            int
	DisabledSites    int
	Accounts         int
	DisabledAccounts int
	Models           int
	DisabledModels   int
	MissingModelRows []string
	Balances         []balanceItem
}

type balanceItem struct {
	SiteName    string
	AccountID   int
	AccountName string
	Balance     float64
	BalanceUsed float64
	TodayIncome float64
	Enabled     bool
}

type alertState map[string]int64

func buildOpsReport(ctx context.Context, args []string) string {
	window := parseReportWindow(args, time.Now())
	usage, models, channels, err := loadUsage(ctx, window)
	if err != nil {
		return "生成运维报告失败：" + err.Error()
	}
	sites, err := loadSiteOpsSummary(ctx)
	if err != nil {
		return "生成站点概览失败：" + err.Error()
	}
	return formatOpsReport(window, usage, models, channels, sites)
}

func buildBalanceReport(ctx context.Context) string {
	sites, err := loadSiteOpsSummary(ctx)
	if err != nil {
		return "读取余额失败：" + err.Error()
	}
	if len(sites.Balances) == 0 {
		return "💰 余额概览\n\n暂无可显示余额。"
	}
	sort.Slice(sites.Balances, func(i, j int) bool {
		if sites.Balances[i].Balance != sites.Balances[j].Balance {
			return sites.Balances[i].Balance < sites.Balances[j].Balance
		}
		return sites.Balances[i].AccountID < sites.Balances[j].AccountID
	})
	var b strings.Builder
	b.WriteString("💰 余额概览\n按余额从低到高")
	for _, item := range limitBalanceItems(sites.Balances, 20) {
		status := "🟢 启用"
		if !item.Enabled {
			status = "⚪ 停用"
		}
		fmt.Fprintf(&b, "\n\n%s #%d %s / %s\n余额 %s｜已用 %s｜今日 %s",
			status,
			item.AccountID,
			item.SiteName,
			item.AccountName,
			formatFloat(item.Balance),
			formatFloat(item.BalanceUsed),
			formatFloat(item.TodayIncome),
		)
	}
	return b.String()
}

func buildTopReport(ctx context.Context, args []string) string {
	window := parseReportWindow(args, time.Now())
	usage, models, channels, err := loadUsage(ctx, window)
	if err != nil {
		return "生成排行失败：" + err.Error()
	}
	var b strings.Builder
	fmt.Fprintf(&b, "🏆 Top 排行｜%s\n\n📊 请求 %s｜✅ %s｜❌ %s\n🎟 Token %s｜💸 %s",
		window.Label,
		formatInt(usage.Requests),
		formatInt(usage.Success),
		formatInt(usage.Failed),
		formatInt(usage.InputTokens+usage.OutputTokens),
		formatFloat(usage.Cost),
	)
	appendTopItems(&b, "\n\n🤖 模型 Top", models, 10)
	appendChannelTopItems(&b, "\n\n🧭 渠道 Top", channels, 10)
	return b.String()
}

func RunOpsNotifications(ctx context.Context) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if !cfg.Enabled || cfg.Token == "" || len(cfg.AdminIDs) == 0 {
		return nil
	}
	client, err := clientForConfig(cfg)
	if err != nil {
		return err
	}
	now := time.Now()
	if err := runDailyReport(ctx, cfg, client, now); err != nil {
		log.Warnf("telegram daily report failed: %v", err)
	}
	if err := runOpsAlerts(ctx, cfg, client, now); err != nil {
		log.Warnf("telegram ops alerts failed: %v", err)
	}
	return nil
}

func runDailyReport(ctx context.Context, cfg config, client *http.Client, now time.Time) error {
	enabled, err := op.SettingGetBool(model.SettingKeyTelegramReportEnabled)
	if err != nil || !enabled {
		return err
	}
	hour, minute := telegramReportTime()
	if now.Hour() < hour || (now.Hour() == hour && now.Minute() < minute) {
		return nil
	}
	today := now.Format("2006-01-02")
	lastDate, _ := op.SettingGetString(model.SettingKeyTelegramReportLastDate)
	if strings.TrimSpace(lastDate) == today {
		return nil
	}
	yesterday := time.Date(now.Year(), now.Month(), now.Day()-1, 0, 0, 0, 0, now.Location())
	text := buildOpsReportForWindow(ctx, reportWindow{
		Label: yesterday.Format("2006-01-02"),
		Start: yesterday,
		End:   yesterday.AddDate(0, 0, 1),
	})
	if err := sendAdminText(ctx, cfg, client, text); err != nil {
		return err
	}
	return op.SettingSetString(model.SettingKeyTelegramReportLastDate, today)
}

func runOpsAlerts(ctx context.Context, cfg config, client *http.Client, now time.Time) error {
	enabled, err := op.SettingGetBool(model.SettingKeyTelegramAlertEnabled)
	if err != nil || !enabled {
		return err
	}
	state := loadAlertState()
	cooldown := settingInt(model.SettingKeyTelegramAlertCooldownMinutes, 60)
	alerts, keys := evaluateAlerts(ctx, now, state, time.Duration(cooldown)*time.Minute)
	if len(alerts) == 0 {
		return nil
	}
	text := "🚨 Octopus 运维告警\n\n" + strings.Join(alerts, "\n")
	if err := sendAdminText(ctx, cfg, client, text); err != nil {
		return err
	}
	for _, key := range keys {
		state[key] = now.Unix()
	}
	return saveAlertState(state)
}

func buildOpsReportForWindow(ctx context.Context, window reportWindow) string {
	usage, models, channels, err := loadUsage(ctx, window)
	if err != nil {
		return "生成运维报告失败：" + err.Error()
	}
	sites, err := loadSiteOpsSummary(ctx)
	if err != nil {
		return "生成站点概览失败：" + err.Error()
	}
	return formatOpsReport(window, usage, models, channels, sites)
}

func parseReportWindow(args []string, now time.Time) reportWindow {
	mode := "today"
	if len(args) > 0 {
		mode = strings.ToLower(strings.TrimSpace(args[0]))
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	switch mode {
	case "24h", "24", "day":
		return reportWindow{Label: "最近24小时", Start: now.Add(-24 * time.Hour), End: now}
	case "yesterday", "yd", "昨天":
		start := today.AddDate(0, 0, -1)
		return reportWindow{Label: start.Format("2006-01-02"), Start: start, End: today}
	default:
		return reportWindow{Label: "今日", Start: today, End: now}
	}
}

func loadUsage(ctx context.Context, window reportWindow) (usageSummary, []topUsageItem, []topUsageItem, error) {
	var rows []model.RelayLog
	err := db.GetDB().WithContext(ctx).
		Select("time", "request_model_name", "channel_id", "channel_name", "actual_model_name", "input_tokens", "output_tokens", "ftut", "use_time", "cost", "success").
		Where("time >= ? AND time < ?", window.Start.Unix(), window.End.Unix()).
		Where("channel_name IS NULL OR channel_name = '' OR channel_name NOT LIKE ?", "Site Test Conversation%").
		Find(&rows).Error
	if err != nil {
		return usageSummary{}, nil, nil, err
	}
	var usage usageSummary
	modelMap := make(map[string]*topUsageItem)
	channelMap := make(map[string]*topUsageItem)
	for _, row := range rows {
		usage.Requests++
		if row.Success {
			usage.Success++
		} else {
			usage.Failed++
		}
		usage.InputTokens += row.InputTokens
		usage.OutputTokens += row.OutputTokens
		usage.Cost += row.Cost
		usage.FtutTotal += row.Ftut
		usage.UseTimeTotal += row.UseTime

		modelName := firstNonEmpty(row.RequestModelName, row.ActualModelName, "-")
		addTopUsage(modelMap, modelName, modelName, row)
		channelName := firstNonEmpty(row.ChannelName, fmt.Sprintf("channel#%d", row.ChannelId))
		addTopUsage(channelMap, channelUsageKey(row), channelName, row)
	}
	return usage, sortTopUsage(modelMap), sortTopUsageBySuccess(channelMap), nil
}

func addTopUsage(items map[string]*topUsageItem, key string, displayName string, row model.RelayLog) {
	item := items[key]
	if item == nil {
		item = &topUsageItem{Name: displayName}
		items[key] = item
	}
	if displayName != "" && row.Time >= item.LastTime {
		item.Name = displayName
		item.LastTime = row.Time
	}
	item.Requests++
	if row.Success {
		item.Success++
	} else {
		item.Failed++
	}
	item.InputTokens += row.InputTokens
	item.OutputTokens += row.OutputTokens
	item.Cost += row.Cost
}

func channelUsageKey(row model.RelayLog) string {
	if row.ChannelId > 0 {
		return fmt.Sprintf("channel:%d", row.ChannelId)
	}
	return "name:" + firstNonEmpty(row.ChannelName, fmt.Sprintf("channel#%d", row.ChannelId))
}

func sortTopUsage(items map[string]*topUsageItem) []topUsageItem {
	result := make([]topUsageItem, 0, len(items))
	for _, item := range items {
		result = append(result, *item)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Requests != result[j].Requests {
			return result[i].Requests > result[j].Requests
		}
		if result[i].InputTokens+result[i].OutputTokens != result[j].InputTokens+result[j].OutputTokens {
			return result[i].InputTokens+result[i].OutputTokens > result[j].InputTokens+result[j].OutputTokens
		}
		return result[i].Name < result[j].Name
	})
	return result
}

func sortTopUsageBySuccess(items map[string]*topUsageItem) []topUsageItem {
	result := make([]topUsageItem, 0, len(items))
	for _, item := range items {
		result = append(result, *item)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Success != result[j].Success {
			return result[i].Success > result[j].Success
		}
		if result[i].Requests != result[j].Requests {
			return result[i].Requests > result[j].Requests
		}
		if result[i].InputTokens+result[i].OutputTokens != result[j].InputTokens+result[j].OutputTokens {
			return result[i].InputTokens+result[i].OutputTokens > result[j].InputTokens+result[j].OutputTokens
		}
		return result[i].Name < result[j].Name
	})
	return result
}

func loadSiteOpsSummary(ctx context.Context) (siteOpsSummary, error) {
	sites, err := op.SiteList(ctx)
	if err != nil {
		return siteOpsSummary{}, err
	}
	var summary siteOpsSummary
	for _, site := range sites {
		summary.Sites++
		if !site.Enabled {
			summary.DisabledSites++
		}
		for _, account := range site.Accounts {
			summary.Accounts++
			if !account.Enabled {
				summary.DisabledAccounts++
			}
			summary.Models += len(account.Models)
			for _, siteModel := range account.Models {
				if siteModel.Disabled {
					summary.DisabledModels++
				}
			}
			if site.Enabled && account.Enabled && account.AutoSync && len(account.Models) == 0 {
				summary.MissingModelRows = append(summary.MissingModelRows, fmt.Sprintf("%s / #%d %s", site.Name, account.ID, account.Name))
			}
			summary.Balances = append(summary.Balances, balanceItem{
				SiteName:    site.Name,
				AccountID:   account.ID,
				AccountName: account.Name,
				Balance:     account.Balance,
				BalanceUsed: account.BalanceUsed,
				TodayIncome: account.TodayIncome,
				Enabled:     site.Enabled && account.Enabled,
			})
		}
	}
	return summary, nil
}

func formatOpsReport(window reportWindow, usage usageSummary, models []topUsageItem, channels []topUsageItem, sites siteOpsSummary) string {
	var b strings.Builder
	fmt.Fprintf(&b, "🐙 Octopus 运维报告｜%s", window.Label)
	fmt.Fprintf(&b, "\n📅 %s - %s", window.Start.Format("01-02 15:04"), window.End.Format("01-02 15:04"))
	fmt.Fprintf(&b, "\n\n📊 流量\n请求 %s｜✅ %s｜❌ %s｜成功率 %s",
		formatInt(usage.Requests),
		formatInt(usage.Success),
		formatInt(usage.Failed),
		formatPercent(usage.Success, usage.Requests),
	)
	fmt.Fprintf(&b, "\n🎟 Token %s｜输入 %s｜输出 %s",
		formatInt(usage.InputTokens+usage.OutputTokens),
		formatInt(usage.InputTokens),
		formatInt(usage.OutputTokens),
	)
	fmt.Fprintf(&b, "\n💸 费用 %s", formatFloat(usage.Cost))
	if usage.Requests > 0 {
		fmt.Fprintf(&b, "\n⏱ 首字 %dms｜耗时 %dms", usage.FtutTotal/usage.Requests, usage.UseTimeTotal/usage.Requests)
	}
	fmt.Fprintf(&b, "\n\n🧩 站点\n站点 %s（停用 %s）｜账号 %s（停用 %s）\n模型 %s（禁用 %s）",
		formatInt(sites.Sites),
		formatInt(sites.DisabledSites),
		formatInt(sites.Accounts),
		formatInt(sites.DisabledAccounts),
		formatInt(sites.Models),
		formatInt(sites.DisabledModels),
	)
	if len(sites.MissingModelRows) > 0 {
		b.WriteString("\n⚠️ 空模型")
		for _, row := range limitStrings(sites.MissingModelRows, 5) {
			b.WriteString("\n- " + shortReportName(row, 72))
		}
	}
	appendTopItems(&b, "\n\n🤖 模型 Top", models, 5)
	appendChannelTopItems(&b, "\n\n🧭 渠道 Top", channels, 5)
	appendBalanceDigest(&b, sites.Balances)
	return b.String()
}

func appendTopItems(b *strings.Builder, title string, items []topUsageItem, limit int) {
	b.WriteString(title)
	if len(items) == 0 {
		b.WriteString("\n暂无请求")
		return
	}
	for idx, item := range limitTopUsageItems(items, limit) {
		fmt.Fprintf(b, "\n%s %s\n   %s 次｜%s｜🎟 %s｜💸 %s",
			rankLabel(idx+1),
			shortReportName(item.Name, 52),
			formatInt(item.Requests),
			formatPercent(item.Success, item.Requests),
			formatInt(item.InputTokens+item.OutputTokens),
			formatFloat(item.Cost),
		)
	}
}

func appendChannelTopItems(b *strings.Builder, title string, items []topUsageItem, limit int) {
	b.WriteString(title)
	if len(items) == 0 {
		b.WriteString("\n暂无请求")
		return
	}
	for idx, item := range limitTopUsageItems(items, limit) {
		fmt.Fprintf(b, "\n%s %s\n   ✅ %s｜请求 %s｜%s｜🎟 %s｜💸 %s",
			rankLabel(idx+1),
			shortReportName(item.Name, 52),
			formatInt(item.Success),
			formatInt(item.Requests),
			formatPercent(item.Success, item.Requests),
			formatInt(item.InputTokens+item.OutputTokens),
			formatFloat(item.Cost),
		)
	}
}

func appendBalanceDigest(b *strings.Builder, balances []balanceItem) {
	filtered := make([]balanceItem, 0, len(balances))
	for _, item := range balances {
		if item.Enabled && item.Balance > 0 {
			filtered = append(filtered, item)
		}
	}
	if len(filtered) == 0 {
		return
	}
	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].Balance != filtered[j].Balance {
			return filtered[i].Balance < filtered[j].Balance
		}
		return filtered[i].AccountID < filtered[j].AccountID
	})
	b.WriteString("\n\n💰 余额最低")
	for _, item := range limitBalanceItems(filtered, 5) {
		fmt.Fprintf(b, "\n- %s / #%d %s：%s",
			shortReportName(item.SiteName, 24),
			item.AccountID,
			shortReportName(item.AccountName, 24),
			formatFloat(item.Balance),
		)
	}
}

func evaluateAlerts(ctx context.Context, now time.Time, state alertState, cooldown time.Duration) ([]string, []string) {
	alerts := make([]string, 0)
	keys := make([]string, 0)
	sites, err := loadSiteOpsSummary(ctx)
	if err != nil {
		return []string{"- 读取站点状态失败：" + err.Error()}, []string{"site_summary_error"}
	}
	threshold := settingFloat(model.SettingKeyTelegramAlertBalanceThreshold, 5)
	for _, item := range sites.Balances {
		if !item.Enabled || item.Balance <= 0 || item.Balance >= threshold {
			continue
		}
		key := fmt.Sprintf("balance:%d", item.AccountID)
		if !alertDue(state, key, now, cooldown) {
			continue
		}
		alerts = append(alerts, fmt.Sprintf("💰 余额低：%s / #%d %s\n当前 %s｜阈值 %s",
			shortReportName(item.SiteName, 32),
			item.AccountID,
			shortReportName(item.AccountName, 32),
			formatFloat(item.Balance),
			formatFloat(threshold),
		))
		keys = append(keys, key)
	}
	for _, row := range sites.MissingModelRows {
		key := "models:" + row
		if !alertDue(state, key, now, cooldown) {
			continue
		}
		alerts = append(alerts, "⚠️ 空模型："+shortReportName(row, 72))
		keys = append(keys, key)
	}
	windowMinutes := settingInt(model.SettingKeyTelegramAlertFailureWindow, 60)
	minRequests := settingInt(model.SettingKeyTelegramAlertMinRequests, 10)
	ratePct := settingInt(model.SettingKeyTelegramAlertFailureRatePct, 20)
	window := reportWindow{Label: "告警窗口", Start: now.Add(-time.Duration(windowMinutes) * time.Minute), End: now}
	usage, _, _, err := loadUsage(ctx, window)
	if err == nil && usage.Requests >= minRequests {
		failedPct := int(float64(usage.Failed) * 100 / float64(usage.Requests))
		key := "failure_rate:global"
		if failedPct >= ratePct && alertDue(state, key, now, cooldown) {
			alerts = append(alerts, fmt.Sprintf("🔥 失败率高：最近 %d 分钟\n失败 %s/%s｜失败率 %d%%｜阈值 %d%%",
				windowMinutes,
				formatInt(usage.Failed),
				formatInt(usage.Requests),
				failedPct,
				ratePct,
			))
			keys = append(keys, key)
		}
	}
	return alerts, keys
}

func sendAdminText(ctx context.Context, cfg config, client *http.Client, text string) error {
	adminIDs := make([]int64, 0, len(cfg.AdminIDs))
	for id := range cfg.AdminIDs {
		adminIDs = append(adminIDs, id)
	}
	sort.Slice(adminIDs, func(i, j int) bool { return adminIDs[i] < adminIDs[j] })
	var lastErr error
	for _, id := range adminIDs {
		if err := sendMessage(ctx, cfg, client, id, response{Text: text}); err != nil {
			lastErr = err
			log.Warnf("telegram send admin message failed: admin=%d err=%v", id, err)
		}
	}
	return lastErr
}

func telegramReportTime() (int, int) {
	value, err := op.SettingGetString(model.SettingKeyTelegramReportTime)
	if err != nil {
		return 9, 0
	}
	parts := strings.Split(value, ":")
	if len(parts) != 2 {
		return 9, 0
	}
	hour, err1 := strconv.Atoi(parts[0])
	minute, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 9, 0
	}
	return hour, minute
}

func loadAlertState() alertState {
	raw, err := op.SettingGetString(model.SettingKeyTelegramAlertState)
	if err != nil || strings.TrimSpace(raw) == "" {
		return alertState{}
	}
	var state alertState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return alertState{}
	}
	if state == nil {
		return alertState{}
	}
	return state
}

func saveAlertState(state alertState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return op.SettingSetString(model.SettingKeyTelegramAlertState, string(data))
}

func alertDue(state alertState, key string, now time.Time, cooldown time.Duration) bool {
	last, ok := state[key]
	if !ok || last <= 0 {
		return true
	}
	return now.Sub(time.Unix(last, 0)) >= cooldown
}

func settingInt(key model.SettingKey, fallback int) int {
	value, err := op.SettingGetInt(key)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func settingFloat(key model.SettingKey, fallback float64) float64 {
	raw, err := op.SettingGetString(key)
	if err != nil {
		return fallback
	}
	value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || value < 0 {
		return fallback
	}
	return value
}

func formatPercent(success int, total int) string {
	if total <= 0 {
		return "-"
	}
	return fmt.Sprintf("%.1f%%", float64(success)*100/float64(total))
}

func formatFloat(value float64) string {
	text := strconv.FormatFloat(value, 'f', 4, 64)
	text = strings.TrimRight(text, "0")
	text = strings.TrimRight(text, ".")
	if text == "" || text == "-0" {
		return "0"
	}
	return text
}

func formatInt(value int) string {
	sign := ""
	if value < 0 {
		sign = "-"
		value = -value
	}
	text := strconv.Itoa(value)
	if len(text) <= 3 {
		return sign + text
	}
	var b strings.Builder
	prefix := len(text) % 3
	if prefix == 0 {
		prefix = 3
	}
	b.WriteString(text[:prefix])
	for i := prefix; i < len(text); i += 3 {
		b.WriteByte(',')
		b.WriteString(text[i : i+3])
	}
	return sign + b.String()
}

func rankLabel(rank int) string {
	switch rank {
	case 1:
		return "🥇"
	case 2:
		return "🥈"
	case 3:
		return "🥉"
	default:
		return fmt.Sprintf("%d.", rank)
	}
}

func shortReportName(value string, limit int) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "-"
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	if limit <= 1 {
		return string(runes[:limit])
	}
	return string(runes[:limit-1]) + "…"
}

func limitTopUsageItems(items []topUsageItem, limit int) []topUsageItem {
	if len(items) <= limit {
		return items
	}
	return items[:limit]
}

func limitBalanceItems(items []balanceItem, limit int) []balanceItem {
	if len(items) <= limit {
		return items
	}
	return items[:limit]
}
