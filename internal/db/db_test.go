package db

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/U188/octopus/internal/model"
	"github.com/U188/octopus/internal/utils/snowflake"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// sqlCaptureLogger 记下 GORM 发出的所有 SQL，用于断言 schema 变更路径上没有
// 触发 glebarez 的 recreateTable（即 CREATE TABLE relay_logs__temp 那条特征）。
type sqlCaptureLogger struct {
	mu         sync.Mutex
	statements []string
}

func (l *sqlCaptureLogger) LogMode(logger.LogLevel) logger.Interface      { return l }
func (l *sqlCaptureLogger) Info(context.Context, string, ...interface{})  {}
func (l *sqlCaptureLogger) Warn(context.Context, string, ...interface{})  {}
func (l *sqlCaptureLogger) Error(context.Context, string, ...interface{}) {}
func (l *sqlCaptureLogger) Trace(_ context.Context, _ time.Time, fc func() (string, int64), _ error) {
	sql, _ := fc()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.statements = append(l.statements, sql)
}
func (l *sqlCaptureLogger) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]string, len(l.statements))
	copy(out, l.statements)
	return out
}
func (l *sqlCaptureLogger) reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.statements = l.statements[:0]
}

// TestEnsureRelayLogColumnsSQLite_AddsMissingWithoutRecreate 模拟旧版本
// 留下的 relay_logs。ensureRelayLogColumnsSQLite 必须：
//  1. 把缺失的 success、request_ip 等列加上来；
//  2. 全程不发出任何 recreateTable 特征 SQL。
func TestEnsureRelayLogColumnsSQLite_AddsMissingWithoutRecreate(t *testing.T) {
	capture := &sqlCaptureLogger{}
	gormDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: capture})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	// v0.8.25 列集合：除了 success，其它全有。
	createSQL := `CREATE TABLE relay_logs (
		id integer,
		time integer,
		request_model_name text,
		request_api_key_name text,
		channel_id integer,
		channel_name text,
		actual_model_name text,
		input_tokens integer,
		transport_input_tokens integer,
		bill_input_tokens integer,
		cache_read_tokens integer,
		cache_write_tokens integer,
		output_tokens integer,
		ftut integer,
		use_time integer,
		cost real,
		request_content text,
		response_content text,
		error text,
		attempts text,
		total_attempts integer,
		used_ws numeric DEFAULT false,
		ws_mode text,
		ws_exec_mode text,
		ws_recovery text,
		PRIMARY KEY (id)
	)`
	if err := gormDB.Exec(createSQL).Error; err != nil {
		t.Fatalf("create legacy table: %v", err)
	}
	if err := gormDB.Exec("INSERT INTO relay_logs (id, time, request_model_name) VALUES (1, 1, 'legacy-model')").Error; err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}

	capture.reset()
	if err := ensureRelayLogColumnsSQLite(gormDB); err != nil {
		t.Fatalf("ensureRelayLogColumnsSQLite failed: %v", err)
	}

	// 新增列被安全补齐。
	for _, column := range []string{"success", "request_ip", "input_token_source", "output_token_source"} {
		var name string
		if err := gormDB.Raw(
			"SELECT name FROM pragma_table_info('relay_logs') WHERE name = ? LIMIT 1",
			column,
		).Scan(&name).Error; err != nil {
			t.Fatalf("read column %s: %v", column, err)
		}
		if name != column {
			t.Fatalf("%s column not added by ensureRelayLogColumnsSQLite", column)
		}
	}
	var sources struct {
		InputTokenSource  model.TokenCountSource
		OutputTokenSource model.TokenCountSource
	}
	if err := gormDB.Table("relay_logs").Select("input_token_source", "output_token_source").Where("id = 1").Scan(&sources).Error; err != nil {
		t.Fatalf("read legacy token sources: %v", err)
	}
	if sources.InputTokenSource != model.TokenCountSourceLegacy || sources.OutputTokenSource != model.TokenCountSourceLegacy {
		t.Fatalf("legacy row token sources = %q/%q, want legacy/legacy", sources.InputTokenSource, sources.OutputTokenSource)
	}

	// 没发出过 recreateTable 特征 SQL。
	for _, sql := range capture.snapshot() {
		if strings.Contains(strings.ToLower(sql), "relay_logs__temp") {
			t.Fatalf("ensureRelayLogColumnsSQLite triggered recreateTable: %s", sql)
		}
	}
}

// TestEnsureRelayLogColumnsSQLite_NoopOnCurrentSchema 验证幂等：
// 在 model 已声明的完整 schema 上重复跑不会发出任何 ALTER TABLE。
func TestEnsureRelayLogColumnsSQLite_NoopOnCurrentSchema(t *testing.T) {
	capture := &sqlCaptureLogger{}
	gormDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: capture})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := gormDB.AutoMigrate(&model.RelayLog{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}

	capture.reset()
	if err := ensureRelayLogColumnsSQLite(gormDB); err != nil {
		t.Fatalf("ensureRelayLogColumnsSQLite failed: %v", err)
	}
	for _, sql := range capture.snapshot() {
		upper := strings.ToUpper(sql)
		if strings.Contains(upper, "ALTER TABLE") || strings.Contains(upper, "RELAY_LOGS__TEMP") {
			t.Fatalf("ensureRelayLogColumnsSQLite must be a no-op on current schema, but emitted: %s", sql)
		}
	}
}

func TestInitDBSQLiteUsesMultiConnectionPool(t *testing.T) {
	if db != nil {
		_ = Close()
	}
	if err := InitDB("sqlite", filepath.Join(t.TempDir(), "pool.db"), false); err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	t.Cleanup(func() { _ = Close() })

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	if got := sqlDB.Stats().MaxOpenConnections; got < 2 {
		t.Fatalf("SQLite pool has %d max open connections; concurrent WAL reads require at least 2", got)
	}
}

func TestInitDBMigratesLegacyProxyConfigurationsForSubscriptions(t *testing.T) {
	if db != nil {
		_ = Close()
	}
	path := filepath.Join(t.TempDir(), "legacy-proxy.db")
	legacy, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open legacy database: %v", err)
	}
	if err := legacy.Exec(`CREATE TABLE proxy_configurations (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL UNIQUE,
		url TEXT NOT NULL UNIQUE,
		enabled NUMERIC DEFAULT 1,
		remark TEXT,
		created_at DATETIME,
		updated_at DATETIME
	)`).Error; err != nil {
		t.Fatalf("create legacy proxy table: %v", err)
	}
	if err := legacy.Exec(`INSERT INTO proxy_configurations (name, url, enabled) VALUES (?, ?, ?)`, "legacy", "socks5://127.0.0.1:1080", true).Error; err != nil {
		t.Fatalf("insert legacy proxy: %v", err)
	}
	if err := legacy.Exec(`CREATE TABLE proxy_subscription_nodes (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		proxy_configuration_id INTEGER NOT NULL,
		url TEXT NOT NULL,
		active NUMERIC NOT NULL DEFAULT 1,
		health_status varchar(16) NOT NULL DEFAULT 'failed',
		latency_ms INTEGER,
		last_checked_at DATETIME,
		last_error TEXT,
		created_at DATETIME,
		updated_at DATETIME,
		CONSTRAINT idx_proxy_subscription_node_url UNIQUE (proxy_configuration_id, url)
	)`).Error; err != nil {
		t.Fatalf("create legacy proxy subscription node table: %v", err)
	}
	legacySQL, err := legacy.DB()
	if err != nil {
		t.Fatalf("get legacy sql database: %v", err)
	}
	if err := legacySQL.Close(); err != nil {
		t.Fatalf("close legacy database: %v", err)
	}

	if err := InitDB("sqlite", path, false); err != nil {
		t.Fatalf("migrate legacy database: %v", err)
	}
	t.Cleanup(func() { _ = Close() })

	var config model.ProxyConfiguration
	if err := db.Where("name = ?", "legacy").First(&config).Error; err != nil {
		t.Fatalf("read migrated proxy: %v", err)
	}
	if config.Type != model.ProxyConfigurationTypeSingle {
		t.Fatalf("migrated proxy type = %q, want %q", config.Type, model.ProxyConfigurationTypeSingle)
	}
	if config.RefreshIntervalMinutes != model.DefaultProxySubscriptionRefreshMinutes {
		t.Fatalf("migrated refresh interval = %d", config.RefreshIntervalMinutes)
	}
	if config.LastSyncStatus != model.ProxySubscriptionSyncIdle {
		t.Fatalf("migrated sync status = %q, want idle", config.LastSyncStatus)
	}
	if !db.Migrator().HasTable(&model.ProxySubscriptionNode{}) {
		t.Fatal("proxy subscription nodes table was not created")
	}
	for _, field := range []string{"runtime_failure_count", "quarantined_until", "last_runtime_failure_at", "last_runtime_error"} {
		if !db.Migrator().HasColumn(&model.ProxySubscriptionNode{}, field) {
			t.Fatalf("proxy subscription nodes column %s was not migrated", field)
		}
	}
	inactiveNode := model.ProxySubscriptionNode{
		ProxyConfigurationID: config.ID,
		URL:                  "socks5://127.0.0.1:1081",
		Active:               false,
		HealthStatus:         model.ProxyTestHealthFailed,
	}
	if err := db.Create(&inactiveNode).Error; err != nil {
		t.Fatalf("create inactive node after migration: %v", err)
	}
	var persistedNode model.ProxySubscriptionNode
	if err := db.First(&persistedNode, inactiveNode.ID).Error; err != nil {
		t.Fatalf("read inactive node after migration: %v", err)
	}
	if persistedNode.Active {
		t.Fatal("inactive node became active after legacy schema migration")
	}
}

func TestInitDBAdvancesRelayLogIDPastPersistedMaximum(t *testing.T) {
	if db != nil {
		_ = Close()
	}
	path := filepath.Join(t.TempDir(), "snowflake.db")
	if err := InitDB("sqlite", path, false); err != nil {
		t.Fatalf("first InitDB failed: %v", err)
	}
	futureID := time.Now().UnixMilli() + 60_000
	if err := db.Create(&model.RelayLog{ID: futureID, Time: time.Now().Unix()}).Error; err != nil {
		t.Fatalf("insert relay log: %v", err)
	}
	if err := Close(); err != nil {
		t.Fatalf("close first database: %v", err)
	}
	if err := InitDB("sqlite", path, false); err != nil {
		t.Fatalf("second InitDB failed: %v", err)
	}
	t.Cleanup(func() { _ = Close() })

	if got := snowflake.GenerateID(); got <= futureID {
		t.Fatalf("GenerateID() = %d, want greater than persisted maximum %d", got, futureID)
	}
}
