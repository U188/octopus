package sitesync

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/U188/octopus/internal/db"
	"github.com/U188/octopus/internal/model"
	"gorm.io/gorm"
)

func CreateAccountToken(ctx context.Context, accountID int, req model.SiteChannelKeyCreateRequest) (*model.SiteSyncResult, error) {
	siteRecord, account, err := loadSiteAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if siteRecord == nil || account == nil {
		return nil, fmt.Errorf("site account not found")
	}

	groupKey := model.NormalizeSiteGroupKey(req.GroupKey)
	name := strings.TrimSpace(req.Name)
	var createdToken *model.SiteToken

	switch siteRecord.Platform {
	case model.SitePlatformAnyRouter:
		createdToken, err = createAnyRouterToken(ctx, siteRecord, account, groupKey, name)
		if err != nil {
			return nil, err
		}
	case model.SitePlatformNewAPI, model.SitePlatformOneAPI, model.SitePlatformOneHub, model.SitePlatformDoneHub:
		createdToken, err = createManagementPlatformToken(ctx, siteRecord, account, groupKey, name)
		if err != nil {
			return nil, err
		}
	case model.SitePlatformSub2API:
		createdToken, err = createSub2APIToken(ctx, siteRecord, account, groupKey, name)
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("site platform %s does not support quick key creation", siteRecord.Platform)
	}
	if createdToken != nil {
		if err := persistCreatedAccountToken(ctx, account.ID, *createdToken); err != nil {
			return nil, err
		}
	}

	return SyncAccount(ctx, accountID)
}

func createManagementPlatformToken(ctx context.Context, siteRecord *model.Site, account *model.SiteAccount, groupKey string, name string) (*model.SiteToken, error) {
	if account == nil {
		return nil, fmt.Errorf("site account is nil")
	}
	if account.CredentialType == model.SiteCredentialTypeAPIKey {
		return nil, fmt.Errorf("API key credential account does not support quick site key creation")
	}

	accessToken, err := resolveManagedAccessToken(ctx, siteRecord, account)
	if err != nil {
		return nil, err
	}

	requestName := defaultSiteTokenCreateName(account, groupKey, name)
	payload, err := requestJSONWithManagedAccessToken(
		ctx,
		siteRecord,
		http.MethodPost,
		buildSiteURL(siteRecord.BaseURL, "/api/token/"),
		buildManagedTokenCreatePayload(account, groupKey, requestName),
		accessToken,
		account,
	)
	if err != nil {
		return nil, err
	}
	if !siteTokenCreateSucceeded(payload) {
		return nil, fmt.Errorf("%s", firstNonEmptyString(extractSiteResponseMessage(payload), "site token creation failed"))
	}
	return createdTokenFromPayload(payload, groupKey, requestName), nil
}

func createAnyRouterToken(ctx context.Context, siteRecord *model.Site, account *model.SiteAccount, groupKey string, name string) (*model.SiteToken, error) {
	if account == nil {
		return nil, fmt.Errorf("site account is nil")
	}
	if account.CredentialType == model.SiteCredentialTypeAPIKey {
		return nil, fmt.Errorf("API key credential account does not support quick site key creation")
	}

	accessToken, err := resolveAnyRouterManagedAccessToken(ctx, siteRecord, account)
	if err != nil {
		return nil, err
	}

	requestName := defaultSiteTokenCreateName(account, groupKey, name)
	payloadBody := buildManagedTokenCreatePayload(account, groupKey, requestName)
	requestURL := buildSiteURL(siteRecord.BaseURL, "/api/token/")

	userID, _ := anyRouterDiscoverUserID(ctx, siteRecord, account, accessToken)
	payload, _, err := anyRouterRequestJSONWithCookies(
		ctx,
		siteRecord,
		http.MethodPost,
		requestURL,
		payloadBody,
		anyRouterAuthHeaders(accessToken, userID),
		account,
	)
	if err == nil && siteTokenCreateSucceeded(payload) {
		return createdTokenFromPayload(payload, groupKey, requestName), nil
	}

	tryUserIDs := []int{userID}
	if alternateUserID, probeErr := anyRouterProbeAlternateUserIDByCookie(ctx, siteRecord, account, accessToken, userID); probeErr == nil && alternateUserID > 0 {
		tryUserIDs = append(tryUserIDs, alternateUserID)
	}
	if userID <= 0 {
		if probedUserID, probeErr := anyRouterProbeUserIDByCookie(ctx, siteRecord, account, accessToken); probeErr == nil && probedUserID > 0 {
			tryUserIDs = append(tryUserIDs, probedUserID)
		}
	}
	tryUserIDs = slicesCompactInts(tryUserIDs)

	for _, candidateUserID := range tryUserIDs {
		for _, cookie := range anyRouterBuildCookieCandidates(accessToken) {
			headers := map[string]string{"Cookie": cookie}
			anyRouterAddUserIDHeaders(headers, candidateUserID)
			payload, _, requestErr := anyRouterRequestJSONWithCookies(
				ctx,
				siteRecord,
				http.MethodPost,
				requestURL,
				payloadBody,
				headers,
				account,
			)
			if requestErr != nil {
				if err == nil {
					err = requestErr
				}
				continue
			}
			if siteTokenCreateSucceeded(payload) {
				return createdTokenFromPayload(payload, groupKey, requestName), nil
			}
			if message := strings.TrimSpace(extractSiteResponseMessage(payload)); message != "" {
				err = fmt.Errorf("%s", message)
			}
		}
	}

	if err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("site token creation failed")
}

func createSub2APIToken(ctx context.Context, siteRecord *model.Site, account *model.SiteAccount, groupKey string, name string) (*model.SiteToken, error) {
	if account == nil {
		return nil, fmt.Errorf("site account is nil")
	}
	if account.CredentialType == model.SiteCredentialTypeAPIKey {
		return nil, fmt.Errorf("API key credential account does not support quick site key creation")
	}

	accessToken := strings.TrimSpace(account.AccessToken)
	accessToken, err := ensureFreshSub2APIAccessToken(ctx, siteRecord, account, false)
	if err != nil {
		return nil, err
	}

	requestName := defaultSiteTokenCreateName(account, groupKey, name)
	requestBody := buildSub2APITokenCreatePayload(account, groupKey, requestName)
	headers := map[string]string{"Authorization": ensureBearer(accessToken)}
	endpoints := []string{"/api/v1/keys", "/api/v1/api-keys"}
	var firstErr error

	for _, endpoint := range endpoints {
		payload, err := requestJSON(
			ctx,
			siteRecord,
			http.MethodPost,
			buildSiteURL(siteRecord.BaseURL, endpoint),
			requestBody,
			headers,
			account,
		)
		if err != nil {
			if shouldRetrySub2APIAfterRefresh(err, account) {
				refreshedToken, refreshErr := ensureFreshSub2APIAccessToken(ctx, siteRecord, account, true)
				if refreshErr == nil && stripBearerPrefix(refreshedToken) != stripBearerPrefix(accessToken) {
					headers = map[string]string{"Authorization": ensureBearer(refreshedToken)}
					payload, err = requestJSON(
						ctx,
						siteRecord,
						http.MethodPost,
						buildSiteURL(siteRecord.BaseURL, endpoint),
						requestBody,
						headers,
						account,
					)
					if err == nil {
						data, envelopeErr := unwrapSub2APIData(payload, endpoint)
						if envelopeErr == nil && siteTokenCreateSucceededFromAny(data) {
							return createdTokenFromAny(data, groupKey, requestName), nil
						}
						if envelopeErr != nil && firstErr == nil {
							firstErr = envelopeErr
						}
					}
				}
			}
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if data, envelopeErr := unwrapSub2APIData(payload, endpoint); envelopeErr == nil {
			if siteTokenCreateSucceededFromAny(data) {
				return createdTokenFromAny(data, groupKey, requestName), nil
			}
		} else {
			return nil, envelopeErr
		}
		if siteTokenCreateSucceeded(payload) {
			return createdTokenFromPayload(payload, groupKey, requestName), nil
		}
		return nil, fmt.Errorf("%s", firstNonEmptyString(extractSiteResponseMessage(payload), "site token creation failed"))
	}

	if firstErr != nil {
		return nil, firstErr
	}
	return nil, fmt.Errorf("site token creation failed")
}

func buildManagedTokenCreatePayload(account *model.SiteAccount, groupKey string, name string) map[string]any {
	name = defaultSiteTokenCreateName(account, groupKey, name)
	return map[string]any{
		"name":                 name,
		"unlimited_quota":      true,
		"expired_time":         -1,
		"remain_quota":         0,
		"allow_ips":            "",
		"model_limits_enabled": false,
		"model_limits":         "",
		"group":                model.NormalizeSiteGroupKey(groupKey),
	}
}

func buildSub2APITokenCreatePayload(account *model.SiteAccount, groupKey string, name string) map[string]any {
	name = defaultSiteTokenCreateName(account, groupKey, name)
	payload := map[string]any{
		"name": name,
	}
	groupKey = model.NormalizeSiteGroupKey(groupKey)
	if groupID, err := strconv.Atoi(groupKey); err == nil && groupID > 0 {
		payload["group_id"] = groupID
	}
	return payload
}

func createdTokenFromPayload(payload map[string]any, groupKey string, name string) *model.SiteToken {
	if payload == nil {
		return nil
	}
	return createdTokenFromAny(payload, groupKey, name)
}

func createdTokenFromAny(value any, groupKey string, name string) *model.SiteToken {
	tokenValue := extractCreatedTokenValue(value)
	tokenValue = strings.TrimSpace(tokenValue)
	if tokenValue == "" || model.IsMaskedSiteTokenValue(tokenValue) {
		return nil
	}
	groupKey = model.NormalizeSiteGroupKey(groupKey)
	name = strings.TrimSpace(name)
	if name == "" {
		name = defaultSiteTokenCreateName(nil, groupKey, "")
	}
	return &model.SiteToken{
		Purpose:     model.SiteCredentialPurposeChat,
		Name:        name,
		Token:       tokenValue,
		GroupKey:    groupKey,
		GroupName:   model.NormalizeSiteGroupName(groupKey, groupKey),
		Enabled:     true,
		ValueStatus: model.SiteTokenValueStatusReady,
		Source:      "sync",
	}
}

func extractCreatedTokenValue(value any) string {
	switch typed := value.(type) {
	case map[string]any:
		for _, key := range []string{"key", "token", "api_key", "apiKey", "value", "secret", "secret_key", "secretKey"} {
			if token := strings.TrimSpace(jsonString(typed[key])); token != "" {
				return token
			}
		}
		for _, key := range []string{"data", "result", "token", "key"} {
			if nested := extractCreatedTokenValue(typed[key]); strings.TrimSpace(nested) != "" {
				return nested
			}
		}
	case []any:
		for _, item := range typed {
			if token := extractCreatedTokenValue(item); strings.TrimSpace(token) != "" {
				return token
			}
		}
	}
	return ""
}

func persistCreatedAccountToken(ctx context.Context, accountID int, token model.SiteToken) error {
	token.SiteAccountID = accountID
	token.Purpose = model.SiteCredentialPurposeChat
	token.GroupKey = model.NormalizeSiteGroupKey(token.GroupKey)
	token.GroupName = model.NormalizeSiteGroupName(token.GroupKey, token.GroupName)
	token.Token = strings.TrimSpace(token.Token)
	token.ValueStatus = model.NormalizeSiteTokenValueStatus(model.SiteTokenValueStatusReady, token.Token)
	if token.Token == "" || token.ValueStatus != model.SiteTokenValueStatusReady {
		return nil
	}
	if strings.TrimSpace(token.Name) == "" {
		token.Name = defaultSiteTokenCreateName(nil, token.GroupKey, "")
	}
	token.Enabled = true
	if strings.TrimSpace(token.Source) == "" {
		token.Source = "sync"
	}
	var existing model.SiteToken
	err := db.GetDB().WithContext(ctx).
		Where("site_account_id = ? AND purpose = ? AND group_key = ? AND name = ?", accountID, model.SiteCredentialPurposeChat, token.GroupKey, token.Name).
		First(&existing).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.GetDB().WithContext(ctx).Create(&token).Error
	}
	updates := map[string]any{
		"value":        token.Token,
		"value_status": token.ValueStatus,
		"enabled":      token.Enabled,
		"source":       token.Source,
		"group_name":   token.GroupName,
	}
	return db.GetDB().WithContext(ctx).Model(&model.SiteToken{}).Where("id = ?", existing.ID).Updates(updates).Error
}

func defaultSiteTokenCreateName(account *model.SiteAccount, groupKey string, name string) string {
	if trimmed := strings.TrimSpace(name); trimmed != "" {
		return trimmed
	}

	groupPart := strings.TrimSpace(groupKey)
	groupPart = strings.NewReplacer("/", "-", "\\", "-", " ", "-", "\t", "-", "\n", "-").Replace(groupPart)
	groupPart = strings.Trim(groupPart, "-")
	if groupPart == "" {
		groupPart = model.SiteDefaultGroupKey
	}
	return fmt.Sprintf("octopus-%s-%d", groupPart, time.Now().Unix())
}

func siteTokenCreateSucceeded(payload map[string]any) bool {
	if payload == nil {
		return false
	}
	return siteTokenCreateSucceededFromAny(payload)
}

func siteTokenCreateSucceededFromAny(value any) bool {
	payload, ok := value.(map[string]any)
	if !ok {
		succeeded, ok := value.(bool)
		return ok && succeeded
	}
	if raw, ok := payload["success"]; ok {
		switch typed := raw.(type) {
		case bool:
			return typed
		case float64:
			return typed != 0
		case int:
			return typed != 0
		case string:
			switch strings.ToLower(strings.TrimSpace(typed)) {
			case "1", "true", "ok", "success":
				return true
			case "0", "false", "fail", "failed", "error":
				return false
			}
		}
		return false
	}
	return true
}

func slicesCompactInts(values []int) []int {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[int]struct{}, len(values))
	result := make([]int, 0, len(values))
	for _, value := range values {
		if value < 0 {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
