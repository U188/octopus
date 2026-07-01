package tgbot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	httpclient "github.com/U188/octopus/internal/client"
	"github.com/U188/octopus/internal/grouphealth"
	"github.com/U188/octopus/internal/model"
	"github.com/U188/octopus/internal/op"
	sitesvc "github.com/U188/octopus/internal/site"
	"github.com/U188/octopus/internal/sitesync"
	"github.com/U188/octopus/internal/utils/log"
)

const (
	defaultAPIBaseURL     = "https://api.telegram.org"
	maxMessageLen         = 3600
	maxCallbackDataBytes  = 64
	callbackAliasPrefix   = "cb:"
	callbackAliasTTL      = 24 * time.Hour
	maxCallbackAliasCount = 5000
	pendingActionTTL      = 15 * time.Minute
)

type config struct {
	Enabled             bool
	Token               string
	AdminIDs            map[int64]struct{}
	APIBaseURL          string
	ProxyMode           string
	ProxyURL            string
	PollIntervalSeconds int
}

type pendingActionKind string

const (
	pendingAddGroup            pendingActionKind = "add_group"
	pendingAddKey              pendingActionKind = "add_key"
	pendingAddModel            pendingActionKind = "add_model"
	pendingTest                pendingActionKind = "test"
	pendingAddSite             pendingActionKind = "add_site"
	pendingSiteName            pendingActionKind = "edit_site_name"
	pendingSiteBase            pendingActionKind = "edit_site_base"
	pendingCreateModelGroup    pendingActionKind = "create_model_group"
	pendingRenameModelGroup    pendingActionKind = "rename_model_group"
	pendingAddModelGroupModels pendingActionKind = "add_model_group_models"
)

type pendingAction struct {
	Kind      pendingActionKind
	SiteID    int
	AccountID int
	GroupID   int
	ChannelID int
	GroupKey  string
	ModelName string
	RouteType model.SiteModelRouteType
	ExpiresAt time.Time
}

type callbackAlias struct {
	Data      string
	ExpiresAt time.Time
}

type response struct {
	Text    string
	Buttons [][]inlineButton
}

type inlineButton struct {
	Text string
	Data string
}

type Runner struct {
	offset           int64
	mu               sync.Mutex
	pending          map[int64]pendingAction
	callbackAliases  map[string]callbackAlias
	callbackAliasSeq uint64
}

func Run(ctx context.Context) {
	r := &Runner{
		pending:         make(map[int64]pendingAction),
		callbackAliases: make(map[string]callbackAlias),
	}
	r.Run(ctx)
}

func (r *Runner) Run(ctx context.Context) {
	log.Infof("telegram bot runner started")
	defer log.Infof("telegram bot runner stopped")

	for {
		if ctx.Err() != nil {
			return
		}
		cfg, err := loadConfig()
		if err != nil {
			log.Warnf("telegram bot config load failed: %v", err)
			sleep(ctx, 5*time.Second)
			continue
		}
		if !cfg.Enabled || cfg.Token == "" || len(cfg.AdminIDs) == 0 {
			sleep(ctx, time.Duration(cfg.PollIntervalSeconds)*time.Second)
			continue
		}
		client, err := clientForConfig(cfg)
		if err != nil {
			log.Warnf("telegram bot http client init failed: %v", err)
			sleep(ctx, time.Duration(cfg.PollIntervalSeconds)*time.Second)
			continue
		}
		if err := r.pollOnce(ctx, cfg, client); err != nil && ctx.Err() == nil {
			log.Warnf("telegram bot poll failed: %v", err)
			sleep(ctx, time.Duration(cfg.PollIntervalSeconds)*time.Second)
		}
	}
}

func loadConfig() (config, error) {
	enabled, err := op.SettingGetBool(model.SettingKeyTelegramBotEnabled)
	if err != nil {
		return config{}, err
	}
	token, err := op.SettingGetString(model.SettingKeyTelegramBotToken)
	if err != nil {
		return config{}, err
	}
	adminRaw, err := op.SettingGetString(model.SettingKeyTelegramBotAdminIDs)
	if err != nil {
		return config{}, err
	}
	apiBaseURL, err := op.SettingGetString(model.SettingKeyTelegramBotAPIBaseURL)
	if err != nil {
		return config{}, err
	}
	proxyMode, err := op.SettingGetString(model.SettingKeyTelegramBotProxyMode)
	if err != nil {
		return config{}, err
	}
	proxyURL, err := op.SettingGetString(model.SettingKeyTelegramBotProxyURL)
	if err != nil {
		return config{}, err
	}
	pollInterval, err := op.SettingGetInt(model.SettingKeyTelegramBotPollInterval)
	if err != nil || pollInterval <= 0 {
		pollInterval = 5
	}
	adminIDs, err := parseAdminIDs(adminRaw)
	if err != nil {
		return config{}, err
	}
	if strings.TrimSpace(apiBaseURL) == "" {
		apiBaseURL = defaultAPIBaseURL
	}
	return config{
		Enabled:             enabled,
		Token:               strings.TrimSpace(token),
		AdminIDs:            adminIDs,
		APIBaseURL:          strings.TrimRight(strings.TrimSpace(apiBaseURL), "/"),
		ProxyMode:           strings.TrimSpace(proxyMode),
		ProxyURL:            strings.TrimSpace(proxyURL),
		PollIntervalSeconds: pollInterval,
	}, nil
}

func clientForConfig(cfg config) (*http.Client, error) {
	switch cfg.ProxyMode {
	case "system":
		return httpclient.GetHTTPClientSystemProxy(true)
	case "custom":
		return httpclient.GetHTTPClientCustomProxy(cfg.ProxyURL)
	default:
		return httpclient.GetHTTPClientSystemProxy(false)
	}
}

func parseAdminIDs(raw string) (map[int64]struct{}, error) {
	result := make(map[int64]struct{})
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '，' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	})
	for _, part := range parts {
		id, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid telegram admin id %q", part)
		}
		result[id] = struct{}{}
	}
	return result, nil
}

func buildAPIURL(baseURL string, token string, method string) (string, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	token = strings.TrimSpace(token)
	method = strings.TrimLeft(strings.TrimSpace(method), "/")
	if baseURL == "" {
		baseURL = defaultAPIBaseURL
	}
	if token == "" {
		return "", fmt.Errorf("telegram bot token is empty")
	}
	if method == "" {
		return "", fmt.Errorf("telegram api method is empty")
	}
	if strings.Contains(baseURL, "{token}") {
		baseURL = strings.ReplaceAll(baseURL, "{token}", token)
	} else {
		baseURL += "/bot" + token
	}
	return baseURL + "/" + method, nil
}

func (r *Runner) pollOnce(ctx context.Context, cfg config, client *http.Client) error {
	pollCtx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	var updates []telegramUpdate
	req := map[string]any{
		"offset":          r.offset,
		"timeout":         25,
		"allowed_updates": []string{"message", "callback_query"},
	}
	if err := telegramRequest(pollCtx, cfg, client, "getUpdates", req, &updates); err != nil {
		return err
	}
	for _, update := range updates {
		if update.UpdateID >= r.offset {
			r.offset = update.UpdateID + 1
		}
		r.handleUpdate(ctx, cfg, client, update)
	}
	return nil
}

func (r *Runner) handleUpdate(ctx context.Context, cfg config, client *http.Client, update telegramUpdate) {
	handleCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	if update.Message != nil {
		userID := update.Message.From.ID
		if !isAdmin(cfg, userID) {
			_ = sendMessage(handleCtx, cfg, client, update.Message.Chat.ID, response{Text: "未授权"})
			return
		}
		text := strings.TrimSpace(update.Message.Text)
		if text == "" {
			return
		}
		resp := r.handleMessage(handleCtx, userID, text)
		_ = sendMessage(handleCtx, cfg, client, update.Message.Chat.ID, r.prepareResponse(resp))
		return
	}

	if update.CallbackQuery != nil {
		query := update.CallbackQuery
		if !isAdmin(cfg, query.From.ID) {
			_ = answerCallback(handleCtx, cfg, client, query.ID, "未授权")
			return
		}
		_ = answerCallback(handleCtx, cfg, client, query.ID, "")
		data, ok := r.resolveCallbackData(query.Data)
		if !ok {
			if query.Message != nil {
				_ = editMessage(handleCtx, cfg, client, query.Message.Chat.ID, query.Message.MessageID, r.prepareResponse(response{
					Text:    "操作已过期，请重新打开菜单。",
					Buttons: mainMenuButtons(),
				}))
			}
			return
		}
		resp := r.handleCallback(handleCtx, query.From.ID, data)
		if query.Message != nil {
			_ = editMessage(handleCtx, cfg, client, query.Message.Chat.ID, query.Message.MessageID, r.prepareResponse(resp))
		}
	}
}

func isAdmin(cfg config, id int64) bool {
	_, ok := cfg.AdminIDs[id]
	return ok
}

func (r *Runner) handleMessage(ctx context.Context, userID int64, text string) response {
	if strings.HasPrefix(strings.TrimSpace(text), "/") {
		r.clearPending(userID)
		return r.handleCommand(ctx, text)
	}
	if action, ok := r.getPending(userID); ok {
		resp := r.fulfillPending(ctx, userID, action, text)
		if resp.Text != "" {
			return resp
		}
	}
	return response{
		Text:    "请输入命令或点击按钮开始操作。",
		Buttons: mainMenuButtons(),
	}
}

func (r *Runner) handleCommand(ctx context.Context, text string) response {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return r.helpResponse()
	}
	command := normalizeCommand(fields[0])
	args := fields[1:]
	switch command {
	case "/start", "/help":
		return r.helpResponse()
	case "/cancel":
		return response{Text: "已取消当前操作。", Buttons: mainMenuButtons()}
	case "/sites":
		return r.sitesMenu(ctx)
	case "/site":
		if len(args) < 1 {
			return response{Text: "用法：/site <site_id>", Buttons: mainMenuButtons()}
		}
		siteID, err := strconv.Atoi(args[0])
		if err != nil {
			return response{Text: "site_id 必须是数字", Buttons: mainMenuButtons()}
		}
		return r.siteMenu(ctx, siteID)
	case "/account":
		if len(args) < 2 {
			return response{Text: "用法：/account <site_id> <account_id>", Buttons: mainMenuButtons()}
		}
		siteID, err := strconv.Atoi(args[0])
		if err != nil {
			return response{Text: "site_id 必须是数字", Buttons: mainMenuButtons()}
		}
		accountID, err := strconv.Atoi(args[1])
		if err != nil {
			return response{Text: "account_id 必须是数字", Buttons: mainMenuButtons()}
		}
		return r.accountMenu(ctx, siteID, accountID)
	case "/addsite":
		return response{Text: r.addSite(ctx, args), Buttons: mainMenuButtons()}
	case "/addkey":
		return response{Text: r.addKey(ctx, args), Buttons: mainMenuButtons()}
	case "/test":
		return response{Text: r.testConversation(ctx, args), Buttons: mainMenuButtons()}
	case "/groups":
		if len(args) < 2 {
			return response{Text: "用法：/groups <site_id> <account_id>", Buttons: mainMenuButtons()}
		}
		siteID, err := strconv.Atoi(args[0])
		if err != nil {
			return response{Text: "site_id 必须是数字", Buttons: mainMenuButtons()}
		}
		accountID, err := strconv.Atoi(args[1])
		if err != nil {
			return response{Text: "account_id 必须是数字", Buttons: mainMenuButtons()}
		}
		return r.groupsMenu(ctx, siteID, accountID)
	case "/projection":
		return response{Text: r.updateProjection(ctx, args), Buttons: mainMenuButtons()}
	case "/model":
		return response{Text: r.updateModel(ctx, args), Buttons: mainMenuButtons()}
	case "/sync":
		if len(args) < 1 {
			return response{Text: "用法：/sync <account_id>", Buttons: mainMenuButtons()}
		}
		accountID, err := strconv.Atoi(args[0])
		if err != nil {
			return response{Text: "account_id 必须是数字", Buttons: mainMenuButtons()}
		}
		return response{Text: r.syncAccount(ctx, accountID), Buttons: mainMenuButtons()}
	case "/checkin":
		if len(args) < 1 {
			return response{Text: "用法：/checkin <account_id>", Buttons: mainMenuButtons()}
		}
		accountID, err := strconv.Atoi(args[0])
		if err != nil {
			return response{Text: "account_id 必须是数字", Buttons: mainMenuButtons()}
		}
		return response{Text: r.checkinAccount(ctx, accountID), Buttons: mainMenuButtons()}
	case "/logs":
		return response{Text: r.listLogs(ctx, strings.Join(args, " ")), Buttons: mainMenuButtons()}
	default:
		resp := r.helpResponse()
		resp.Text = "未知命令。\n\n" + resp.Text
		return resp
	}
}

func (r *Runner) helpResponse() response {
	return response{
		Text: strings.TrimSpace(`Octopus Telegram Bot

主菜单：
- 站点管理：新增站点、编辑站点、启停站点
- 渠道管理：查看站点投影渠道、路由组和模型
- 分组管理：把多个渠道模型合并成一个对外模型组，支持轮询/随机/故障转移/加权
- 运维：账号同步、签到、测试对话
- 监控：错误日志、站点概览

渠道管理里的“路由组”指站点来源组，例如 claude、ds；分组管理里的“分组”指可被 API key 调用的模型组。`),
		Buttons: mainMenuButtons(),
	}
}

func normalizeCommand(command string) string {
	command = strings.TrimSpace(command)
	if idx := strings.Index(command, "@"); idx >= 0 {
		command = command[:idx]
	}
	return strings.ToLower(command)
}

func (r *Runner) handleCallback(ctx context.Context, userID int64, data string) response {
	if data == "" {
		return response{Text: "空操作", Buttons: mainMenuButtons()}
	}
	r.clearPending(userID)
	switch {
	case data == "home":
		return r.helpResponse()
	case data == "site_mgmt":
		return r.sitesMenu(ctx)
	case data == "group_mgmt":
		return r.groupManagementMenu(ctx)
	case data == "model_groups":
		return r.modelGroupsMenu(ctx)
	case data == "mg:create":
		r.setPending(userID, pendingAction{Kind: pendingCreateModelGroup})
		return response{
			Text: "请发送：<分组名> [mode]\nmode 可选：round/random/failover/weighted，默认 round\n例如：gpt4 round",
			Buttons: [][]inlineButton{
				{{Text: "返回分组管理", Data: "model_groups"}},
				{{Text: "主页", Data: "home"}},
			},
		}
	case strings.HasPrefix(data, "mg:view:"):
		groupID, err := strconv.Atoi(strings.TrimPrefix(data, "mg:view:"))
		if err != nil {
			return response{Text: "分组参数无效", Buttons: mainMenuButtons()}
		}
		return r.modelGroupMenu(ctx, groupID)
	case strings.HasPrefix(data, "mg:rename:"):
		groupID, err := strconv.Atoi(strings.TrimPrefix(data, "mg:rename:"))
		if err != nil {
			return response{Text: "重命名分组参数无效", Buttons: mainMenuButtons()}
		}
		r.setPending(userID, pendingAction{Kind: pendingRenameModelGroup, GroupID: groupID})
		return response{
			Text: "请发送新的分组名。\n注意：分组名就是对外可调用模型名。",
			Buttons: [][]inlineButton{
				{{Text: "返回分组", Data: fmt.Sprintf("mg:view:%d", groupID)}},
				{{Text: "主页", Data: "home"}},
			},
		}
	case strings.HasPrefix(data, "mg:health:run:"):
		groupID, err := strconv.Atoi(strings.TrimPrefix(data, "mg:health:run:"))
		if err != nil {
			return response{Text: "分组探活参数无效", Buttons: mainMenuButtons()}
		}
		text := r.runModelGroupHealth(ctx, groupID)
		resp := r.modelGroupHealthMenu(ctx, groupID)
		resp.Text = text + "\n\n" + resp.Text
		return resp
	case strings.HasPrefix(data, "mg:health:"):
		groupID, err := strconv.Atoi(strings.TrimPrefix(data, "mg:health:"))
		if err != nil {
			return response{Text: "分组探活参数无效", Buttons: mainMenuButtons()}
		}
		return r.modelGroupHealthMenu(ctx, groupID)
	case strings.HasPrefix(data, "mg:mode:set:"):
		groupID, mode, err := parseModelGroupModeTarget(data, "mg:mode:set")
		if err != nil {
			return response{Text: "分组模式参数无效", Buttons: mainMenuButtons()}
		}
		text := r.updateModelGroupMode(ctx, groupID, mode)
		resp := r.modelGroupMenu(ctx, groupID)
		resp.Text = text + "\n\n" + resp.Text
		return resp
	case strings.HasPrefix(data, "mg:mode:"):
		groupID, err := strconv.Atoi(strings.TrimPrefix(data, "mg:mode:"))
		if err != nil {
			return response{Text: "分组模式参数无效", Buttons: mainMenuButtons()}
		}
		return r.modelGroupModeMenu(ctx, groupID)
	case strings.HasPrefix(data, "mg:add:ch:"):
		groupID, channelID, err := parsePair(data, "mg:add:ch")
		if err != nil {
			return response{Text: "添加分组模型参数无效", Buttons: mainMenuButtons()}
		}
		return r.modelGroupAddModelMenu(ctx, groupID, channelID)
	case strings.HasPrefix(data, "mg:addmodel:"):
		groupID, channelID, modelName, err := parseModelGroupModelTarget(data, "mg:addmodel")
		if err != nil {
			return response{Text: "添加分组模型参数无效", Buttons: mainMenuButtons()}
		}
		text := r.addModelGroupItems(ctx, groupID, channelID, []string{modelName})
		resp := r.modelGroupMenu(ctx, groupID)
		resp.Text = text + "\n\n" + resp.Text
		return resp
	case strings.HasPrefix(data, "mg:addtext:"):
		groupID, channelID, err := parsePair(data, "mg:addtext")
		if err != nil {
			return response{Text: "添加分组模型参数无效", Buttons: mainMenuButtons()}
		}
		r.setPending(userID, pendingAction{Kind: pendingAddModelGroupModels, GroupID: groupID, ChannelID: channelID})
		return response{
			Text: "请发送一个或多个模型名，每行一个。\n这些模型会加入当前分组并按分组模式轮询/调度。",
			Buttons: [][]inlineButton{
				{{Text: "返回分组", Data: fmt.Sprintf("mg:view:%d", groupID)}},
				{{Text: "主页", Data: "home"}},
			},
		}
	case strings.HasPrefix(data, "mg:add:"):
		groupID, err := strconv.Atoi(strings.TrimPrefix(data, "mg:add:"))
		if err != nil {
			return response{Text: "添加分组模型参数无效", Buttons: mainMenuButtons()}
		}
		return r.modelGroupAddChannelMenu(ctx, groupID)
	case strings.HasPrefix(data, "mg:item:del:"):
		groupID, itemID, err := parsePair(data, "mg:item:del")
		if err != nil {
			return response{Text: "删除分组模型参数无效", Buttons: mainMenuButtons()}
		}
		text := r.deleteModelGroupItem(ctx, itemID)
		resp := r.modelGroupMenu(ctx, groupID)
		resp.Text = text + "\n\n" + resp.Text
		return resp
	case strings.HasPrefix(data, "mg:item:prio:"):
		groupID, itemID, delta, err := parseTriple(data, "mg:item:prio")
		if err != nil {
			return response{Text: "调整优先级参数无效", Buttons: mainMenuButtons()}
		}
		text := r.adjustModelGroupItem(ctx, groupID, itemID, delta, 0)
		resp := r.modelGroupItemMenu(ctx, groupID, itemID)
		resp.Text = text + "\n\n" + resp.Text
		return resp
	case strings.HasPrefix(data, "mg:item:weight:"):
		groupID, itemID, delta, err := parseTriple(data, "mg:item:weight")
		if err != nil {
			return response{Text: "调整权重参数无效", Buttons: mainMenuButtons()}
		}
		text := r.adjustModelGroupItem(ctx, groupID, itemID, 0, delta)
		resp := r.modelGroupItemMenu(ctx, groupID, itemID)
		resp.Text = text + "\n\n" + resp.Text
		return resp
	case strings.HasPrefix(data, "mg:item:"):
		groupID, itemID, err := parsePair(data, "mg:item")
		if err != nil {
			return response{Text: "分组模型参数无效", Buttons: mainMenuButtons()}
		}
		return r.modelGroupItemMenu(ctx, groupID, itemID)
	case strings.HasPrefix(data, "mg:del:confirm:"):
		groupID, err := strconv.Atoi(strings.TrimPrefix(data, "mg:del:confirm:"))
		if err != nil {
			return response{Text: "删除分组参数无效", Buttons: mainMenuButtons()}
		}
		return response{Text: r.deleteModelGroup(ctx, groupID), Buttons: mainMenuButtons()}
	case strings.HasPrefix(data, "mg:del:"):
		groupID, err := strconv.Atoi(strings.TrimPrefix(data, "mg:del:"))
		if err != nil {
			return response{Text: "删除分组参数无效", Buttons: mainMenuButtons()}
		}
		return response{
			Text: "确认删除这个分组？删除后该组名将不能再作为模型名路由。",
			Buttons: [][]inlineButton{
				{{Text: "确认删除", Data: fmt.Sprintf("mg:del:confirm:%d", groupID)}},
				{{Text: "取消", Data: fmt.Sprintf("mg:view:%d", groupID)}},
			},
		}
	case data == "ops":
		return r.opsMenu()
	case data == "monitor":
		return r.monitorMenu(ctx)
	case data == "sites":
		return r.sitesMenu(ctx)
	case data == "logs":
		return response{Text: r.listLogs(ctx, ""), Buttons: mainMenuButtons()}
	case data == "site:add":
		r.setPending(userID, pendingAction{Kind: pendingAddSite})
		return response{
			Text: "请发送一行：<site_name> <base_url> <api_key> [platform] [account_name]\n例如：any https://example.com sk-xxx api default",
			Buttons: [][]inlineButton{
				{{Text: "返回站点管理", Data: "site_mgmt"}},
				{{Text: "主页", Data: "home"}},
			},
		}
	case strings.HasPrefix(data, "site:edit:"):
		siteID, err := strconv.Atoi(strings.TrimPrefix(data, "site:edit:"))
		if err != nil {
			return response{Text: "site edit 参数无效", Buttons: mainMenuButtons()}
		}
		return r.siteEditMenu(ctx, siteID)
	case strings.HasPrefix(data, "site:name:"):
		siteID, err := strconv.Atoi(strings.TrimPrefix(data, "site:name:"))
		if err != nil {
			return response{Text: "site name 参数无效", Buttons: mainMenuButtons()}
		}
		r.setPending(userID, pendingAction{Kind: pendingSiteName, SiteID: siteID})
		return response{
			Text: "请发送新的站点名称",
			Buttons: [][]inlineButton{
				{{Text: "返回站点编辑", Data: fmt.Sprintf("site:edit:%d", siteID)}},
				{{Text: "主页", Data: "home"}},
			},
		}
	case strings.HasPrefix(data, "site:base:"):
		siteID, err := strconv.Atoi(strings.TrimPrefix(data, "site:base:"))
		if err != nil {
			return response{Text: "site base 参数无效", Buttons: mainMenuButtons()}
		}
		r.setPending(userID, pendingAction{Kind: pendingSiteBase, SiteID: siteID})
		return response{
			Text: "请发送新的 Base URL",
			Buttons: [][]inlineButton{
				{{Text: "返回站点编辑", Data: fmt.Sprintf("site:edit:%d", siteID)}},
				{{Text: "主页", Data: "home"}},
			},
		}
	case strings.HasPrefix(data, "site:toggle:"):
		siteID, err := strconv.Atoi(strings.TrimPrefix(data, "site:toggle:"))
		if err != nil {
			return response{Text: "site toggle 参数无效", Buttons: mainMenuButtons()}
		}
		text := r.toggleSiteEnabled(ctx, siteID)
		resp := r.siteEditMenu(ctx, siteID)
		resp.Text = text + "\n\n" + resp.Text
		return resp
	case strings.HasPrefix(data, "site:codex:"):
		siteID, err := strconv.Atoi(strings.TrimPrefix(data, "site:codex:"))
		if err != nil {
			return response{Text: "site codex 参数无效", Buttons: mainMenuButtons()}
		}
		text := r.toggleSiteCodex(ctx, siteID)
		resp := r.siteEditMenu(ctx, siteID)
		resp.Text = text + "\n\n" + resp.Text
		return resp
	case strings.HasPrefix(data, "site:claude:"):
		siteID, err := strconv.Atoi(strings.TrimPrefix(data, "site:claude:"))
		if err != nil {
			return response{Text: "site claude 参数无效", Buttons: mainMenuButtons()}
		}
		text := r.toggleSiteClaude(ctx, siteID)
		resp := r.siteEditMenu(ctx, siteID)
		resp.Text = text + "\n\n" + resp.Text
		return resp
	case data == "group:create":
		return r.groupCreateSiteMenu(ctx)
	case strings.HasPrefix(data, "group:create:site:"):
		siteID, err := strconv.Atoi(strings.TrimPrefix(data, "group:create:site:"))
		if err != nil {
			return response{Text: "group create site 参数无效", Buttons: mainMenuButtons()}
		}
		return r.groupCreateAccountMenu(ctx, siteID)
	case strings.HasPrefix(data, "group:create:acct:"):
		siteID, accountID, err := parsePair(data, "group:create:acct")
		if err != nil {
			return response{Text: "group create account 参数无效", Buttons: mainMenuButtons()}
		}
		r.setPending(userID, pendingAction{Kind: pendingAddGroup, SiteID: siteID, AccountID: accountID})
		return response{
			Text: "请发送一行路由组 key，例如：ds",
			Buttons: [][]inlineButton{
				{{Text: "返回账号选择", Data: fmt.Sprintf("group:create:site:%d", siteID)}},
				{{Text: "主页", Data: "home"}},
			},
		}
	case strings.HasPrefix(data, "group:settings:"):
		siteID, accountID, groupKey, err := parseGroupTarget(data, "group:settings")
		if err != nil {
			return response{Text: "group settings 参数无效", Buttons: mainMenuButtons()}
		}
		return r.groupSettingsMenu(ctx, siteID, accountID, groupKey)
	case strings.HasPrefix(data, "groups:"):
		siteID, accountID, err := parsePair(data, "groups")
		if err != nil {
			return response{Text: "groups 参数无效", Buttons: mainMenuButtons()}
		}
		return r.groupsMenu(ctx, siteID, accountID)
	case strings.HasPrefix(data, "group:"):
		siteID, accountID, groupKey, err := parseGroupTarget(data, "group")
		if err != nil {
			return response{Text: "group 参数无效", Buttons: mainMenuButtons()}
		}
		return r.groupMenu(ctx, siteID, accountID, groupKey)
	case strings.HasPrefix(data, "proj:toggle:"):
		siteID, accountID, groupKey, err := parseGroupTarget(data, "proj:toggle")
		if err != nil {
			return response{Text: "projection 参数无效", Buttons: mainMenuButtons()}
		}
		text := r.toggleProjection(ctx, siteID, accountID, groupKey)
		resp := r.groupMenu(ctx, siteID, accountID, groupKey)
		resp.Text = text + "\n\n" + resp.Text
		return resp
	case strings.HasPrefix(data, "key:add:"):
		siteID, accountID, groupKey, err := parseGroupTarget(data, "key:add")
		if err != nil {
			return response{Text: "key 参数无效", Buttons: mainMenuButtons()}
		}
		r.setPending(userID, pendingAction{Kind: pendingAddKey, SiteID: siteID, AccountID: accountID, GroupKey: groupKey})
		return response{
			Text: fmt.Sprintf("请发送一行：<key> [name]\n当前路由组：%s", groupKey),
			Buttons: [][]inlineButton{
				{{Text: "返回路由组", Data: groupCallbackData(siteID, accountID, groupKey)}},
				{{Text: "主页", Data: "home"}},
			},
		}
	case strings.HasPrefix(data, "model:add:"):
		siteID, accountID, groupKey, err := parseGroupTarget(data, "model:add")
		if err != nil {
			return response{Text: "model 参数无效", Buttons: mainMenuButtons()}
		}
		return r.modelAddMenu(siteID, accountID, groupKey)
	case strings.HasPrefix(data, "model:addauto:"):
		siteID, accountID, groupKey, err := parseGroupTarget(data, "model:addauto")
		if err != nil {
			return response{Text: "model auto 参数无效", Buttons: mainMenuButtons()}
		}
		r.setPending(userID, pendingAction{Kind: pendingAddModel, SiteID: siteID, AccountID: accountID, GroupKey: groupKey})
		return response{
			Text: fmt.Sprintf("请发送一行或多行：<model> [route]\n未填写 route 时会自动推断\n当前路由组：%s", groupKey),
			Buttons: [][]inlineButton{
				{{Text: "选择固定 Route", Data: fmt.Sprintf("model:add:%d:%d:%s", siteID, accountID, groupKey)}},
				{{Text: "返回路由组", Data: groupCallbackData(siteID, accountID, groupKey)}},
				{{Text: "主页", Data: "home"}},
			},
		}
	case strings.HasPrefix(data, "model:addroute:"):
		siteID, accountID, groupKey, routeType, err := parseRouteTarget(data, "model:addroute")
		if err != nil {
			return response{Text: "model route 参数无效", Buttons: mainMenuButtons()}
		}
		r.setPending(userID, pendingAction{
			Kind:      pendingAddModel,
			SiteID:    siteID,
			AccountID: accountID,
			GroupKey:  groupKey,
			RouteType: routeType,
		})
		return response{
			Text: fmt.Sprintf("请发送一行或多行模型名\n固定 route：%s\n当前路由组：%s", routeType, groupKey),
			Buttons: [][]inlineButton{
				{{Text: "改为自动识别", Data: fmt.Sprintf("model:addauto:%d:%d:%s", siteID, accountID, groupKey)}},
				{{Text: "返回添加方式", Data: fmt.Sprintf("model:add:%d:%d:%s", siteID, accountID, groupKey)}},
				{{Text: "返回路由组", Data: groupCallbackData(siteID, accountID, groupKey)}},
			},
		}
	case strings.HasPrefix(data, "group:del:"):
		siteID, accountID, groupKey, err := parseGroupTarget(data, "group:del")
		if err != nil {
			return response{Text: "delete group 参数无效", Buttons: mainMenuButtons()}
		}
		text := r.deleteGroup(ctx, siteID, accountID, groupKey)
		resp := r.groupsMenu(ctx, siteID, accountID)
		resp.Text = text + "\n\n" + resp.Text
		return resp
	case strings.HasPrefix(data, "test:prepare:"):
		siteID, accountID, groupKey, err := parseGroupTarget(data, "test:prepare")
		if err != nil {
			return response{Text: "test 参数无效", Buttons: mainMenuButtons()}
		}
		r.setPending(userID, pendingAction{Kind: pendingTest, SiteID: siteID, AccountID: accountID, GroupKey: groupKey})
		return response{
			Text: "请发送一行：<token_id> <model> [client] [message]\nclient 可选 default/codex/claude",
			Buttons: [][]inlineButton{
				{{Text: "返回路由组", Data: groupCallbackData(siteID, accountID, groupKey)}},
				{{Text: "主页", Data: "home"}},
			},
		}
	case strings.HasPrefix(data, "model:list:"):
		siteID, accountID, groupKey, err := parseGroupTarget(data, "model:list")
		if err != nil {
			return response{Text: "model list 参数无效", Buttons: mainMenuButtons()}
		}
		return r.modelListMenu(ctx, siteID, accountID, groupKey)
	case strings.HasPrefix(data, "model:view:"):
		siteID, accountID, groupKey, modelName, err := parseModelTarget(data, "model:view")
		if err != nil {
			return response{Text: "model view 参数无效", Buttons: mainMenuButtons()}
		}
		return r.modelMenu(ctx, siteID, accountID, groupKey, modelName)
	case strings.HasPrefix(data, "model:toggle:"):
		siteID, accountID, groupKey, modelName, err := parseModelTarget(data, "model:toggle")
		if err != nil {
			return response{Text: "model toggle 参数无效", Buttons: mainMenuButtons()}
		}
		text := r.toggleModelDisabled(ctx, siteID, accountID, groupKey, modelName)
		resp := r.modelMenu(ctx, siteID, accountID, groupKey, modelName)
		resp.Text = text + "\n\n" + resp.Text
		return resp
	case strings.HasPrefix(data, "model:1m:"):
		siteID, accountID, groupKey, modelName, err := parseModelTarget(data, "model:1m")
		if err != nil {
			return response{Text: "model 1m 参数无效", Buttons: mainMenuButtons()}
		}
		text := r.toggleModel1M(ctx, siteID, accountID, groupKey, modelName)
		resp := r.modelMenu(ctx, siteID, accountID, groupKey, modelName)
		resp.Text = text + "\n\n" + resp.Text
		return resp
	case strings.HasPrefix(data, "model:del:confirm:"):
		siteID, accountID, groupKey, modelName, err := parseModelTarget(data, "model:del:confirm")
		if err != nil {
			return response{Text: "model delete confirm 参数无效", Buttons: mainMenuButtons()}
		}
		text := r.deleteManualModel(ctx, siteID, accountID, groupKey, modelName)
		resp := r.modelListMenu(ctx, siteID, accountID, groupKey)
		resp.Text = text + "\n\n" + resp.Text
		return resp
	case strings.HasPrefix(data, "model:del:"):
		siteID, accountID, groupKey, modelName, err := parseModelTarget(data, "model:del")
		if err != nil {
			return response{Text: "model delete 参数无效", Buttons: mainMenuButtons()}
		}
		return response{
			Text: fmt.Sprintf("确认删除模型 `%s` ?", modelName),
			Buttons: [][]inlineButton{
				{{Text: "确认删除", Data: modelCallbackData("model:del:confirm", siteID, accountID, groupKey, modelName)}},
				{{Text: "取消", Data: modelCallbackData("model:view", siteID, accountID, groupKey, modelName)}},
				{{Text: "返回模型列表", Data: fmt.Sprintf("model:list:%d:%d:%s", siteID, accountID, groupKey)}},
			},
		}
	case strings.HasPrefix(data, "sync:"):
		accountID, err := strconv.Atoi(strings.TrimPrefix(data, "sync:"))
		if err != nil {
			return response{Text: "sync 参数无效", Buttons: mainMenuButtons()}
		}
		return response{Text: r.syncAccount(ctx, accountID), Buttons: mainMenuButtons()}
	case strings.HasPrefix(data, "checkin:"):
		accountID, err := strconv.Atoi(strings.TrimPrefix(data, "checkin:"))
		if err != nil {
			return response{Text: "checkin 参数无效", Buttons: mainMenuButtons()}
		}
		return response{Text: r.checkinAccount(ctx, accountID), Buttons: mainMenuButtons()}
	case strings.HasPrefix(data, "acct:"):
		siteID, accountID, err := parsePair(data, "acct")
		if err != nil {
			return response{Text: "account 参数无效", Buttons: mainMenuButtons()}
		}
		return r.accountMenu(ctx, siteID, accountID)
	case strings.HasPrefix(data, "site:"):
		siteID, err := strconv.Atoi(strings.TrimPrefix(data, "site:"))
		if err != nil {
			return response{Text: "site 参数无效", Buttons: mainMenuButtons()}
		}
		return r.siteMenu(ctx, siteID)
	default:
		return response{Text: "未知操作", Buttons: mainMenuButtons()}
	}

}

func (r *Runner) modelGroupsMenu(ctx context.Context) response {
	groups, err := op.GroupList(ctx)
	if err != nil {
		return response{Text: "读取分组失败：" + err.Error(), Buttons: mainMenuButtons()}
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].Pinned != groups[j].Pinned {
			return groups[i].Pinned
		}
		return strings.ToLower(groups[i].Name) < strings.ToLower(groups[j].Name)
	})
	var b strings.Builder
	b.WriteString("分组管理\n把多个渠道模型合并成一个对外模型组，客户端请求分组名即可按模式调度组内模型。")
	buttons := make([][]inlineButton, 0, len(groups)+4)
	for _, group := range groups {
		fmt.Fprintf(&b, "\n#%d %s mode=%s items=%d", group.ID, group.Name, groupModeLabel(group.Mode), len(group.Items))
		buttons = append(buttons, []inlineButton{{Text: fmt.Sprintf("#%d %s", group.ID, trimForButton(group.Name, 22)), Data: fmt.Sprintf("mg:view:%d", group.ID)}})
	}
	if len(groups) == 0 {
		b.WriteString("\n暂无分组")
	}
	buttons = append(buttons, []inlineButton{{Text: "创建分组", Data: "mg:create"}})
	buttons = append(buttons, []inlineButton{{Text: "渠道管理", Data: "group_mgmt"}, {Text: "主页", Data: "home"}})
	return response{Text: b.String(), Buttons: buttons}
}

func (r *Runner) modelGroupMenu(ctx context.Context, groupID int) response {
	group, err := op.GroupGet(groupID, ctx)
	if err != nil {
		return response{Text: "读取分组失败：" + err.Error(), Buttons: [][]inlineButton{{{Text: "返回分组管理", Data: "model_groups"}}}}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "分组 #%d %s\n对外模型名：%s\n模式：%s\n模型数：%d", group.ID, group.Name, group.Name, groupModeLabel(group.Mode), len(group.Items))
	if group.MatchRegex != "" {
		fmt.Fprintf(&b, "\n匹配正则：%s", group.MatchRegex)
	}
	if group.FirstTokenTimeOut > 0 {
		fmt.Fprintf(&b, "\n首 token 超时：%ds", group.FirstTokenTimeOut)
	}
	if group.SessionKeepTime > 0 {
		fmt.Fprintf(&b, "\n会话保持：%ds", group.SessionKeepTime)
	}
	if len(group.Items) > 0 {
		b.WriteString("\n\n组内模型:")
		items := append([]model.GroupItem(nil), group.Items...)
		sort.Slice(items, func(i, j int) bool {
			if items[i].Priority != items[j].Priority {
				return items[i].Priority < items[j].Priority
			}
			if items[i].ChannelID != items[j].ChannelID {
				return items[i].ChannelID < items[j].ChannelID
			}
			return items[i].ModelName < items[j].ModelName
		})
		for _, item := range items {
			channelName := fmt.Sprintf("channel#%d", item.ChannelID)
			if channel, err := op.ChannelGet(item.ChannelID, ctx); err == nil {
				channelName = channel.Name
			}
			fmt.Fprintf(&b, "\n- #%d %s / %s priority=%d weight=%d", item.ID, channelName, item.ModelName, item.Priority, item.Weight)
		}
	}
	buttons := [][]inlineButton{
		{{Text: "添加模型", Data: fmt.Sprintf("mg:add:%d", group.ID)}, {Text: "切换模式", Data: fmt.Sprintf("mg:mode:%d", group.ID)}},
		{{Text: "重命名", Data: fmt.Sprintf("mg:rename:%d", group.ID)}, {Text: "探活", Data: fmt.Sprintf("mg:health:%d", group.ID)}},
	}
	for _, item := range group.Items {
		buttons = append(buttons, []inlineButton{{Text: trimForButton(item.ModelName, 24), Data: fmt.Sprintf("mg:item:%d:%d", group.ID, item.ID)}})
	}
	buttons = append(buttons, []inlineButton{{Text: "删除分组", Data: fmt.Sprintf("mg:del:%d", group.ID)}})
	buttons = append(buttons, []inlineButton{{Text: "返回分组管理", Data: "model_groups"}, {Text: "主页", Data: "home"}})
	return response{Text: b.String(), Buttons: buttons}
}

func (r *Runner) modelGroupHealthMenu(ctx context.Context, groupID int) response {
	view, err := grouphealth.NewService(nil, nil).GetGroupHealthViewByID(ctx, groupID)
	if err != nil {
		return response{Text: "读取分组探活失败：" + err.Error(), Buttons: [][]inlineButton{{{Text: "返回分组", Data: fmt.Sprintf("mg:view:%d", groupID)}}}}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "分组探活\n分组：%s\n模式：%s", view.GroupName, groupModeLabel(view.GroupMode))
	if view.Latest == nil {
		b.WriteString("\n暂无探活记录")
	} else {
		snapshot := view.Latest
		fmt.Fprintf(&b, "\n状态：%s\n消息：%s\n耗时：%dms\n开始：%s", snapshot.Status, firstNonEmpty(snapshot.Message, "-"), snapshot.DurationMS, snapshot.StartedAt.Format("2006-01-02 15:04:05"))
		if snapshot.FinishedAt != nil {
			fmt.Fprintf(&b, "\n结束：%s", snapshot.FinishedAt.Format("2006-01-02 15:04:05"))
		}
	}
	return response{
		Text: b.String(),
		Buttons: [][]inlineButton{
			{{Text: "运行探活", Data: fmt.Sprintf("mg:health:run:%d", groupID)}},
			{{Text: "返回分组", Data: fmt.Sprintf("mg:view:%d", groupID)}, {Text: "主页", Data: "home"}},
		},
	}
}

func (r *Runner) modelGroupItemMenu(ctx context.Context, groupID int, itemID int) response {
	group, item, ok := r.findModelGroupItem(ctx, groupID, itemID)
	if !ok {
		return response{Text: "分组模型不存在", Buttons: [][]inlineButton{{{Text: "返回分组", Data: fmt.Sprintf("mg:view:%d", groupID)}}}}
	}
	channelName := fmt.Sprintf("channel#%d", item.ChannelID)
	if channel, err := op.ChannelGet(item.ChannelID, ctx); err == nil {
		channelName = channel.Name
	}
	text := fmt.Sprintf("分组模型\n分组：%s\n对外模型名：%s\n渠道：%s\n模型：%s\npriority=%d\nweight=%d", group.Name, group.Name, channelName, item.ModelName, item.Priority, item.Weight)
	return response{
		Text: text,
		Buttons: [][]inlineButton{
			{{Text: "优先级 -1", Data: fmt.Sprintf("mg:item:prio:%d:%d:-1", groupID, itemID)}, {Text: "优先级 +1", Data: fmt.Sprintf("mg:item:prio:%d:%d:1", groupID, itemID)}},
			{{Text: "权重 -1", Data: fmt.Sprintf("mg:item:weight:%d:%d:-1", groupID, itemID)}, {Text: "权重 +1", Data: fmt.Sprintf("mg:item:weight:%d:%d:1", groupID, itemID)}},
			{{Text: "删除模型", Data: fmt.Sprintf("mg:item:del:%d:%d", groupID, itemID)}},
			{{Text: "返回分组", Data: fmt.Sprintf("mg:view:%d", groupID)}, {Text: "主页", Data: "home"}},
		},
	}
}

func (r *Runner) modelGroupModeMenu(ctx context.Context, groupID int) response {
	group, err := op.GroupGet(groupID, ctx)
	if err != nil {
		return response{Text: "读取分组失败：" + err.Error(), Buttons: mainMenuButtons()}
	}
	return response{
		Text: fmt.Sprintf("切换分组模式\n分组：%s\n当前：%s", group.Name, groupModeLabel(group.Mode)),
		Buttons: [][]inlineButton{
			{{Text: "轮询", Data: fmt.Sprintf("mg:mode:set:%d:%d", group.ID, model.GroupModeRoundRobin)}, {Text: "随机", Data: fmt.Sprintf("mg:mode:set:%d:%d", group.ID, model.GroupModeRandom)}},
			{{Text: "故障转移", Data: fmt.Sprintf("mg:mode:set:%d:%d", group.ID, model.GroupModeFailover)}, {Text: "加权", Data: fmt.Sprintf("mg:mode:set:%d:%d", group.ID, model.GroupModeWeighted)}},
			{{Text: "返回分组", Data: fmt.Sprintf("mg:view:%d", group.ID)}},
		},
	}
}

func (r *Runner) modelGroupAddChannelMenu(ctx context.Context, groupID int) response {
	group, err := op.GroupGet(groupID, ctx)
	if err != nil {
		return response{Text: "读取分组失败：" + err.Error(), Buttons: mainMenuButtons()}
	}
	channels, err := op.ChannelList(ctx)
	if err != nil {
		return response{Text: "读取渠道失败：" + err.Error(), Buttons: mainMenuButtons()}
	}
	sort.Slice(channels, func(i, j int) bool { return channels[i].ID < channels[j].ID })
	var b strings.Builder
	fmt.Fprintf(&b, "添加分组模型\n分组：%s\n先选择渠道，再发送模型名。", group.Name)
	buttons := make([][]inlineButton, 0, len(channels)+2)
	for _, ch := range channels {
		models := splitModelCSV(ch.Model, ch.CustomModel)
		modelHint := ""
		if len(models) > 0 {
			modelHint = " models=" + strings.Join(limitStrings(models, 3), ",")
		}
		fmt.Fprintf(&b, "\n#%d %s enabled=%t%s", ch.ID, ch.Name, ch.Enabled, modelHint)
		buttons = append(buttons, []inlineButton{{Text: fmt.Sprintf("#%d %s", ch.ID, trimForButton(ch.Name, 20)), Data: fmt.Sprintf("mg:add:ch:%d:%d", group.ID, ch.ID)}})
	}
	if len(channels) == 0 {
		b.WriteString("\n暂无渠道")
	}
	buttons = append(buttons, []inlineButton{{Text: "返回分组", Data: fmt.Sprintf("mg:view:%d", group.ID)}, {Text: "主页", Data: "home"}})
	return response{Text: b.String(), Buttons: buttons}
}

func (r *Runner) modelGroupAddModelMenu(ctx context.Context, groupID int, channelID int) response {
	group, err := op.GroupGet(groupID, ctx)
	if err != nil {
		return response{Text: "读取分组失败：" + err.Error(), Buttons: mainMenuButtons()}
	}
	channel, err := op.ChannelGet(channelID, ctx)
	if err != nil {
		return response{Text: "读取渠道失败：" + err.Error(), Buttons: mainMenuButtons()}
	}
	models := splitModelCSV(channel.Model, channel.CustomModel)
	var b strings.Builder
	fmt.Fprintf(&b, "添加分组模型\n分组：%s\n渠道：%s\n选择已有模型，或手动输入模型名。", group.Name, channel.Name)
	buttons := make([][]inlineButton, 0, len(models)+3)
	for _, modelName := range models {
		buttons = append(buttons, []inlineButton{{Text: trimForButton(modelName, 28), Data: fmt.Sprintf("mg:addmodel:%d:%d:%s", groupID, channelID, modelName)}})
	}
	if len(models) == 0 {
		b.WriteString("\n该渠道没有配置模型列表")
	}
	buttons = append(buttons, []inlineButton{{Text: "手动输入", Data: fmt.Sprintf("mg:addtext:%d:%d", groupID, channelID)}})
	buttons = append(buttons, []inlineButton{{Text: "返回渠道选择", Data: fmt.Sprintf("mg:add:%d", groupID)}, {Text: "返回分组", Data: fmt.Sprintf("mg:view:%d", groupID)}})
	return response{Text: b.String(), Buttons: buttons}
}

func (r *Runner) sitesMenu(ctx context.Context) response {
	sites, err := op.SiteList(ctx)
	if err != nil {
		return response{Text: "读取站点失败：" + err.Error(), Buttons: mainMenuButtons()}
	}
	var b strings.Builder
	b.WriteString("站点管理")
	buttons := make([][]inlineButton, 0, len(sites)+2)
	for _, site := range sites {
		fmt.Fprintf(&b, "\n#%d %s [%s] accounts=%d enabled=%t", site.ID, site.Name, site.Platform, len(site.Accounts), site.Enabled)
		buttons = append(buttons, []inlineButton{
			{Text: fmt.Sprintf("#%d %s", site.ID, trimForButton(site.Name, 18)), Data: fmt.Sprintf("site:%d", site.ID)},
			{Text: "编辑", Data: fmt.Sprintf("site:edit:%d", site.ID)},
		})
	}
	if len(sites) == 0 {
		b.WriteString("\n暂无站点")
	}
	buttons = append(buttons, []inlineButton{{Text: "新增站点", Data: "site:add"}})
	buttons = append(buttons, []inlineButton{{Text: "渠道管理", Data: "group_mgmt"}, {Text: "运维", Data: "ops"}})
	buttons = append(buttons, []inlineButton{{Text: "主页", Data: "home"}})
	return response{Text: b.String(), Buttons: buttons}
}

func (r *Runner) groupManagementMenu(ctx context.Context) response {
	cards, err := op.SiteChannelListWithOptions(ctx, op.SiteChannelListOptions{IncludeHistory: false})
	if err != nil {
		return response{Text: "读取分组失败：" + err.Error(), Buttons: mainMenuButtons()}
	}
	var b strings.Builder
	b.WriteString("渠道管理")
	buttons := make([][]inlineButton, 0, 16)
	groupCount := 0
	for _, site := range cards {
		for _, account := range site.Accounts {
			for _, group := range account.Groups {
				groupCount++
				fmt.Fprintf(&b, "\n- %s | #%d %s / #%d %s | models=%d keys=%d/%d projection=%s",
					group.GroupKey,
					site.SiteID,
					site.SiteName,
					account.AccountID,
					account.AccountName,
					len(group.Models),
					group.EnabledKeyCount,
					group.KeyCount,
					onOff(!group.ProjectionDisabled),
				)
				buttons = append(buttons, []inlineButton{{
					Text: fmt.Sprintf("%s | %s/%s", trimForButton(group.GroupKey, 10), trimForButton(site.SiteName, 8), trimForButton(account.AccountName, 8)),
					Data: groupCallbackData(site.SiteID, account.AccountID, group.GroupKey),
				}})
			}
		}
	}
	if groupCount == 0 {
		b.WriteString("\n暂无路由组")
	}
	buttons = append(buttons, []inlineButton{{Text: "创建路由组", Data: "group:create"}})
	buttons = append(buttons, []inlineButton{{Text: "站点管理", Data: "site_mgmt"}, {Text: "运维", Data: "ops"}})
	buttons = append(buttons, []inlineButton{{Text: "主页", Data: "home"}})
	return response{Text: b.String(), Buttons: buttons}
}

func (r *Runner) groupCreateSiteMenu(ctx context.Context) response {
	sites, err := op.SiteList(ctx)
	if err != nil {
		return response{Text: "读取站点失败：" + err.Error(), Buttons: mainMenuButtons()}
	}
	var b strings.Builder
	b.WriteString("创建路由组\n先选择站点")
	buttons := make([][]inlineButton, 0, len(sites)+2)
	for _, site := range sites {
		fmt.Fprintf(&b, "\n- #%d %s [%s] accounts=%d", site.ID, site.Name, site.Platform, len(site.Accounts))
		buttons = append(buttons, []inlineButton{{
			Text: fmt.Sprintf("#%d %s", site.ID, trimForButton(site.Name, 22)),
			Data: fmt.Sprintf("group:create:site:%d", site.ID),
		}})
	}
	if len(sites) == 0 {
		b.WriteString("\n暂无站点")
	}
	buttons = append(buttons, []inlineButton{{Text: "新增站点", Data: "site:add"}})
	buttons = append(buttons, []inlineButton{{Text: "返回渠道管理", Data: "group_mgmt"}, {Text: "主页", Data: "home"}})
	return response{Text: b.String(), Buttons: buttons}
}

func (r *Runner) groupCreateAccountMenu(ctx context.Context, siteID int) response {
	site, err := op.SiteGet(siteID, ctx)
	if err != nil {
		return response{Text: "读取站点失败：" + err.Error(), Buttons: mainMenuButtons()}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "创建路由组\n站点：#%d %s", site.ID, site.Name)
	buttons := make([][]inlineButton, 0, len(site.Accounts)+2)
	for _, account := range site.Accounts {
		fmt.Fprintf(&b, "\n- #%d %s enabled=%t groups=%d models=%d", account.ID, account.Name, account.Enabled, len(account.UserGroups), len(account.Models))
		buttons = append(buttons, []inlineButton{{
			Text: fmt.Sprintf("#%d %s", account.ID, trimForButton(account.Name, 22)),
			Data: fmt.Sprintf("group:create:acct:%d:%d", site.ID, account.ID),
		}})
	}
	if len(site.Accounts) == 0 {
		b.WriteString("\n该站点暂无账号")
	}
	buttons = append(buttons, []inlineButton{{Text: "返回站点选择", Data: "group:create"}, {Text: "返回站点详情", Data: fmt.Sprintf("site:%d", site.ID)}})
	buttons = append(buttons, []inlineButton{{Text: "主页", Data: "home"}})
	return response{Text: b.String(), Buttons: buttons}
}

func (r *Runner) opsMenu() response {
	return response{
		Text: strings.TrimSpace(`运维

- 从站点或账号页进入，可执行同步、签到
- 从路由组页进入，可执行测试对话
- 这里保留快捷入口到站点和渠道管理`),
		Buttons: [][]inlineButton{
			{{Text: "站点管理", Data: "site_mgmt"}, {Text: "渠道管理", Data: "group_mgmt"}},
			{{Text: "监控", Data: "monitor"}, {Text: "主页", Data: "home"}},
		},
	}
}

func (r *Runner) monitorMenu(ctx context.Context) response {
	cards, err := op.SiteChannelListWithOptions(ctx, op.SiteChannelListOptions{IncludeHistory: false})
	if err != nil {
		return response{Text: "读取监控概览失败：" + err.Error(), Buttons: mainMenuButtons()}
	}
	var siteCount, accountCount, groupCount, modelCount, disabledSiteCount int
	for _, site := range cards {
		siteCount++
		if !site.Enabled {
			disabledSiteCount++
		}
		accountCount += len(site.Accounts)
		for _, account := range site.Accounts {
			groupCount += len(account.Groups)
			modelCount += account.ModelCount
		}
	}
	logSummary := r.listLogs(ctx, "")
	text := fmt.Sprintf("监控概览\n站点：%d（停用 %d）\n账号：%d\n路由组：%d\n模型：%d\n\n%s", siteCount, disabledSiteCount, accountCount, groupCount, modelCount, logSummary)
	return response{
		Text: text,
		Buttons: [][]inlineButton{
			{{Text: "错误日志", Data: "logs"}, {Text: "站点管理", Data: "site_mgmt"}},
			{{Text: "渠道管理", Data: "group_mgmt"}, {Text: "主页", Data: "home"}},
		},
	}
}

func (r *Runner) siteMenu(ctx context.Context, siteID int) response {
	site, err := op.SiteGet(siteID, ctx)
	if err != nil {
		return response{Text: "读取站点失败：" + err.Error(), Buttons: mainMenuButtons()}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "站点 #%d %s\n平台：%s\nBaseURL：%s\n启用：%t\nCodex：%t Claude：%t\n账号：%d", site.ID, site.Name, site.Platform, site.BaseURL, site.Enabled, site.CodexMode, site.ClaudeMode, len(site.Accounts))
	buttons := make([][]inlineButton, 0, len(site.Accounts)+1)
	for _, account := range site.Accounts {
		fmt.Fprintf(&b, "\n- account #%d %s enabled=%t groups=%d keys=%d models=%d", account.ID, account.Name, account.Enabled, len(account.UserGroups), len(account.Tokens), len(account.Models))
		buttons = append(buttons, []inlineButton{{Text: fmt.Sprintf("#%d %s", account.ID, trimForButton(account.Name, 22)), Data: fmt.Sprintf("acct:%d:%d", site.ID, account.ID)}})
	}
	buttons = append(buttons, []inlineButton{{Text: "编辑站点", Data: fmt.Sprintf("site:edit:%d", site.ID)}, {Text: "返回站点管理", Data: "site_mgmt"}})
	buttons = append(buttons, []inlineButton{{Text: "主页", Data: "home"}})
	return response{Text: b.String(), Buttons: buttons}
}

func (r *Runner) siteEditMenu(ctx context.Context, siteID int) response {
	site, err := op.SiteGet(siteID, ctx)
	if err != nil {
		return response{Text: "读取站点失败：" + err.Error(), Buttons: mainMenuButtons()}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "编辑站点 #%d %s\n平台：%s\nBaseURL：%s\n启用：%t\nCodex：%t Claude：%t", site.ID, site.Name, site.Platform, site.BaseURL, site.Enabled, site.CodexMode, site.ClaudeMode)
	buttons := [][]inlineButton{
		{{Text: "改名称", Data: fmt.Sprintf("site:name:%d", site.ID)}, {Text: "改 BaseURL", Data: fmt.Sprintf("site:base:%d", site.ID)}},
		{{Text: siteEnabledLabel(site.Enabled), Data: fmt.Sprintf("site:toggle:%d", site.ID)}},
		{{Text: siteCodexLabel(site.CodexMode), Data: fmt.Sprintf("site:codex:%d", site.ID)}, {Text: siteClaudeLabel(site.ClaudeMode), Data: fmt.Sprintf("site:claude:%d", site.ID)}},
		{{Text: "查看账号", Data: fmt.Sprintf("site:%d", site.ID)}, {Text: "返回站点管理", Data: "site_mgmt"}},
	}
	return response{Text: b.String(), Buttons: buttons}
}

func (r *Runner) accountMenu(ctx context.Context, siteID int, accountID int) response {
	account, err := op.SiteChannelAccountGet(siteID, accountID, ctx)
	if err != nil {
		return response{Text: "读取账号失败：" + err.Error(), Buttons: mainMenuButtons()}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "账号 #%d %s\n启用：%t 自动同步：%t\n路由组：%d 模型：%d", account.AccountID, account.AccountName, account.Enabled, account.AutoSync, account.GroupCount, account.ModelCount)
	for _, group := range account.Groups {
		fmt.Fprintf(&b, "\n- %s projection=%s keys=%d/%d models=%d status=%s", group.GroupKey, onOff(!group.ProjectionDisabled), group.EnabledKeyCount, group.KeyCount, len(group.Models), group.ModelSyncStatus)
	}
	buttons := [][]inlineButton{
		{{Text: "路由组模型", Data: fmt.Sprintf("groups:%d:%d", siteID, accountID)}},
		{{Text: "同步", Data: fmt.Sprintf("sync:%d", accountID)}, {Text: "签到", Data: fmt.Sprintf("checkin:%d", accountID)}},
		{{Text: "返回站点", Data: fmt.Sprintf("site:%d", siteID)}, {Text: "主页", Data: "home"}},
	}
	return response{Text: b.String(), Buttons: buttons}
}

func (r *Runner) groupsMenu(ctx context.Context, siteID int, accountID int) response {
	account, err := op.SiteChannelAccountGet(siteID, accountID, ctx)
	if err != nil {
		return response{Text: "读取路由组失败：" + err.Error(), Buttons: mainMenuButtons()}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "账号 #%d 路由组列表（如 claude、ds）", accountID)
	buttons := make([][]inlineButton, 0, len(account.Groups)+2)
	for _, group := range account.Groups {
		fmt.Fprintf(&b, "\n- %s projection=%s keys=%d/%d models=%d status=%s", group.GroupKey, onOff(!group.ProjectionDisabled), group.EnabledKeyCount, group.KeyCount, len(group.Models), group.ModelSyncStatus)
		buttons = append(buttons, []inlineButton{{Text: trimForButton(group.GroupKey, 28), Data: groupCallbackData(siteID, accountID, group.GroupKey)}})
	}
	buttons = append(buttons, []inlineButton{{Text: "新增路由组", Data: fmt.Sprintf("group:create:acct:%d:%d", siteID, accountID)}})
	buttons = append(buttons, []inlineButton{{Text: "返回账号", Data: fmt.Sprintf("acct:%d:%d", siteID, accountID)}, {Text: "渠道总览", Data: "group_mgmt"}})
	buttons = append(buttons, []inlineButton{{Text: "主页", Data: "home"}})
	return response{Text: b.String(), Buttons: buttons}
}

func (r *Runner) groupMenu(ctx context.Context, siteID int, accountID int, groupKey string) response {
	account, err := op.SiteChannelAccountGet(siteID, accountID, ctx)
	if err != nil {
		return response{Text: "读取路由组失败：" + err.Error(), Buttons: mainMenuButtons()}
	}
	group, ok := findGroup(account.Groups, groupKey)
	if !ok {
		return response{Text: "路由组不存在：" + groupKey, Buttons: [][]inlineButton{{{Text: "返回路由组列表", Data: fmt.Sprintf("groups:%d:%d", siteID, accountID)}}}}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "路由组 [%s]\n投影：%s\nKey：%d/%d\n模型：%d\n状态：%s", group.GroupKey, onOff(!group.ProjectionDisabled), group.EnabledKeyCount, group.KeyCount, len(group.Models), group.ModelSyncStatus)
	if group.ProjectionSuspended {
		fmt.Fprintf(&b, "\n系统暂停：%s", firstNonEmpty(group.ProjectionSuspendReason, "unknown"))
	}
	if len(group.SourceKeys) > 0 {
		b.WriteString("\n\nKeys:")
		for _, key := range group.SourceKeys {
			fmt.Fprintf(&b, "\n- #%d %s enabled=%t status=%s", key.ID, firstNonEmpty(key.Name, key.TokenMasked), key.Enabled, key.ValueStatus)
		}
	}
	buttons := [][]inlineButton{
		{{Text: "模型列表", Data: fmt.Sprintf("model:list:%d:%d:%s", siteID, accountID, group.GroupKey)}, {Text: "添加模型", Data: fmt.Sprintf("model:add:%d:%d:%s", siteID, accountID, group.GroupKey)}},
		{{Text: "测试对话", Data: fmt.Sprintf("test:prepare:%d:%d:%s", siteID, accountID, group.GroupKey)}},
		{{Text: "更多设置", Data: fmt.Sprintf("group:settings:%d:%d:%s", siteID, accountID, group.GroupKey)}},
		{{Text: "返回路由组列表", Data: fmt.Sprintf("groups:%d:%d", siteID, accountID)}, {Text: "主页", Data: "home"}},
	}
	return response{Text: b.String(), Buttons: buttons}
}

func (r *Runner) groupSettingsMenu(ctx context.Context, siteID int, accountID int, groupKey string) response {
	account, err := op.SiteChannelAccountGet(siteID, accountID, ctx)
	if err != nil {
		return response{Text: "读取路由组设置失败：" + err.Error(), Buttons: mainMenuButtons()}
	}
	group, ok := findGroup(account.Groups, groupKey)
	if !ok {
		return response{Text: "路由组不存在：" + groupKey, Buttons: [][]inlineButton{{{Text: "返回路由组列表", Data: fmt.Sprintf("groups:%d:%d", siteID, accountID)}}}}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "路由组 [%s] 更多设置\n投影：%s\nKey：%d/%d", group.GroupKey, onOff(!group.ProjectionDisabled), group.EnabledKeyCount, group.KeyCount)
	buttons := [][]inlineButton{
		{{Text: "添加 Key", Data: fmt.Sprintf("key:add:%d:%d:%s", siteID, accountID, group.GroupKey)}},
		{{Text: projectionToggleLabel(group.ProjectionDisabled), Data: fmt.Sprintf("proj:toggle:%d:%d:%s", siteID, accountID, group.GroupKey)}},
		{{Text: "删除路由组", Data: fmt.Sprintf("group:del:%d:%d:%s", siteID, accountID, group.GroupKey)}},
		{{Text: "返回路由组", Data: groupCallbackData(siteID, accountID, group.GroupKey)}, {Text: "主页", Data: "home"}},
	}
	return response{Text: b.String(), Buttons: buttons}
}

func (r *Runner) modelAddMenu(siteID int, accountID int, groupKey string) response {
	return response{
		Text: fmt.Sprintf("添加模型\n路由组：%s\n先选择添加方式", groupKey),
		Buttons: [][]inlineButton{
			{{Text: "自动识别 Route", Data: fmt.Sprintf("model:addauto:%d:%d:%s", siteID, accountID, groupKey)}},
			{{Text: "OpenAI Chat", Data: fmt.Sprintf("model:addroute:%d:%d:%s:%s", siteID, accountID, groupKey, model.SiteModelRouteTypeOpenAIChat)}},
			{{Text: "OpenAI Response", Data: fmt.Sprintf("model:addroute:%d:%d:%s:%s", siteID, accountID, groupKey, model.SiteModelRouteTypeOpenAIResponse)}},
			{{Text: "Anthropic", Data: fmt.Sprintf("model:addroute:%d:%d:%s:%s", siteID, accountID, groupKey, model.SiteModelRouteTypeAnthropic)}},
			{{Text: "Gemini", Data: fmt.Sprintf("model:addroute:%d:%d:%s:%s", siteID, accountID, groupKey, model.SiteModelRouteTypeGemini)}},
			{{Text: "Volcengine", Data: fmt.Sprintf("model:addroute:%d:%d:%s:%s", siteID, accountID, groupKey, model.SiteModelRouteTypeVolcengine)}},
			{{Text: "Embedding", Data: fmt.Sprintf("model:addroute:%d:%d:%s:%s", siteID, accountID, groupKey, model.SiteModelRouteTypeOpenAIEmbedding)}},
			{{Text: "返回路由组", Data: groupCallbackData(siteID, accountID, groupKey)}, {Text: "主页", Data: "home"}},
		},
	}
}

func (r *Runner) modelListMenu(ctx context.Context, siteID int, accountID int, groupKey string) response {
	account, err := op.SiteChannelAccountGet(siteID, accountID, ctx)
	if err != nil {
		return response{Text: "读取模型失败：" + err.Error(), Buttons: mainMenuButtons()}
	}
	group, ok := findGroup(account.Groups, groupKey)
	if !ok {
		return response{Text: "路由组不存在：" + groupKey, Buttons: [][]inlineButton{{{Text: "返回路由组", Data: fmt.Sprintf("groups:%d:%d", siteID, accountID)}}}}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "路由组 %s 模型列表", group.GroupKey)
	buttons := make([][]inlineButton, 0, len(group.Models)+2)
	for _, item := range group.Models {
		fmt.Fprintf(&b, "\n- %s route=%s enabled=%t 1m=%t source=%s", item.ModelName, item.RouteType, !item.Disabled, item.Context1M, item.Source)
		buttons = append(buttons, []inlineButton{{Text: trimForButton(item.ModelName, 28), Data: modelCallbackData("model:view", siteID, accountID, group.GroupKey, item.ModelName)}})
	}
	buttons = append(buttons, []inlineButton{{Text: "添加模型", Data: fmt.Sprintf("model:add:%d:%d:%s", siteID, accountID, group.GroupKey)}})
	buttons = append(buttons, []inlineButton{{Text: "返回路由组", Data: groupCallbackData(siteID, accountID, group.GroupKey)}, {Text: "主页", Data: "home"}})
	return response{Text: b.String(), Buttons: buttons}
}

func (r *Runner) modelMenu(ctx context.Context, siteID int, accountID int, groupKey string, modelName string) response {
	account, err := op.SiteChannelAccountGet(siteID, accountID, ctx)
	if err != nil {
		return response{Text: "读取模型失败：" + err.Error(), Buttons: mainMenuButtons()}
	}
	group, ok := findGroup(account.Groups, groupKey)
	if !ok {
		return response{Text: "路由组不存在：" + groupKey, Buttons: [][]inlineButton{{{Text: "返回路由组", Data: fmt.Sprintf("groups:%d:%d", siteID, accountID)}}}}
	}
	modelItem, ok := findModel(group.Models, modelName)
	if !ok {
		return response{Text: "模型不存在：" + modelName, Buttons: [][]inlineButton{{{Text: "返回模型列表", Data: fmt.Sprintf("model:list:%d:%d:%s", siteID, accountID, group.GroupKey)}}}}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "模型 %s\nroute=%s\nsource=%s\nenabled=%t\n1m=%t", modelItem.ModelName, modelItem.RouteType, modelItem.Source, !modelItem.Disabled, modelItem.Context1M)
	buttons := [][]inlineButton{
		{{Text: modelToggleLabel(modelItem.Disabled), Data: modelCallbackData("model:toggle", siteID, accountID, group.GroupKey, modelItem.ModelName)}, {Text: model1MLabel(modelItem.Context1M), Data: modelCallbackData("model:1m", siteID, accountID, group.GroupKey, modelItem.ModelName)}},
	}
	if strings.EqualFold(strings.TrimSpace(modelItem.Source), "manual") {
		buttons = append(buttons, []inlineButton{{Text: "删除手动模型", Data: modelCallbackData("model:del", siteID, accountID, group.GroupKey, modelItem.ModelName)}})
	}
	buttons = append(buttons, []inlineButton{{Text: "返回模型列表", Data: fmt.Sprintf("model:list:%d:%d:%s", siteID, accountID, group.GroupKey)}, {Text: "返回路由组", Data: groupCallbackData(siteID, accountID, group.GroupKey)}})
	return response{Text: b.String(), Buttons: buttons}
}

func (r *Runner) fulfillPending(ctx context.Context, userID int64, action pendingAction, text string) response {
	switch action.Kind {
	case pendingCreateModelGroup:
		r.clearPending(userID)
		msg := r.createModelGroup(ctx, text)
		resp := r.modelGroupsMenu(ctx)
		resp.Text = msg + "\n\n" + resp.Text
		return resp
	case pendingRenameModelGroup:
		r.clearPending(userID)
		msg := r.renameModelGroup(ctx, action.GroupID, text)
		resp := r.modelGroupMenu(ctx, action.GroupID)
		resp.Text = msg + "\n\n" + resp.Text
		return resp
	case pendingAddModelGroupModels:
		r.clearPending(userID)
		lines := parseNonEmptyLines(text)
		if len(lines) == 0 {
			return response{Text: "请输入模型名，每行一个", Buttons: [][]inlineButton{{{Text: "返回分组", Data: fmt.Sprintf("mg:view:%d", action.GroupID)}}}}
		}
		msg := r.addModelGroupItems(ctx, action.GroupID, action.ChannelID, lines)
		resp := r.modelGroupMenu(ctx, action.GroupID)
		resp.Text = msg + "\n\n" + resp.Text
		return resp
	case pendingAddGroup:
		r.clearPending(userID)
		groupKey := model.NormalizeSiteGroupKey(text)
		if groupKey == "" {
			return response{Text: "路由组 key 不能为空", Buttons: [][]inlineButton{{{Text: "返回路由组列表", Data: fmt.Sprintf("groups:%d:%d", action.SiteID, action.AccountID)}}}}
		}
		msg := r.ensureGroup(ctx, action.SiteID, action.AccountID, groupKey)
		resp := r.groupMenu(ctx, action.SiteID, action.AccountID, groupKey)
		resp.Text = msg + "\n\n" + resp.Text
		return resp
	case pendingAddKey:
		r.clearPending(userID)
		fields := strings.Fields(text)
		if len(fields) == 0 {
			return response{Text: "请输入 <key> [name]", Buttons: [][]inlineButton{{{Text: "返回路由组", Data: groupCallbackData(action.SiteID, action.AccountID, action.GroupKey)}}}}
		}
		name := ""
		if len(fields) > 1 {
			name = strings.Join(fields[1:], " ")
		}
		msg := r.addKeyForGroup(ctx, action.SiteID, action.AccountID, action.GroupKey, fields[0], name)
		resp := r.groupMenu(ctx, action.SiteID, action.AccountID, action.GroupKey)
		resp.Text = msg + "\n\n" + resp.Text
		return resp
	case pendingAddModel:
		r.clearPending(userID)
		lines := parseNonEmptyLines(text)
		if len(lines) == 0 {
			return response{Text: "请输入 <model> [route]，支持多行", Buttons: [][]inlineButton{{{Text: "返回路由组", Data: groupCallbackData(action.SiteID, action.AccountID, action.GroupKey)}}}}
		}
		msg := r.addManualModels(ctx, action.SiteID, action.AccountID, action.GroupKey, lines, action.RouteType)
		resp := r.modelListMenu(ctx, action.SiteID, action.AccountID, action.GroupKey)
		resp.Text = msg + "\n\n" + resp.Text
		return resp
	case pendingTest:
		r.clearPending(userID)
		msg := r.runPendingTest(ctx, action, text)
		resp := r.groupMenu(ctx, action.SiteID, action.AccountID, action.GroupKey)
		resp.Text = msg + "\n\n" + resp.Text
		return resp
	case pendingAddSite:
		r.clearPending(userID)
		msg := r.addSite(ctx, strings.Fields(text))
		resp := r.sitesMenu(ctx)
		resp.Text = msg + "\n\n" + resp.Text
		return resp
	case pendingSiteName:
		r.clearPending(userID)
		msg := r.updateSiteName(ctx, action.SiteID, text)
		resp := r.siteEditMenu(ctx, action.SiteID)
		resp.Text = msg + "\n\n" + resp.Text
		return resp
	case pendingSiteBase:
		r.clearPending(userID)
		msg := r.updateSiteBaseURL(ctx, action.SiteID, text)
		resp := r.siteEditMenu(ctx, action.SiteID)
		resp.Text = msg + "\n\n" + resp.Text
		return resp
	default:
		r.clearPending(userID)
		return response{Text: "未知待处理操作", Buttons: mainMenuButtons()}
	}
}

func (r *Runner) getPending(userID int64) (pendingAction, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensurePendingLocked()
	action, ok := r.pending[userID]
	if !ok {
		return pendingAction{}, false
	}
	if !action.ExpiresAt.IsZero() && time.Now().After(action.ExpiresAt) {
		delete(r.pending, userID)
		return pendingAction{}, false
	}
	return action, ok
}

func (r *Runner) setPending(userID int64, action pendingAction) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensurePendingLocked()
	action.ExpiresAt = time.Now().Add(pendingActionTTL)
	r.pending[userID] = action
}

func (r *Runner) clearPending(userID int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensurePendingLocked()
	delete(r.pending, userID)
}

func (r *Runner) ensurePendingLocked() {
	if r.pending == nil {
		r.pending = make(map[int64]pendingAction)
	}
}

func (r *Runner) ensureCallbackAliasesLocked() {
	if r.callbackAliases == nil {
		r.callbackAliases = make(map[string]callbackAlias)
	}
}

func (r *Runner) createModelGroup(ctx context.Context, text string) string {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return "分组名不能为空"
	}
	name := strings.TrimSpace(fields[0])
	if name == "" {
		return "分组名不能为空"
	}
	mode := model.GroupModeRoundRobin
	if len(fields) >= 2 {
		parsed, ok := parseModelGroupMode(fields[1])
		if !ok {
			return "mode 无效，可选：round/random/failover/weighted"
		}
		mode = parsed
	}
	group := &model.Group{Name: name, Mode: mode, FirstTokenTimeOut: 0, SessionKeepTime: 0, MaxRetries: 3}
	if err := op.GroupCreate(group, ctx); err != nil {
		return "创建分组失败：" + err.Error()
	}
	return fmt.Sprintf("分组已创建：#%d %s mode=%s", group.ID, group.Name, groupModeLabel(group.Mode))
}

func (r *Runner) addModelGroupItems(ctx context.Context, groupID int, channelID int, lines []string) string {
	items := make([]model.GroupIDAndLLMName, 0, len(lines))
	names := make([]string, 0, len(lines))
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		name := strings.TrimSpace(fields[0])
		if name == "" {
			continue
		}
		items = append(items, model.GroupIDAndLLMName{ChannelID: channelID, ModelName: name})
		names = append(names, name)
	}
	if len(items) == 0 {
		return "没有可添加的模型"
	}
	if err := op.GroupItemBatchAdd(groupID, items, ctx); err != nil {
		return "添加分组模型失败：" + err.Error()
	}
	return fmt.Sprintf("已添加 %d 个分组模型：%s", len(names), strings.Join(names, ", "))
}

func (r *Runner) updateModelGroupMode(ctx context.Context, groupID int, mode model.GroupMode) string {
	if !validModelGroupMode(mode) {
		return "分组模式无效"
	}
	if _, err := op.GroupUpdate(&model.GroupUpdateRequest{ID: groupID, Mode: &mode}, ctx); err != nil {
		return "更新分组模式失败：" + err.Error()
	}
	return "分组模式已切换为：" + groupModeLabel(mode)
}

func (r *Runner) renameModelGroup(ctx context.Context, groupID int, name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "分组名不能为空"
	}
	if _, err := op.GroupUpdate(&model.GroupUpdateRequest{ID: groupID, Name: &trimmed}, ctx); err != nil {
		return "重命名分组失败：" + err.Error()
	}
	return "分组已重命名为：" + trimmed
}

func (r *Runner) runModelGroupHealth(ctx context.Context, groupID int) string {
	enabled, err := op.SettingGetBool(model.SettingKeyGroupHealthEnabled)
	if err != nil {
		return "读取探活设置失败：" + err.Error()
	}
	if !enabled {
		return "分组探活未启用，请先在设置中开启 group health"
	}
	if err := grouphealth.NewService(nil, nil).RunGroupHealth(ctx, groupID, model.GroupHealthProbeModeStandard); err != nil {
		return "分组探活失败：" + err.Error()
	}
	return "分组探活完成"
}

func (r *Runner) adjustModelGroupItem(ctx context.Context, groupID int, itemID int, priorityDelta int, weightDelta int) string {
	_, item, ok := r.findModelGroupItem(ctx, groupID, itemID)
	if !ok {
		return "分组模型不存在"
	}
	nextPriority := item.Priority + priorityDelta
	if nextPriority < 1 {
		nextPriority = 1
	}
	nextWeight := item.Weight + weightDelta
	if nextWeight < 1 {
		nextWeight = 1
	}
	if _, err := op.GroupUpdate(&model.GroupUpdateRequest{
		ID: groupID,
		ItemsToUpdate: []model.GroupItemUpdateRequest{{
			ID:       item.ID,
			Priority: nextPriority,
			Weight:   nextWeight,
		}},
	}, ctx); err != nil {
		return "更新分组模型失败：" + err.Error()
	}
	return fmt.Sprintf("分组模型已更新：priority=%d weight=%d", nextPriority, nextWeight)
}

func (r *Runner) deleteModelGroupItem(ctx context.Context, itemID int) string {
	if err := op.GroupItemDel(itemID, ctx); err != nil {
		return "删除分组模型失败：" + err.Error()
	}
	return "分组模型已删除"
}

func (r *Runner) findModelGroupItem(ctx context.Context, groupID int, itemID int) (*model.Group, model.GroupItem, bool) {
	group, err := op.GroupGet(groupID, ctx)
	if err != nil {
		return nil, model.GroupItem{}, false
	}
	for _, item := range group.Items {
		if item.ID == itemID {
			return group, item, true
		}
	}
	return group, model.GroupItem{}, false
}

func (r *Runner) deleteModelGroup(ctx context.Context, groupID int) string {
	if err := op.GroupDel(groupID, ctx); err != nil {
		return "删除分组失败：" + err.Error()
	}
	return "分组已删除"
}

func (r *Runner) ensureGroup(ctx context.Context, siteID int, accountID int, groupKey string) string {
	if groupKey == "" {
		return "路由组 key 不能为空"
	}
	if err := op.UpdateSiteGroupProjection(siteID, accountID, &model.SiteGroupProjectionUpdateRequest{
		GroupKey:           groupKey,
		ProjectionDisabled: true,
	}, ctx); err != nil {
		return "创建路由组失败：" + err.Error()
	}
	if err := op.UpdateSiteGroupProjection(siteID, accountID, &model.SiteGroupProjectionUpdateRequest{
		GroupKey:           groupKey,
		ProjectionDisabled: false,
	}, ctx); err != nil {
		return "创建路由组失败：" + err.Error()
	}
	if _, err := sitesvc.ProjectAccount(ctx, accountID); err != nil {
		return "路由组已创建，但重投影失败：" + err.Error()
	}
	return "路由组已创建"
}

func (r *Runner) deleteGroup(ctx context.Context, siteID int, accountID int, groupKey string) string {
	if err := op.SiteGroupDelete(siteID, accountID, groupKey, ctx); err != nil {
		return "删除路由组失败：" + err.Error()
	}
	if _, err := sitesvc.ProjectAccount(ctx, accountID); err != nil {
		return "路由组已删除，但重投影失败：" + err.Error()
	}
	return "路由组已删除"
}

func (r *Runner) toggleProjection(ctx context.Context, siteID int, accountID int, groupKey string) string {
	account, err := op.SiteChannelAccountGet(siteID, accountID, ctx)
	if err != nil {
		return "读取路由组失败：" + err.Error()
	}
	group, ok := findGroup(account.Groups, groupKey)
	if !ok {
		return "路由组不存在：" + groupKey
	}
	if err := op.UpdateSiteGroupProjection(siteID, accountID, &model.SiteGroupProjectionUpdateRequest{
		GroupKey:           group.GroupKey,
		ProjectionDisabled: !group.ProjectionDisabled,
	}, ctx); err != nil {
		return "更新投影失败：" + err.Error()
	}
	if _, err := sitesvc.ProjectAccount(ctx, accountID); err != nil {
		return "投影已更新，但重投影失败：" + err.Error()
	}
	return "路由组投影已切换"
}

func (r *Runner) addKeyForGroup(ctx context.Context, siteID int, accountID int, groupKey string, key string, name string) string {
	req := &model.SiteSourceKeyUpdateRequest{
		GroupKey: groupKey,
		KeysToAdd: []model.SiteSourceKeyAddRequest{{
			Enabled: true,
			Token:   key,
			Name:    name,
		}},
	}
	if err := op.UpdateSiteSourceKeys(siteID, accountID, req, ctx); err != nil {
		return "添加 Key 失败：" + err.Error()
	}
	if _, err := sitesvc.ProjectAccount(ctx, accountID); err != nil {
		return "Key 已添加，但重投影失败：" + err.Error()
	}
	return "Key 已添加"
}

func (r *Runner) addManualModel(ctx context.Context, siteID int, accountID int, groupKey string, modelName string, routeType model.SiteModelRouteType) string {
	if err := op.SiteManualModelsAdd(siteID, accountID, &model.SiteManualModelAddRequest{
		GroupKey: groupKey,
		Models: []model.SiteManualModelAddEntry{{
			ModelName: strings.TrimSpace(modelName),
			RouteType: routeType,
		}},
	}, ctx); err != nil {
		return "添加模型失败：" + err.Error()
	}
	if _, err := sitesvc.ProjectAccount(ctx, accountID); err != nil {
		return "模型已添加，但重投影失败：" + err.Error()
	}
	return "模型已添加"
}

func (r *Runner) addManualModels(ctx context.Context, siteID int, accountID int, groupKey string, lines []string, fixedRouteType model.SiteModelRouteType) string {
	items := make([]model.SiteManualModelAddEntry, 0, len(lines))
	names := make([]string, 0, len(lines))
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		routeType := fixedRouteType
		if routeType == "" {
			routeType = inferRouteType(fields)
		}
		items = append(items, model.SiteManualModelAddEntry{
			ModelName: strings.TrimSpace(fields[0]),
			RouteType: routeType,
		})
		names = append(names, strings.TrimSpace(fields[0]))
	}
	if len(items) == 0 {
		return "没有可添加的模型"
	}
	if err := op.SiteManualModelsAdd(siteID, accountID, &model.SiteManualModelAddRequest{
		GroupKey: groupKey,
		Models:   items,
	}, ctx); err != nil {
		return "添加模型失败：" + err.Error()
	}
	if _, err := sitesvc.ProjectAccount(ctx, accountID); err != nil {
		return "模型已添加，但重投影失败：" + err.Error()
	}
	if len(names) == 1 {
		return "模型已添加：" + names[0]
	}
	return fmt.Sprintf("已批量添加 %d 个模型：%s", len(names), strings.Join(names, ", "))
}

func (r *Runner) deleteManualModel(ctx context.Context, siteID int, accountID int, groupKey string, modelName string) string {
	if err := op.SiteManualModelDelete(siteID, accountID, &model.SiteManualModelDeleteRequest{
		GroupKey:  groupKey,
		ModelName: modelName,
	}, ctx); err != nil {
		return "删除模型失败：" + err.Error()
	}
	if _, err := sitesvc.ProjectAccount(ctx, accountID); err != nil {
		return "模型已删除，但重投影失败：" + err.Error()
	}
	return "手动模型已删除"
}

func (r *Runner) toggleModelDisabled(ctx context.Context, siteID int, accountID int, groupKey string, modelName string) string {
	account, err := op.SiteChannelAccountGet(siteID, accountID, ctx)
	if err != nil {
		return "读取模型失败：" + err.Error()
	}
	group, ok := findGroup(account.Groups, groupKey)
	if !ok {
		return "路由组不存在：" + groupKey
	}
	item, ok := findModel(group.Models, modelName)
	if !ok {
		return "模型不存在：" + modelName
	}
	if err := op.SiteModelDisabledUpdate(accountID, group.GroupKey, item.ModelName, !item.Disabled, ctx); err != nil {
		return "更新模型开关失败：" + err.Error()
	}
	if _, err := sitesvc.ProjectAccount(ctx, accountID); err != nil {
		return "模型已更新，但重投影失败：" + err.Error()
	}
	return "模型启用状态已切换"
}

func (r *Runner) toggleModel1M(ctx context.Context, siteID int, accountID int, groupKey string, modelName string) string {
	account, err := op.SiteChannelAccountGet(siteID, accountID, ctx)
	if err != nil {
		return "读取模型失败：" + err.Error()
	}
	group, ok := findGroup(account.Groups, groupKey)
	if !ok {
		return "路由组不存在：" + groupKey
	}
	item, ok := findModel(group.Models, modelName)
	if !ok {
		return "模型不存在：" + modelName
	}
	if err := op.SiteModelContext1MUpdate(accountID, group.GroupKey, item.ModelName, !item.Context1M, ctx); err != nil {
		return "更新 1M 失败：" + err.Error()
	}
	if _, err := sitesvc.ProjectAccount(ctx, accountID); err != nil {
		return "模型已更新，但重投影失败：" + err.Error()
	}
	return "模型 1M 已切换"
}

func (r *Runner) runPendingTest(ctx context.Context, action pendingAction, text string) string {
	fields := strings.Fields(text)
	if len(fields) < 2 {
		return "测试格式错误，请发送：<token_id> <model> [client] [message]"
	}
	tokenID, err := strconv.Atoi(fields[0])
	if err != nil {
		return "token_id 必须是数字"
	}
	modelName := fields[1]
	client := sitesync.TestConversationClientDefault
	messageStart := 2
	if len(fields) >= 3 {
		switch strings.ToLower(fields[2]) {
		case "default":
			client = sitesync.TestConversationClientDefault
			messageStart = 3
		case "codex":
			client = sitesync.TestConversationClientCodex
			messageStart = 3
		case "claude":
			client = sitesync.TestConversationClientClaude
			messageStart = 3
		}
	}
	greeting := "hi"
	if len(fields) > messageStart {
		greeting = strings.Join(fields[messageStart:], " ")
	}
	mode := sitesync.TestConversationModeOpenAIChat
	if client == sitesync.TestConversationClientCodex {
		mode = sitesync.TestConversationModeOpenAIResponse
	} else if client == sitesync.TestConversationClientClaude || strings.HasPrefix(strings.ToLower(modelName), "claude") {
		mode = sitesync.TestConversationModeAnthropic
	}
	result, err := sitesync.TestConversation(ctx, sitesync.TestConversationRequest{
		AccountID: action.AccountID,
		TokenID:   tokenID,
		Model:     modelName,
		Mode:      mode,
		Greeting:  greeting,
		Client:    client,
	})
	if err != nil {
		return "测试失败：" + err.Error()
	}
	return fmt.Sprintf("测试成功\n模型：%s\n模式：%s\n耗时：%dms\n回复：\n%s", result.Model, result.Mode, result.DurationMS, result.Reply)
}

func inferRouteType(fields []string) model.SiteModelRouteType {
	if len(fields) >= 2 {
		switch model.SiteModelRouteType(strings.TrimSpace(fields[1])) {
		case model.SiteModelRouteTypeOpenAIChat,
			model.SiteModelRouteTypeOpenAIResponse,
			model.SiteModelRouteTypeAnthropic,
			model.SiteModelRouteTypeGemini,
			model.SiteModelRouteTypeVolcengine,
			model.SiteModelRouteTypeOpenAIEmbedding:
			return model.SiteModelRouteType(strings.TrimSpace(fields[1]))
		}
	}
	return model.InferSiteModelRouteType(fields[0])
}

func (r *Runner) addSite(ctx context.Context, args []string) string {
	if len(args) < 3 {
		return "用法：/addsite <name> <base_url> <api_key> [platform] [account_name]"
	}
	platform := model.SitePlatformAPI
	if len(args) >= 4 {
		platform = model.SitePlatform(args[3])
	}
	accountName := "default"
	if len(args) >= 5 {
		accountName = args[4]
	}
	site := model.Site{
		Name:       args[0],
		Platform:   platform,
		BaseURL:    args[1],
		Enabled:    true,
		EnabledSet: true,
		ProxyMode:  model.ProxyUsageModeDirect,
	}
	if err := op.SiteCreate(&site, ctx); err != nil {
		return "添加站点失败：" + err.Error()
	}
	account := model.SiteAccount{
		SiteID:                     site.ID,
		Name:                       accountName,
		CredentialType:             model.SiteCredentialTypeAPIKey,
		APIKey:                     args[2],
		ProxyMode:                  model.ProxyUsageModeInherit,
		Enabled:                    true,
		EnabledSet:                 true,
		AutoSync:                   true,
		AutoSyncSet:                true,
		AutoCheckin:                false,
		AutoCheckinSet:             true,
		CheckinIntervalHours:       24,
		CheckinRandomWindowMinutes: 120,
	}
	if err := op.SiteAccountCreate(&account, ctx); err != nil {
		return fmt.Sprintf("站点已创建 #%d，但账号创建失败：%v", site.ID, err)
	}
	return fmt.Sprintf("已添加站点 #%d %s，账号 #%d %s", site.ID, site.Name, account.ID, account.Name)
}

func (r *Runner) updateSiteName(ctx context.Context, siteID int, name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "站点名称不能为空"
	}
	site, err := op.SiteUpdate(&model.SiteUpdateRequest{
		ID:   siteID,
		Name: &trimmed,
	}, ctx)
	if err != nil {
		return "更新站点名称失败：" + err.Error()
	}
	return fmt.Sprintf("站点名称已更新为：%s", site.Name)
}

func (r *Runner) updateSiteBaseURL(ctx context.Context, siteID int, baseURL string) string {
	trimmed := strings.TrimSpace(baseURL)
	if trimmed == "" {
		return "BaseURL 不能为空"
	}
	site, err := op.SiteUpdate(&model.SiteUpdateRequest{
		ID:      siteID,
		BaseURL: &trimmed,
	}, ctx)
	if err != nil {
		return "更新 BaseURL 失败：" + err.Error()
	}
	return fmt.Sprintf("BaseURL 已更新为：%s", site.BaseURL)
}

func (r *Runner) toggleSiteEnabled(ctx context.Context, siteID int) string {
	site, err := op.SiteGet(siteID, ctx)
	if err != nil {
		return "读取站点失败：" + err.Error()
	}
	next := !site.Enabled
	updated, err := op.SiteUpdate(&model.SiteUpdateRequest{
		ID:      siteID,
		Enabled: &next,
	}, ctx)
	if err != nil {
		return "更新站点启用状态失败：" + err.Error()
	}
	return fmt.Sprintf("站点启用状态已更新：%t", updated.Enabled)
}

func (r *Runner) toggleSiteCodex(ctx context.Context, siteID int) string {
	site, err := op.SiteGet(siteID, ctx)
	if err != nil {
		return "读取站点失败：" + err.Error()
	}
	next := !site.CodexMode
	updated, err := op.SiteUpdate(&model.SiteUpdateRequest{
		ID:        siteID,
		CodexMode: &next,
	}, ctx)
	if err != nil {
		return "更新 Codex 模式失败：" + err.Error()
	}
	return fmt.Sprintf("Codex 模式已更新：%t", updated.CodexMode)
}

func (r *Runner) toggleSiteClaude(ctx context.Context, siteID int) string {
	site, err := op.SiteGet(siteID, ctx)
	if err != nil {
		return "读取站点失败：" + err.Error()
	}
	next := !site.ClaudeMode
	updated, err := op.SiteUpdate(&model.SiteUpdateRequest{
		ID:         siteID,
		ClaudeMode: &next,
	}, ctx)
	if err != nil {
		return "更新 Claude 模式失败：" + err.Error()
	}
	return fmt.Sprintf("Claude 模式已更新：%t", updated.ClaudeMode)
}

func (r *Runner) addKey(ctx context.Context, args []string) string {
	if len(args) < 4 {
		return "用法：/addkey <site_id> <account_id> <group_key> <key> [name]"
	}
	siteID, err := strconv.Atoi(args[0])
	if err != nil {
		return "site_id 必须是数字"
	}
	accountID, err := strconv.Atoi(args[1])
	if err != nil {
		return "account_id 必须是数字"
	}
	name := ""
	if len(args) >= 5 {
		name = strings.Join(args[4:], " ")
	}
	return r.addKeyForGroup(ctx, siteID, accountID, args[2], args[3], name)
}

func (r *Runner) testConversation(ctx context.Context, args []string) string {
	if len(args) < 3 {
		return "用法：/test <account_id> <token_id> <model> [default|codex|claude] [message]"
	}
	accountID, err := strconv.Atoi(args[0])
	if err != nil {
		return "account_id 必须是数字"
	}
	tokenID, err := strconv.Atoi(args[1])
	if err != nil {
		return "token_id 必须是数字"
	}
	modelName := args[2]
	client := sitesync.TestConversationClientDefault
	messageStart := 3
	if len(args) >= 4 {
		switch strings.ToLower(args[3]) {
		case "default":
			client = sitesync.TestConversationClientDefault
			messageStart = 4
		case "codex":
			client = sitesync.TestConversationClientCodex
			messageStart = 4
		case "claude":
			client = sitesync.TestConversationClientClaude
			messageStart = 4
		}
	}
	greeting := "hi"
	if len(args) > messageStart {
		greeting = strings.Join(args[messageStart:], " ")
	}
	mode := sitesync.TestConversationModeOpenAIChat
	if client == sitesync.TestConversationClientCodex {
		mode = sitesync.TestConversationModeOpenAIResponse
	} else if client == sitesync.TestConversationClientClaude || strings.HasPrefix(strings.ToLower(modelName), "claude") {
		mode = sitesync.TestConversationModeAnthropic
	}
	result, err := sitesync.TestConversation(ctx, sitesync.TestConversationRequest{
		AccountID: accountID,
		TokenID:   tokenID,
		Model:     modelName,
		Mode:      mode,
		Greeting:  greeting,
		Client:    client,
	})
	if err != nil {
		return "测试失败：" + err.Error()
	}
	return fmt.Sprintf("测试成功\n模型：%s\n模式：%s\n耗时：%dms\n回复：\n%s", result.Model, result.Mode, result.DurationMS, result.Reply)
}

func (r *Runner) updateProjection(ctx context.Context, args []string) string {
	if len(args) < 4 {
		return "用法：/projection <site_id> <account_id> <group_key> on|off"
	}
	siteID, err := strconv.Atoi(args[0])
	if err != nil {
		return "site_id 必须是数字"
	}
	accountID, err := strconv.Atoi(args[1])
	if err != nil {
		return "account_id 必须是数字"
	}
	action := strings.ToLower(args[3])
	if action != "on" && action != "off" {
		return "最后一个参数必须是 on 或 off"
	}
	if err := op.UpdateSiteGroupProjection(siteID, accountID, &model.SiteGroupProjectionUpdateRequest{
		GroupKey:           args[2],
		ProjectionDisabled: action == "off",
	}, ctx); err != nil {
		return "更新投影失败：" + err.Error()
	}
	if _, err := sitesvc.ProjectAccount(ctx, accountID); err != nil {
		return "投影状态已更新，但重投影失败：" + err.Error()
	}
	return "分组投影已更新"
}

func (r *Runner) updateModel(ctx context.Context, args []string) string {
	if len(args) < 5 {
		return "用法：/model <site_id> <account_id> <group_key> <model> enable|disable|1m-on|1m-off"
	}
	accountID, err := strconv.Atoi(args[1])
	if err != nil {
		return "account_id 必须是数字"
	}
	action := strings.ToLower(args[4])
	switch action {
	case "enable", "disable":
		if err := op.SiteModelDisabledUpdate(accountID, args[2], args[3], action == "disable", ctx); err != nil {
			return "更新模型开关失败：" + err.Error()
		}
	case "1m-on", "1m-off":
		if err := op.SiteModelContext1MUpdate(accountID, args[2], args[3], action == "1m-on", ctx); err != nil {
			return "更新 1M 开关失败：" + err.Error()
		}
	default:
		return "操作必须是 enable、disable、1m-on 或 1m-off"
	}
	if _, err := sitesvc.ProjectAccount(ctx, accountID); err != nil {
		return "模型已更新，但重投影失败：" + err.Error()
	}
	return "模型设置已更新"
}

func (r *Runner) syncAccount(ctx context.Context, accountID int) string {
	result, err := sitesvc.SyncAccount(ctx, accountID)
	if err != nil {
		return "同步失败：" + err.Error()
	}
	return fmt.Sprintf("同步完成：%s\n%s", result.Status, result.Message)
}

func (r *Runner) checkinAccount(ctx context.Context, accountID int) string {
	result, err := sitesvc.CheckinAccount(ctx, accountID)
	if err != nil {
		return "签到失败：" + err.Error()
	}
	return fmt.Sprintf("签到完成：%s\n%s", result.Status, result.Message)
}

func (r *Runner) listLogs(ctx context.Context, keyword string) string {
	filter := op.RelayLogListFilter{
		Status:         op.RelayLogStatusError,
		Limit:          5,
		IncludeContent: false,
		Pagination:     "cursor",
	}
	keyword = strings.TrimSpace(keyword)
	if keyword != "" {
		filter.Keyword = keyword
	}
	result, err := op.RelayLogListWithFilter(ctx, filter)
	if err != nil {
		return "读取日志失败：" + err.Error()
	}
	if len(result.Logs) == 0 {
		return "没有找到错误日志"
	}
	sort.Slice(result.Logs, func(i, j int) bool { return result.Logs[i].Time > result.Logs[j].Time })
	var b strings.Builder
	b.WriteString("近期错误日志")
	for _, item := range result.Logs {
		fmt.Fprintf(&b, "\n\n#%d %s\nchannel=%s model=%s key=%s\nerror=%s", item.ID, time.Unix(item.Time, 0).Format("2006-01-02 15:04:05"), item.ChannelName, item.RequestModelName, item.RequestAPIKeyName, firstNonEmpty(item.Error, "unknown"))
	}
	return b.String()
}

func findGroup(groups []model.SiteChannelGroup, groupKey string) (model.SiteChannelGroup, bool) {
	target := model.NormalizeSiteGroupKey(groupKey)
	for _, group := range groups {
		if model.NormalizeSiteGroupKey(group.GroupKey) == target {
			return group, true
		}
	}
	return model.SiteChannelGroup{}, false
}

func findModel(models []model.SiteChannelModel, modelName string) (model.SiteChannelModel, bool) {
	target := strings.TrimSpace(modelName)
	for _, item := range models {
		if strings.TrimSpace(item.ModelName) == target {
			return item, true
		}
	}
	return model.SiteChannelModel{}, false
}

func parsePair(data string, prefix string) (int, int, error) {
	parts := strings.Split(data, ":")
	prefixParts := strings.Count(prefix, ":") + 1
	if len(parts) != prefixParts+2 {
		return 0, 0, fmt.Errorf("invalid pair callback")
	}
	siteID, err := strconv.Atoi(parts[prefixParts])
	if err != nil {
		return 0, 0, err
	}
	accountID, err := strconv.Atoi(parts[prefixParts+1])
	if err != nil {
		return 0, 0, err
	}
	return siteID, accountID, nil
}

func parseTriple(data string, prefix string) (int, int, int, error) {
	parts := strings.Split(data, ":")
	prefixParts := strings.Count(prefix, ":") + 1
	if len(parts) != prefixParts+3 {
		return 0, 0, 0, fmt.Errorf("invalid triple callback")
	}
	first, err := strconv.Atoi(parts[prefixParts])
	if err != nil {
		return 0, 0, 0, err
	}
	second, err := strconv.Atoi(parts[prefixParts+1])
	if err != nil {
		return 0, 0, 0, err
	}
	third, err := strconv.Atoi(parts[prefixParts+2])
	if err != nil {
		return 0, 0, 0, err
	}
	return first, second, third, nil
}

func parseGroupTarget(data string, prefix string) (int, int, string, error) {
	parts := strings.Split(data, ":")
	prefixParts := strings.Count(prefix, ":") + 1
	if len(parts) < prefixParts+3 {
		return 0, 0, "", fmt.Errorf("invalid group callback")
	}
	siteID, err := strconv.Atoi(parts[prefixParts])
	if err != nil {
		return 0, 0, "", err
	}
	accountID, err := strconv.Atoi(parts[prefixParts+1])
	if err != nil {
		return 0, 0, "", err
	}
	groupKey := strings.Join(parts[prefixParts+2:], ":")
	return siteID, accountID, groupKey, nil
}

func parseModelTarget(data string, prefix string) (int, int, string, string, error) {
	parts := strings.Split(data, ":")
	prefixParts := strings.Count(prefix, ":") + 1
	if len(parts) < prefixParts+4 {
		return 0, 0, "", "", fmt.Errorf("invalid model callback")
	}
	siteID, err := strconv.Atoi(parts[prefixParts])
	if err != nil {
		return 0, 0, "", "", err
	}
	accountID, err := strconv.Atoi(parts[prefixParts+1])
	if err != nil {
		return 0, 0, "", "", err
	}
	modelName := parts[len(parts)-1]
	groupKey := strings.Join(parts[prefixParts+2:len(parts)-1], ":")
	if strings.TrimSpace(groupKey) == "" || strings.TrimSpace(modelName) == "" {
		return 0, 0, "", "", fmt.Errorf("invalid model callback")
	}
	return siteID, accountID, groupKey, modelName, nil
}

func parseRouteTarget(data string, prefix string) (int, int, string, model.SiteModelRouteType, error) {
	parts := strings.Split(data, ":")
	prefixParts := strings.Count(prefix, ":") + 1
	if len(parts) < prefixParts+4 {
		return 0, 0, "", "", fmt.Errorf("invalid route callback")
	}
	siteID, err := strconv.Atoi(parts[prefixParts])
	if err != nil {
		return 0, 0, "", "", err
	}
	accountID, err := strconv.Atoi(parts[prefixParts+1])
	if err != nil {
		return 0, 0, "", "", err
	}
	routeType := model.SiteModelRouteType(parts[len(parts)-1])
	groupKey := strings.Join(parts[prefixParts+2:len(parts)-1], ":")
	if strings.TrimSpace(groupKey) == "" || routeType == "" {
		return 0, 0, "", "", fmt.Errorf("invalid route callback")
	}
	return siteID, accountID, groupKey, routeType, nil
}

func parseModelGroupModelTarget(data string, prefix string) (int, int, string, error) {
	parts := strings.Split(data, ":")
	prefixParts := strings.Count(prefix, ":") + 1
	if len(parts) < prefixParts+3 {
		return 0, 0, "", fmt.Errorf("invalid model group model callback")
	}
	groupID, err := strconv.Atoi(parts[prefixParts])
	if err != nil {
		return 0, 0, "", err
	}
	channelID, err := strconv.Atoi(parts[prefixParts+1])
	if err != nil {
		return 0, 0, "", err
	}
	modelName := strings.Join(parts[prefixParts+2:], ":")
	if strings.TrimSpace(modelName) == "" {
		return 0, 0, "", fmt.Errorf("invalid model group model callback")
	}
	return groupID, channelID, modelName, nil
}

func parseModelGroupModeTarget(data string, prefix string) (int, model.GroupMode, error) {
	parts := strings.Split(data, ":")
	prefixParts := strings.Count(prefix, ":") + 1
	if len(parts) != prefixParts+2 {
		return 0, 0, fmt.Errorf("invalid model group mode callback")
	}
	groupID, err := strconv.Atoi(parts[prefixParts])
	if err != nil {
		return 0, 0, err
	}
	modeValue, err := strconv.Atoi(parts[prefixParts+1])
	if err != nil {
		return 0, 0, err
	}
	mode := model.GroupMode(modeValue)
	if !validModelGroupMode(mode) {
		return 0, 0, fmt.Errorf("invalid model group mode")
	}
	return groupID, mode, nil
}

func validModelGroupMode(mode model.GroupMode) bool {
	switch mode {
	case model.GroupModeRoundRobin, model.GroupModeRandom, model.GroupModeFailover, model.GroupModeWeighted:
		return true
	default:
		return false
	}
}

func parseModelGroupMode(value string) (model.GroupMode, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "round", "round_robin", "rr", "轮询":
		return model.GroupModeRoundRobin, true
	case "random", "rand", "随机":
		return model.GroupModeRandom, true
	case "failover", "fo", "故障转移":
		return model.GroupModeFailover, true
	case "weighted", "weight", "加权":
		return model.GroupModeWeighted, true
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0, false
	}
	mode := model.GroupMode(parsed)
	return mode, validModelGroupMode(mode)
}

func groupModeLabel(mode model.GroupMode) string {
	switch mode {
	case model.GroupModeRoundRobin:
		return "轮询"
	case model.GroupModeRandom:
		return "随机"
	case model.GroupModeFailover:
		return "故障转移"
	case model.GroupModeWeighted:
		return "加权"
	default:
		return fmt.Sprintf("unknown(%d)", mode)
	}
}

func splitModelCSV(values ...string) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0)
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			name := strings.TrimSpace(part)
			if name == "" {
				continue
			}
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			out = append(out, name)
		}
	}
	return out
}

func limitStrings(values []string, limit int) []string {
	if len(values) <= limit {
		return values
	}
	return values[:limit]
}

func mainMenuButtons() [][]inlineButton {
	return [][]inlineButton{
		{{Text: "站点管理", Data: "site_mgmt"}, {Text: "渠道管理", Data: "group_mgmt"}},
		{{Text: "分组管理", Data: "model_groups"}, {Text: "运维", Data: "ops"}},
		{{Text: "监控", Data: "monitor"}},
	}
}

func groupCallbackData(siteID int, accountID int, groupKey string) string {
	return fmt.Sprintf("group:%d:%d:%s", siteID, accountID, groupKey)
}

func modelCallbackData(prefix string, siteID int, accountID int, groupKey string, modelName string) string {
	return fmt.Sprintf("%s:%d:%d:%s:%s", prefix, siteID, accountID, groupKey, modelName)
}

func trimForButton(text string, limit int) string {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) <= limit {
		return string(runes)
	}
	if limit <= 3 {
		return string(runes[:limit])
	}
	return string(runes[:limit-3]) + "..."
}

func projectionToggleLabel(disabled bool) string {
	if disabled {
		return "开启投影"
	}
	return "关闭投影"
}

func modelToggleLabel(disabled bool) string {
	if disabled {
		return "启用模型"
	}
	return "禁用模型"
}

func model1MLabel(enabled bool) string {
	if enabled {
		return "关闭 1M"
	}
	return "开启 1M"
}

func siteEnabledLabel(enabled bool) string {
	if enabled {
		return "停用站点"
	}
	return "启用站点"
}

func siteCodexLabel(enabled bool) string {
	if enabled {
		return "关闭 Codex"
	}
	return "开启 Codex"
}

func siteClaudeLabel(enabled bool) string {
	if enabled {
		return "关闭 Claude"
	}
	return "开启 Claude"
}

func onOff(v bool) string {
	if v {
		return "on"
	}
	return "off"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func parseNonEmptyLines(text string) []string {
	raw := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	lines := make([]string, 0, len(raw))
	for _, line := range raw {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			lines = append(lines, trimmed)
		}
	}
	return lines
}

func sleep(ctx context.Context, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

type telegramUpdate struct {
	UpdateID      int64                  `json:"update_id"`
	Message       *telegramMessage       `json:"message,omitempty"`
	CallbackQuery *telegramCallbackQuery `json:"callback_query,omitempty"`
}

type telegramUser struct {
	ID int64 `json:"id"`
}

type telegramChat struct {
	ID int64 `json:"id"`
}

type telegramMessage struct {
	MessageID int64        `json:"message_id"`
	From      telegramUser `json:"from"`
	Chat      telegramChat `json:"chat"`
	Text      string       `json:"text"`
}

type telegramCallbackQuery struct {
	ID      string           `json:"id"`
	From    telegramUser     `json:"from"`
	Message *telegramMessage `json:"message,omitempty"`
	Data    string           `json:"data"`
}

func sendMessage(ctx context.Context, cfg config, client *http.Client, chatID int64, resp response) error {
	body := map[string]any{
		"chat_id": chatID,
		"text":    truncateMessage(resp.Text),
	}
	if markup := buildInlineKeyboard(resp.Buttons); markup != nil {
		body["reply_markup"] = markup
	}
	return telegramRequest(ctx, cfg, client, "sendMessage", body, nil)
}

func editMessage(ctx context.Context, cfg config, client *http.Client, chatID int64, messageID int64, resp response) error {
	body := map[string]any{
		"chat_id":    chatID,
		"message_id": messageID,
		"text":       truncateMessage(resp.Text),
	}
	if markup := buildInlineKeyboard(resp.Buttons); markup != nil {
		body["reply_markup"] = markup
	}
	return telegramRequest(ctx, cfg, client, "editMessageText", body, nil)
}

func answerCallback(ctx context.Context, cfg config, client *http.Client, callbackID string, text string) error {
	body := map[string]any{
		"callback_query_id": callbackID,
	}
	if text != "" {
		body["text"] = text
	}
	return telegramRequest(ctx, cfg, client, "answerCallbackQuery", body, nil)
}

func (r *Runner) prepareResponse(resp response) response {
	resp.Text = strings.TrimSpace(resp.Text)
	if resp.Text == "" {
		resp.Text = "操作完成"
	}
	if len(resp.Buttons) == 0 {
		return resp
	}

	now := time.Now()
	prepared := make([][]inlineButton, 0, len(resp.Buttons))
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureCallbackAliasesLocked()
	r.pruneCallbackAliasesLocked(now)
	for _, row := range resp.Buttons {
		if len(row) == 0 {
			continue
		}
		preparedRow := make([]inlineButton, 0, len(row))
		for _, item := range row {
			data := strings.TrimSpace(item.Data)
			if data == "" {
				preparedRow = append(preparedRow, item)
				continue
			}
			item.Data = data
			if len(item.Data) > maxCallbackDataBytes {
				item.Data = r.registerCallbackAliasLocked(item.Data, now)
			}
			preparedRow = append(preparedRow, item)
		}
		if len(preparedRow) > 0 {
			prepared = append(prepared, preparedRow)
		}
	}
	resp.Buttons = prepared
	return resp
}

func (r *Runner) registerCallbackAliasLocked(data string, now time.Time) string {
	r.callbackAliasSeq++
	token := callbackAliasPrefix + strconv.FormatUint(r.callbackAliasSeq, 36)
	r.callbackAliases[token] = callbackAlias{
		Data:      data,
		ExpiresAt: now.Add(callbackAliasTTL),
	}
	return token
}

func (r *Runner) resolveCallbackData(data string) (string, bool) {
	data = strings.TrimSpace(data)
	if !strings.HasPrefix(data, callbackAliasPrefix) {
		return data, true
	}
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureCallbackAliasesLocked()
	alias, ok := r.callbackAliases[data]
	if !ok {
		return "", false
	}
	if now.After(alias.ExpiresAt) {
		delete(r.callbackAliases, data)
		return "", false
	}
	return alias.Data, true
}

func (r *Runner) pruneCallbackAliasesLocked(now time.Time) {
	if len(r.callbackAliases) == 0 {
		return
	}
	for token, alias := range r.callbackAliases {
		if now.After(alias.ExpiresAt) {
			delete(r.callbackAliases, token)
		}
	}
	for len(r.callbackAliases) > maxCallbackAliasCount {
		for token := range r.callbackAliases {
			delete(r.callbackAliases, token)
			break
		}
	}
}

func buildInlineKeyboard(rows [][]inlineButton) map[string]any {
	if len(rows) == 0 {
		return nil
	}
	keyboard := make([][]map[string]string, 0, len(rows))
	for _, row := range rows {
		if len(row) == 0 {
			continue
		}
		items := make([]map[string]string, 0, len(row))
		for _, item := range row {
			data := strings.TrimSpace(item.Data)
			if strings.TrimSpace(item.Text) == "" || data == "" || len(data) > maxCallbackDataBytes {
				continue
			}
			items = append(items, map[string]string{
				"text":          item.Text,
				"callback_data": data,
			})
		}
		if len(items) > 0 {
			keyboard = append(keyboard, items)
		}
	}
	if len(keyboard) == 0 {
		return nil
	}
	return map[string]any{"inline_keyboard": keyboard}
}

func telegramRequest(ctx context.Context, cfg config, client *http.Client, method string, body any, result any) error {
	apiURL, err := buildAPIURL(cfg.APIBaseURL, cfg.Token, method)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("telegram http %d: %s", resp.StatusCode, truncateForError(string(respBody)))
	}
	var wrapper struct {
		OK          bool            `json:"ok"`
		Description string          `json:"description"`
		Result      json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(respBody, &wrapper); err != nil {
		return fmt.Errorf("telegram returned invalid json: %w", err)
	}
	if !wrapper.OK {
		if wrapper.Description == "" {
			wrapper.Description = "unknown error"
		}
		return errors.New(wrapper.Description)
	}
	if result != nil && len(wrapper.Result) > 0 {
		if err := json.Unmarshal(wrapper.Result, result); err != nil {
			return err
		}
	}
	return nil
}

func truncateMessage(text string) string {
	text = strings.TrimSpace(text)
	if len([]rune(text)) <= maxMessageLen {
		return text
	}
	runes := []rune(text)
	return string(runes[:maxMessageLen]) + "\n..."
}

func truncateForError(text string) string {
	text = strings.TrimSpace(text)
	if len(text) <= 500 {
		return text
	}
	return text[:500] + "..."
}
