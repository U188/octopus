package model

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

type ProxyUsageMode string

type ProxyConfigurationType string

const (
	ProxyConfigurationTypeSingle       ProxyConfigurationType = "single"
	ProxyConfigurationTypeSubscription ProxyConfigurationType = "subscription"
)

type ProxySubscriptionSyncStatus string

const (
	ProxySubscriptionSyncIdle    ProxySubscriptionSyncStatus = "idle"
	ProxySubscriptionSyncSuccess ProxySubscriptionSyncStatus = "success"
	ProxySubscriptionSyncFailed  ProxySubscriptionSyncStatus = "failed"
)

const (
	DefaultProxySubscriptionRefreshMinutes = 30
	MinProxySubscriptionRefreshMinutes     = 5
	MaxProxySubscriptionRefreshMinutes     = 7 * 24 * 60
)

const (
	ProxyUsageModeDirect  ProxyUsageMode = "direct"
	ProxyUsageModeSystem  ProxyUsageMode = "system"
	ProxyUsageModePool    ProxyUsageMode = "pool"
	ProxyUsageModeInherit ProxyUsageMode = "inherit"
)

type ProxyConfiguration struct {
	ID                     int                         `json:"id" gorm:"primaryKey"`
	Name                   string                      `json:"name" gorm:"unique;not null"`
	URL                    string                      `json:"url" gorm:"unique;not null"`
	Type                   ProxyConfigurationType      `json:"type" gorm:"size:16;not null;default:single;index"`
	Enabled                bool                        `json:"enabled" gorm:"default:true"`
	Remark                 string                      `json:"remark"`
	RefreshIntervalMinutes int                         `json:"refresh_interval_minutes" gorm:"not null;default:30"`
	LastSyncAt             *time.Time                  `json:"last_sync_at,omitempty"`
	LastSyncStatus         ProxySubscriptionSyncStatus `json:"last_sync_status" gorm:"size:16;not null;default:idle"`
	LastSyncMessage        string                      `json:"last_sync_message"`
	CreatedAt              time.Time                   `json:"created_at"`
	UpdatedAt              time.Time                   `json:"updated_at"`
	ReferenceCount         int                         `json:"reference_count" gorm:"-"`
	NodeCount              int                         `json:"node_count" gorm:"-"`
	HealthyNodeCount       int                         `json:"healthy_node_count" gorm:"-"`
	AvailableNodeCount     int                         `json:"available_node_count" gorm:"-"`
	QuarantinedNodeCount   int                         `json:"quarantined_node_count" gorm:"-"`
}

type ProxyConfigurationUpdateRequest struct {
	ID                     int     `json:"id" binding:"required"`
	Name                   *string `json:"name,omitempty"`
	URL                    *string `json:"url,omitempty"`
	Enabled                *bool   `json:"enabled,omitempty"`
	Remark                 *string `json:"remark,omitempty"`
	RefreshIntervalMinutes *int    `json:"refresh_interval_minutes,omitempty"`
}

type ProxySubscriptionNode struct {
	ID                   int                   `json:"id" gorm:"primaryKey"`
	ProxyConfigurationID int                   `json:"proxy_configuration_id" gorm:"not null;uniqueIndex:idx_proxy_subscription_node_url;index"`
	URL                  string                `json:"url" gorm:"not null;uniqueIndex:idx_proxy_subscription_node_url"`
	Active               bool                  `json:"active" gorm:"not null;index"`
	HealthStatus         ProxyTestHealthStatus `json:"health_status" gorm:"size:16;not null;default:failed;index"`
	LatencyMS            int64                 `json:"latency_ms"`
	LastCheckedAt        *time.Time            `json:"last_checked_at,omitempty"`
	LastError            string                `json:"last_error"`
	RuntimeFailureCount  int                   `json:"runtime_failure_count"`
	QuarantinedUntil     *time.Time            `json:"quarantined_until,omitempty" gorm:"index"`
	LastRuntimeFailureAt *time.Time            `json:"last_runtime_failure_at,omitempty"`
	LastRuntimeError     string                `json:"last_runtime_error"`
	CreatedAt            time.Time             `json:"created_at"`
	UpdatedAt            time.Time             `json:"updated_at"`
}

type ProxySubscriptionSyncResult struct {
	ProxyConfigurationID int       `json:"proxy_configuration_id"`
	FetchedCount         int       `json:"fetched_count"`
	HealthyCount         int       `json:"healthy_count"`
	DegradedCount        int       `json:"degraded_count"`
	FailedCount          int       `json:"failed_count"`
	SyncedAt             time.Time `json:"synced_at"`
}

type ProxyTestRequest struct {
	ProxyConfigID  *int   `json:"proxy_config_id,omitempty"`
	ProxyURL       string `json:"proxy_url,omitempty"`
	UseSystemProxy bool   `json:"use_system_proxy,omitempty"`
	URL            string `json:"url,omitempty"`
}

type ProxyTestHealthStatus string

const (
	ProxyTestHealthHealthy  ProxyTestHealthStatus = "healthy"
	ProxyTestHealthDegraded ProxyTestHealthStatus = "degraded"
	ProxyTestHealthFailed   ProxyTestHealthStatus = "failed"
)

type ProxyTestAttemptResult struct {
	Attempt    int    `json:"attempt"`
	Success    bool   `json:"success"`
	StatusCode int    `json:"status_code"`
	DurationMS int64  `json:"duration_ms"`
	Message    string `json:"message"`
}

type ProxyTestResult struct {
	Success           bool                     `json:"success"`
	HealthStatus      ProxyTestHealthStatus    `json:"health_status"`
	StatusCode        int                      `json:"status_code"`
	DurationMS        int64                    `json:"duration_ms"`
	AverageDurationMS int64                    `json:"average_duration_ms"`
	AttemptCount      int                      `json:"attempt_count"`
	SuccessCount      int                      `json:"success_count"`
	Attempts          []ProxyTestAttemptResult `json:"attempts"`
	Message           string                   `json:"message"`
}

type ProxyConfigurationReferenceType string

const (
	ProxyConfigurationReferenceTypeSite           ProxyConfigurationReferenceType = "site"
	ProxyConfigurationReferenceTypeSiteAccount    ProxyConfigurationReferenceType = "site_account"
	ProxyConfigurationReferenceTypeChannel        ProxyConfigurationReferenceType = "channel"
	ProxyConfigurationReferenceTypeManagedChannel ProxyConfigurationReferenceType = "managed_channel"
)

type ProxyConfigurationReference struct {
	Type            ProxyConfigurationReferenceType `json:"type"`
	SiteID          int                             `json:"site_id,omitempty"`
	SiteName        string                          `json:"site_name,omitempty"`
	SiteArchived    bool                            `json:"site_archived,omitempty"`
	SiteAccountID   int                             `json:"site_account_id,omitempty"`
	SiteAccountName string                          `json:"site_account_name,omitempty"`
	ChannelID       int                             `json:"channel_id,omitempty"`
	ChannelName     string                          `json:"channel_name,omitempty"`
	Managed         bool                            `json:"managed,omitempty"`
	ManagedSource   *ManagedChannelSource           `json:"managed_source,omitempty"`
}

func (m ProxyUsageMode) Validate(allowInherit bool) error {
	switch m {
	case ProxyUsageModeDirect, ProxyUsageModeSystem, ProxyUsageModePool:
		return nil
	case ProxyUsageModeInherit:
		if allowInherit {
			return nil
		}
	}
	return fmt.Errorf("unsupported proxy mode: %s", m)
}

func NormalizeProxyURL(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", fmt.Errorf("proxy url is required")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("invalid proxy url: %w", err)
	}
	parsed.Scheme = strings.ToLower(strings.TrimSpace(parsed.Scheme))
	parsed.Host = strings.ToLower(strings.TrimSpace(parsed.Host))
	switch parsed.Scheme {
	case "http", "https", "socks", "socks5":
	default:
		return "", fmt.Errorf("unsupported proxy scheme: %s", parsed.Scheme)
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("proxy url must have a host")
	}
	return parsed.String(), nil
}

func (p *ProxyConfiguration) Normalize() error {
	if p == nil {
		return fmt.Errorf("proxy configuration is nil")
	}
	p.Name = strings.TrimSpace(p.Name)
	p.Remark = strings.TrimSpace(p.Remark)
	if p.Name == "" {
		return fmt.Errorf("proxy name is required")
	}
	if p.Type == "" {
		p.Type = ProxyConfigurationTypeSingle
	}
	switch p.Type {
	case ProxyConfigurationTypeSingle:
		normalizedURL, err := NormalizeProxyURL(p.URL)
		if err != nil {
			return err
		}
		p.URL = normalizedURL
	case ProxyConfigurationTypeSubscription:
		normalizedURL, err := normalizeProxySubscriptionURL(p.URL)
		if err != nil {
			return err
		}
		p.URL = normalizedURL
		if p.RefreshIntervalMinutes == 0 {
			p.RefreshIntervalMinutes = DefaultProxySubscriptionRefreshMinutes
		}
		if p.RefreshIntervalMinutes < MinProxySubscriptionRefreshMinutes || p.RefreshIntervalMinutes > MaxProxySubscriptionRefreshMinutes {
			return fmt.Errorf("subscription refresh interval must be between %d and %d minutes", MinProxySubscriptionRefreshMinutes, MaxProxySubscriptionRefreshMinutes)
		}
	default:
		return fmt.Errorf("unsupported proxy configuration type: %s", p.Type)
	}
	if p.LastSyncStatus == "" {
		p.LastSyncStatus = ProxySubscriptionSyncIdle
	}
	return nil
}

func (p *ProxyConfiguration) Validate() error {
	return p.Normalize()
}

func normalizeProxySubscriptionURL(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", fmt.Errorf("subscription url is required")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("invalid subscription url: %w", err)
	}
	parsed.Scheme = strings.ToLower(strings.TrimSpace(parsed.Scheme))
	parsed.Host = strings.ToLower(strings.TrimSpace(parsed.Host))
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("subscription url must use http or https")
	}
	if parsed.Hostname() == "" {
		return "", fmt.Errorf("subscription url must have a host")
	}
	if parsed.User != nil {
		return "", fmt.Errorf("subscription url must not contain embedded credentials")
	}
	return parsed.String(), nil
}
