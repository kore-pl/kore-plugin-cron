package croncore

import (
	"context"
	"testing"
	"time"
)

// TestRunlogAppendQuery：追加/按任务名过滤/时间过滤/limit 与最新在前。
func TestRunlogAppendQuery(t *testing.T) {
	t.Setenv("KORE_CRON_RUNLOG_DIR", t.TempDir())
	now := time.Now().UTC().Truncate(time.Second)
	base := RunlogEntry{
		DurationMS: 12, ExitCode: 0, OutputHead: "ok", OutputBytes: 2, Mode: "manual",
	}
	entries := []RunlogEntry{
		{TaskName: "alpha", StartedAt: now.Add(-2 * time.Minute).Format(time.RFC3339)},
		{TaskName: "beta", StartedAt: now.Add(-1 * time.Minute).Format(time.RFC3339)},
		{TaskName: "alpha", StartedAt: now.Format(time.RFC3339)},
	}
	for _, entry := range entries {
		entry.DurationMS, entry.ExitCode = base.DurationMS, base.ExitCode
		entry.OutputHead, entry.OutputBytes, entry.Mode = base.OutputHead, base.OutputBytes, base.Mode
		if err := appendRunLog(runlogFilePath(), entry); err != nil {
			t.Fatalf("追加失败: %v", err)
		}
	}
	store := NewStore()

	result, err := store.Runlog(context.Background(), RunlogParams{})
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if !result.Available || result.Count != 3 || result.Entries[0].TaskName != "alpha" {
		t.Fatalf("查询结果不符（应最新在前）: %+v", result)
	}

	filtered, err := store.Runlog(context.Background(), RunlogParams{Name: "ALP"})
	if err != nil || filtered.Count != 2 {
		t.Fatalf("按任务名过滤不符: %+v err=%v", filtered, err)
	}

	limited, err := store.Runlog(context.Background(), RunlogParams{Limit: 1})
	if err != nil || limited.Count != 1 || limited.Entries[0].TaskName != "alpha" {
		t.Fatalf("limit 不符: %+v err=%v", limited, err)
	}

	since := now.Add(-90 * time.Second).Format(time.RFC3339)
	window, err := store.Runlog(context.Background(), RunlogParams{Since: since})
	if err != nil || window.Count != 2 {
		t.Fatalf("时间过滤不符: %+v err=%v", window, err)
	}
	if _, err := store.Runlog(context.Background(), RunlogParams{Since: "bad"}); err == nil {
		t.Fatal("非法时间应返回错误")
	}
}

// TestRunlogAbsent：还没有执行记录时 available=false 且不是错误。
func TestRunlogAbsent(t *testing.T) {
	t.Setenv("KORE_CRON_RUNLOG_DIR", t.TempDir())
	result, err := NewStore().Runlog(context.Background(), RunlogParams{})
	if err != nil {
		t.Fatalf("无记录不应报错: %v", err)
	}
	if result.Available || result.Count != 0 || result.Note == "" {
		t.Fatalf("无记录结果不符: %+v", result)
	}
}
