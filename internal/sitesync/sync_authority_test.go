package sitesync

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/U188/octopus/internal/model"
)

// 回归：全部分组端点瞬时失败（5xx/超时）时必须返回错误，
// 不得回退成伪权威的默认分组——那会让历史分组被判定为"上游已移除"，
// 连带清掉用户手工 Key、手工模型和投影设置。
func TestFetchManagementGroupsTransientFailureReturnsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream exploded", http.StatusInternalServerError)
	}))
	defer server.Close()

	groups, err := fetchManagementGroups(context.Background(), &model.Site{
		Platform: model.SitePlatformNewAPI,
		BaseURL:  server.URL,
	}, &model.SiteAccount{}, "token")
	if err == nil {
		t.Fatalf("expected error for all-endpoints transient failure, got groups %+v", groups)
	}
}

// 平台没有分组端点（404）不属于失败：仍应回退默认分组，保持这类站点可同步。
func TestFetchManagementGroupsNotFoundFallsBackToDefault(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer server.Close()

	groups, err := fetchManagementGroups(context.Background(), &model.Site{
		Platform: model.SitePlatformNewAPI,
		BaseURL:  server.URL,
	}, &model.SiteAccount{}, "token")
	if err != nil {
		t.Fatalf("404 endpoints must fall back to default group, got error: %v", err)
	}
	if len(groups) != 1 || groups[0].GroupKey != model.SiteDefaultGroupKey {
		t.Fatalf("expected single default group, got %+v", groups)
	}
}

// 回归：failed / unresolved 是非权威结论，必须保留既有暂停状态，
// 只累加失败计数；否则已暂停的投影分组会被上游抖动意外"恢复"。
func TestApplyPersistedGroupSyncStateKeepsSuspensionOnTransientFailure(t *testing.T) {
	now := time.Now()
	suspendedAt := now.Add(-time.Hour)
	existing := model.SiteUserGroup{
		ProjectionSuspended:     true,
		ProjectionSuspendReason: "该分组没有可用 Key，已暂停投影",
		ProjectionSuspendedAt:   &suspendedAt,
		ModelSyncFailureCount:   2,
	}

	for _, status := range []siteGroupSyncStatus{siteGroupSyncStatusFailed, siteGroupSyncStatusUnresolved} {
		group := model.SiteUserGroup{}
		applyPersistedGroupSyncState(&group, &existing, siteGroupSyncResult{Status: status}, now)
		if !group.ProjectionSuspended {
			t.Fatalf("status %s must keep ProjectionSuspended", status)
		}
		if group.ProjectionSuspendReason != existing.ProjectionSuspendReason {
			t.Fatalf("status %s must keep suspend reason, got %q", status, group.ProjectionSuspendReason)
		}
		if group.ProjectionSuspendedAt == nil || !group.ProjectionSuspendedAt.Equal(suspendedAt) {
			t.Fatalf("status %s must keep suspend timestamp", status)
		}
		if group.ModelSyncFailureCount != existing.ModelSyncFailureCount+1 {
			t.Fatalf("status %s must increment failure count, got %d", status, group.ModelSyncFailureCount)
		}
	}

	// synced 仍应清除暂停（权威成功结论）
	group := model.SiteUserGroup{}
	applyPersistedGroupSyncState(&group, &existing, siteGroupSyncResult{Status: siteGroupSyncStatusSynced}, now)
	if group.ProjectionSuspended {
		t.Fatal("synced must clear ProjectionSuspended")
	}
}

// 回归：同步合并必须保留匹配到的既有 Token 行 ID（稳定标识），
// 否则每轮同步轮换全部 ID，用户按旧 ID 提交的禁用/删除操作会失效。
func TestMergePersistedSiteTokensKeepsStableIDs(t *testing.T) {
	now := time.Now()
	existing := []model.SiteToken{{
		ID:            77,
		SiteAccountID: 1,
		Name:          "primary",
		Token:         "sk-stable-token",
		GroupKey:      "vip",
		Enabled:       true,
		ValueStatus:   model.SiteTokenValueStatusReady,
		Source:        "sync",
	}}
	incoming := []model.SiteToken{{
		Name:        "primary",
		Token:       "sk-stable-token",
		GroupKey:    "vip",
		Enabled:     true,
		ValueStatus: model.SiteTokenValueStatusReady,
		Source:      "sync",
	}}

	merged := mergePersistedSiteTokens(1, existing, incoming, now, nil)
	if len(merged) != 1 {
		t.Fatalf("expected one merged token, got %+v", merged)
	}
	if merged[0].ID != 77 {
		t.Fatalf("merged token must keep existing row ID 77, got %d", merged[0].ID)
	}
}
