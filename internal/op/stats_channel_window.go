package op

import (
	"context"
	"time"

	"github.com/U188/octopus/internal/db"
	"github.com/U188/octopus/internal/model"
)

// StatsChannel24h is a rolling 24-hour view over persisted relay logs.
// Token totals include estimated values; cache ratios only use reported usage.
type StatsChannel24h struct {
	ChannelID int   `json:"channel_id"`
	From      int64 `json:"from"`
	To        int64 `json:"to"`
	AsOf      int64 `json:"as_of"`

	RelayLogRetentionEnabled bool   `json:"relay_log_retention_enabled"`
	RelayLogDroppedTotal     uint64 `json:"relay_log_dropped_total"`

	RequestTotal   int64 `json:"request_total"`
	RequestSuccess int64 `json:"request_success"`
	RequestFailed  int64 `json:"request_failed"`

	InputToken       int64   `json:"input_token"`
	OutputToken      int64   `json:"output_token"`
	BillInputToken   int64   `json:"bill_input_token"`
	CacheReadToken   int64   `json:"cache_read_token"`
	CacheWriteToken  int64   `json:"cache_write_token"`
	TotalCost        float64 `json:"total_cost"`
	AverageLatencyMs float64 `json:"average_latency_ms"`
	AverageFTUTMs    float64 `json:"average_ftut_ms"`

	SuccessRate          *float64 `json:"success_rate"`
	CacheReadRatio       *float64 `json:"cache_read_ratio"`
	CacheReadRequestRate *float64 `json:"cache_read_request_rate"`
	UsageCoverage        *float64 `json:"usage_coverage"`
}

type statsChannel24hRow struct {
	RequestTotal         int64
	RequestSuccess       int64
	RequestFailed        int64
	InputToken           int64
	OutputToken          int64
	BillInputToken       int64
	CacheReadToken       int64
	CacheWriteToken      int64
	ReportedRequests     int64
	ReportedInputToken   int64
	CacheKnownRequests   int64
	CacheKnownInputToken int64
	CacheReadRequests    int64
	TotalCost            float64
	AverageLatencyMs     float64
	AverageFTUTMs        float64
}

// StatsChannel24hGet returns a rolling 24-hour aggregate without loading log
// request/response bodies. The relay-log writer is asynchronous, so the result
// can lag the most recent request by approximately one flush interval.
func StatsChannel24hGet(ctx context.Context, channelID int, now time.Time) (StatsChannel24h, error) {
	to := now.Unix()
	from := to - int64((24*time.Hour)/time.Second)

	var row statsChannel24hRow
	err := db.GetDB().WithContext(ctx).Model(&model.RelayLog{}).
		Select(`
			COUNT(*) AS request_total,
			COALESCE(SUM(CASE WHEN success = ? THEN 1 ELSE 0 END), 0) AS request_success,
			COALESCE(SUM(CASE WHEN success = ? THEN 1 ELSE 0 END), 0) AS request_failed,
			COALESCE(SUM(input_tokens), 0) AS input_token,
			COALESCE(SUM(output_tokens), 0) AS output_token,
			COALESCE(SUM(bill_input_tokens), 0) AS bill_input_token,
			COALESCE(SUM(CASE WHEN input_token_source = ? THEN cache_read_tokens ELSE 0 END), 0) AS cache_read_token,
			COALESCE(SUM(CASE WHEN input_token_source = ? THEN cache_write_tokens ELSE 0 END), 0) AS cache_write_token,
			COALESCE(SUM(CASE WHEN input_token_source = ? THEN 1 ELSE 0 END), 0) AS reported_requests,
			COALESCE(SUM(CASE WHEN input_token_source = ? THEN input_tokens ELSE 0 END), 0) AS reported_input_token,
			COALESCE(SUM(CASE WHEN input_token_source = ? AND cache_read_tokens IS NOT NULL THEN 1 ELSE 0 END), 0) AS cache_known_requests,
			COALESCE(SUM(CASE WHEN input_token_source = ? AND cache_read_tokens IS NOT NULL THEN input_tokens ELSE 0 END), 0) AS cache_known_input_token,
			COALESCE(SUM(CASE WHEN input_token_source = ? AND cache_read_tokens IS NOT NULL AND cache_read_tokens > 0 THEN 1 ELSE 0 END), 0) AS cache_read_requests,
			COALESCE(SUM(cost), 0) AS total_cost,
			COALESCE(AVG(use_time), 0) AS average_latency_ms,
			COALESCE(AVG(NULLIF(ftut, 0)), 0) AS average_ftut_ms`,
			true, false,
			model.TokenCountSourceReported,
			model.TokenCountSourceReported,
			model.TokenCountSourceReported,
			model.TokenCountSourceReported,
			model.TokenCountSourceReported,
			model.TokenCountSourceReported,
			model.TokenCountSourceReported,
		).
		Where("channel_id = ? AND time >= ? AND time < ?", channelID, from, to).
		Scan(&row).Error
	if err != nil {
		return StatsChannel24h{}, err
	}

	result := StatsChannel24h{
		ChannelID:                channelID,
		From:                     from,
		To:                       to,
		AsOf:                     to,
		RelayLogRetentionEnabled: relayLogRetentionEnabled(),
		RelayLogDroppedTotal:     RelayLogDroppedTotal(),
		RequestTotal:             row.RequestTotal,
		RequestSuccess:           row.RequestSuccess,
		RequestFailed:            row.RequestFailed,
		InputToken:               row.InputToken,
		OutputToken:              row.OutputToken,
		BillInputToken:           row.BillInputToken,
		CacheReadToken:           row.CacheReadToken,
		CacheWriteToken:          row.CacheWriteToken,
		TotalCost:                row.TotalCost,
		AverageLatencyMs:         row.AverageLatencyMs,
		AverageFTUTMs:            row.AverageFTUTMs,
		SuccessRate:              ratio(row.RequestSuccess, row.RequestTotal),
		CacheReadRatio:           ratio(row.CacheReadToken, row.CacheKnownInputToken),
		CacheReadRequestRate:     ratio(row.CacheReadRequests, row.CacheKnownRequests),
		UsageCoverage:            ratio(row.ReportedRequests, row.RequestTotal),
	}
	return result, nil
}

func relayLogRetentionEnabled() bool {
	enabled, err := SettingGetBool(model.SettingKeyRelayLogKeepEnabled)
	return err == nil && enabled
}

func ratio(numerator, denominator int64) *float64 {
	if denominator <= 0 {
		return nil
	}
	value := float64(numerator) / float64(denominator)
	return &value
}
