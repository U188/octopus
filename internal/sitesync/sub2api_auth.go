package sitesync

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/U188/octopus/internal/db"
	"github.com/U188/octopus/internal/model"
	"gorm.io/gorm"
)

const sub2APIAccessTokenRefreshLead = 5 * time.Minute

type sub2APIRefreshedCredentials struct {
	AccessToken    string
	RefreshToken   string
	TokenExpiresAt int64
}

func ensureFreshSub2APIAccessToken(ctx context.Context, siteRecord *model.Site, account *model.SiteAccount, forceRefresh bool) (string, error) {
	if account == nil {
		return "", fmt.Errorf("site account is nil")
	}

	accessToken := stripBearerPrefix(account.AccessToken)
	if accessToken == "" {
		return "", newAccessTokenRequiredError()
	}
	if !forceRefresh && !shouldProactivelyRefreshSub2API(account) {
		return accessToken, nil
	}
	if strings.TrimSpace(account.RefreshToken) == "" {
		return accessToken, nil
	}

	refreshed, err := refreshSub2APIManagedSession(ctx, siteRecord, account, accessToken)
	if err != nil {
		if forceRefresh {
			return "", err
		}
		return accessToken, nil
	}
	return refreshed, nil
}

func shouldProactivelyRefreshSub2API(account *model.SiteAccount) bool {
	if account == nil {
		return false
	}
	if strings.TrimSpace(account.RefreshToken) == "" {
		return false
	}
	if account.TokenExpiresAt <= 0 {
		return false
	}
	return time.Until(time.UnixMilli(account.TokenExpiresAt)) <= sub2APIAccessTokenRefreshLead
}

func shouldRetrySub2APIAfterRefresh(err error, account *model.SiteAccount) bool {
	if err == nil || account == nil || strings.TrimSpace(account.RefreshToken) == "" {
		return false
	}
	text := strings.ToLower(strings.TrimSpace(err.Error()))
	if text == "" {
		return false
	}
	return strings.Contains(text, "http 401") ||
		strings.Contains(text, "http 403") ||
		strings.Contains(text, "unauthorized") ||
		strings.Contains(text, "forbidden") ||
		strings.Contains(text, "expired") ||
		strings.Contains(text, "invalid token") ||
		strings.Contains(text, "access token")
}

func refreshSub2APIManagedSession(ctx context.Context, siteRecord *model.Site, account *model.SiteAccount, currentAccessToken string) (string, error) {
	if siteRecord == nil || account == nil {
		return "", fmt.Errorf("site or account is nil")
	}
	refreshToken := strings.TrimSpace(account.RefreshToken)
	if refreshToken == "" {
		return "", fmt.Errorf("sub2api managed refresh token missing")
	}

	headers := map[string]string{
		"Content-Type": "application/json",
	}
	if currentAccessToken = stripBearerPrefix(currentAccessToken); currentAccessToken != "" {
		headers["Authorization"] = ensureBearer(currentAccessToken)
	}

	payload, err := requestJSON(
		ctx,
		siteRecord,
		"POST",
		buildSiteURL(siteRecord.BaseURL, "/api/v1/auth/refresh"),
		map[string]any{"refresh_token": refreshToken},
		headers,
		account,
	)
	if err != nil {
		return "", fmt.Errorf("sub2api token refresh request failed: %w", err)
	}

	refreshed, ok := parseSub2APIRefreshPayload(payload)
	if !ok {
		return "", fmt.Errorf("sub2api token refresh failed")
	}

	account.AccessToken = refreshed.AccessToken
	account.RefreshToken = refreshed.RefreshToken
	account.TokenExpiresAt = refreshed.TokenExpiresAt

	if account.ID > 0 {
		if err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := upsertSessionCredentialTx(tx, account.ID, refreshed.AccessToken, refreshed.TokenExpiresAt); err != nil {
				return err
			}
			return upsertRefreshCredentialTx(tx, account.ID, refreshed.RefreshToken)
		}); err != nil {
			return "", fmt.Errorf("failed to persist sub2api refreshed session: %w", err)
		}
	}

	return refreshed.AccessToken, nil
}

func loginSub2API(ctx context.Context, siteRecord *model.Site, account *model.SiteAccount) (sub2APIRefreshedCredentials, error) {
	if siteRecord == nil || account == nil {
		return sub2APIRefreshedCredentials{}, fmt.Errorf("site or account is nil")
	}
	email := strings.TrimSpace(account.Username)
	password := strings.TrimSpace(account.Password)
	if email == "" || password == "" {
		return sub2APIRefreshedCredentials{}, fmt.Errorf("sub2api email and password are required")
	}

	payload, err := requestJSON(
		ctx,
		siteRecord,
		"POST",
		buildSiteURL(siteRecord.BaseURL, "/api/v1/auth/login"),
		map[string]any{"email": email, "password": password},
		map[string]string{"Content-Type": "application/json"},
		account,
	)
	if err != nil {
		return sub2APIRefreshedCredentials{}, fmt.Errorf("sub2api login request failed: %w", err)
	}
	data, err := unwrapSub2APIData(payload, "/api/v1/auth/login")
	if err != nil {
		return sub2APIRefreshedCredentials{}, err
	}
	if response, ok := data.(map[string]any); ok && jsonBool(response["requires_2fa"]) {
		return sub2APIRefreshedCredentials{}, fmt.Errorf("sub2api login requires 2FA; use access token credential instead")
	}

	refreshed, ok := parseSub2APIAuthData(data)
	if !ok {
		return sub2APIRefreshedCredentials{}, fmt.Errorf("sub2api login response missing access token")
	}

	account.AccessToken = refreshed.AccessToken
	account.RefreshToken = refreshed.RefreshToken
	account.TokenExpiresAt = refreshed.TokenExpiresAt
	return refreshed, nil
}

func upsertRefreshCredentialTx(tx *gorm.DB, accountID int, refreshToken string) error {
	refreshToken = strings.TrimSpace(refreshToken)
	if refreshToken == "" {
		return nil
	}
	var existing model.SiteCredential
	err := tx.Where("site_account_id = ? AND purpose = ?", accountID, model.SiteCredentialPurposeRefresh).First(&existing).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	row := model.SiteCredential{
		SiteAccountID: accountID,
		Purpose:       model.SiteCredentialPurposeRefresh,
		Name:          "refresh",
		Token:         refreshToken,
		ValueStatus:   model.SiteTokenValueStatusReady,
		Enabled:       true,
		Source:        "sync",
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return tx.Create(&row).Error
	}
	return tx.Model(&model.SiteCredential{}).Where("id = ?", existing.ID).Updates(map[string]any{
		"value":        row.Token,
		"value_status": row.ValueStatus,
		"enabled":      row.Enabled,
		"source":       row.Source,
	}).Error
}

func parseSub2APIRefreshPayload(payload map[string]any) (sub2APIRefreshedCredentials, bool) {
	if payload == nil {
		return sub2APIRefreshedCredentials{}, false
	}

	if rawCode, ok := payload["code"]; ok {
		code := anyToInt64(rawCode)
		if code != 0 {
			return sub2APIRefreshedCredentials{}, false
		}
	}

	refreshed, ok := parseSub2APIAuthData(payload["data"])
	if !ok || refreshed.RefreshToken == "" || refreshed.TokenExpiresAt <= 0 {
		return sub2APIRefreshedCredentials{}, false
	}
	return refreshed, true
}

func parseSub2APIAuthData(value any) (sub2APIRefreshedCredentials, bool) {
	data, ok := value.(map[string]any)
	if !ok {
		return sub2APIRefreshedCredentials{}, false
	}
	accessToken := stripBearerPrefix(jsonString(data["access_token"]))
	refreshToken := strings.TrimSpace(jsonString(data["refresh_token"]))
	expiresInSeconds := anyToInt64(data["expires_in"])
	if accessToken == "" {
		return sub2APIRefreshedCredentials{}, false
	}
	tokenExpiresAt := int64(0)
	if expiresInSeconds > 0 {
		tokenExpiresAt = time.Now().Add(time.Duration(expiresInSeconds) * time.Second).UnixMilli()
	}

	return sub2APIRefreshedCredentials{
		AccessToken:    accessToken,
		RefreshToken:   refreshToken,
		TokenExpiresAt: tokenExpiresAt,
	}, true
}

func anyToInt64(value any) int64 {
	switch typed := value.(type) {
	case int:
		return int64(typed)
	case int64:
		return typed
	case float64:
		return int64(typed)
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return 0
		}
		var parsed int64
		if _, err := fmt.Sscanf(trimmed, "%d", &parsed); err == nil {
			return parsed
		}
	}
	return 0
}
