package sitesync

import (
	"strings"
	"testing"

	"github.com/U188/octopus/internal/model"
)

func TestBuildSyncSnapshotMessageAuthoritativeEmpty(t *testing.T) {
	results := []siteGroupSyncResult{{
		GroupKey:      model.SiteDefaultGroupKey,
		GroupName:     model.SiteDefaultGroupName,
		HasKey:        true,
		Status:        siteGroupSyncStatusEmpty,
		Authoritative: true,
		Message:       "上游当前没有可用模型",
	}}

	if status := buildSyncSnapshotStatus(results); status != model.SiteExecutionStatusSuccess {
		t.Fatalf("expected success for authoritative empty models, got %q", status)
	}
	message := buildSyncSnapshotMessage(results)
	if !strings.Contains(message, "同步完成") || !strings.Contains(message, "没有可用模型") {
		t.Fatalf("expected explicit empty-model success message, got %q", message)
	}
	if strings.Contains(message, "同步失败") {
		t.Fatalf("empty models must not be reported as sync failure, got %q", message)
	}
}

func TestBuildSyncSnapshotMessageNamedEmptyGroup(t *testing.T) {
	results := []siteGroupSyncResult{{
		GroupKey:      "weekend",
		GroupName:     "周末狂欢",
		HasKey:        true,
		Status:        siteGroupSyncStatusEmpty,
		Authoritative: true,
		Message:       "上游当前没有可用模型",
	}}

	message := buildSyncSnapshotMessage(results)
	if !strings.Contains(message, "周末狂欢") {
		t.Fatalf("expected group name in empty-model message, got %q", message)
	}
	if strings.Contains(message, "同步失败") {
		t.Fatalf("empty models must not be reported as sync failure, got %q", message)
	}
}

func TestBuildSyncSnapshotMessageUnresolvedKeepsFailureDetail(t *testing.T) {
	results := []siteGroupSyncResult{{
		GroupKey:  "vip",
		GroupName: "VIP",
		HasKey:    true,
		Status:    siteGroupSyncStatusFailed,
		Message:   "decode response failed: Hlool API",
	}}

	if status := buildSyncSnapshotStatus(results); status != model.SiteExecutionStatusFailed {
		t.Fatalf("expected failed status for unresolved groups, got %q", status)
	}
	message := buildSyncSnapshotMessage(results)
	if !strings.Contains(message, "同步失败") {
		t.Fatalf("expected failure message, got %q", message)
	}
	if !strings.Contains(message, "VIP") || !strings.Contains(message, "decode response failed") {
		t.Fatalf("expected group failure detail, got %q", message)
	}
}

func TestBuildSyncSnapshotFailureMissingKeyOnly(t *testing.T) {
	results := []siteGroupSyncResult{{
		GroupKey:  "default",
		GroupName: "default",
		HasKey:    false,
		Status:    siteGroupSyncStatusMissingKey,
		Message:   "该分组没有可用 Key，已暂停投影",
	}}
	err := buildSyncSnapshotFailure(results)
	if err == nil {
		t.Fatal("expected missing-key failure")
	}
	if !strings.Contains(err.Error(), "缺少可用 Key") {
		t.Fatalf("expected missing-key failure text, got %v", err)
	}
}
