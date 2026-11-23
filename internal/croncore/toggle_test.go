package croncore

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

// TestDisableEnableRoundTrip 是启停的核心契约：
//   - disable 只加注释标记，任务行原文与上方注释完整保留；
//   - list 能看到它（enabled=false），run 拒绝执行；
//   - enable 逐字还原，且状态本来就一致时不写盘（无备份、无 changed）。
func TestDisableEnableRoundTrip(t *testing.T) {
	store, fake := newTestStore(t)
	ctx := context.Background()
	added, err := store.Add(ctx, AddParams{
		Schedule: "0 3 * * *", Command: "/usr/bin/backup --full", Comment: "nightly backup",
	})
	if err != nil {
		t.Fatalf("Add 失败: %v", err)
	}
	original, _ := fake.Content()
	if !added.Entry.Enabled {
		t.Fatalf("新增任务必须默认启用: %+v", added.Entry)
	}

	disabled, err := store.SetEnabled(ctx, 0, false)
	if err != nil {
		t.Fatalf("disable 失败: %v", err)
	}
	if disabled.Changed != true || disabled.Enabled || disabled.Entry.Enabled {
		t.Fatalf("disable 结果不符: %+v", disabled)
	}
	if disabled.BackupPath == "" {
		t.Fatal("disable 写盘必须返回 backup_path")
	}
	if !strings.Contains(disabled.Entry.Raw, "__task-run") ||
		!strings.Contains(disabled.Entry.Raw, "/usr/bin/backup --full") {
		t.Fatalf("停用条目的 raw 应是去标记后的包装命令行: %q", disabled.Entry.Raw)
	}
	if disabled.Entry.Comment != "nightly backup" {
		t.Fatalf("停用不得丢注释: %q", disabled.Entry.Comment)
	}
	content, _ := fake.Content()
	if !strings.Contains(content, "# nightly backup\n# kore-cron:") ||
		!strings.Contains(content, DisabledPrefix+"0 3 * * * ") ||
		!strings.Contains(content, "'/usr/bin/backup --full'") {
		t.Fatalf("停用后的 crontab 不符（标记应加在包装命令行上）: %q", content)
	}

	listed, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List 失败: %v", err)
	}
	if listed.Count != 1 || listed.Entries[0].Enabled || listed.Entries[0].Command != "/usr/bin/backup --full" {
		t.Fatalf("停用任务应仍可列出且 enabled=false: %+v", listed.Entries)
	}

	if _, err := store.Run(ctx, RunParams{Index: intPtr(0)}); !errors.Is(err, ErrDisabled) {
		t.Fatalf("停用任务不得被 run 执行，期望 ErrDisabled，实际 %v", err)
	}

	enabled, err := store.SetEnabled(ctx, 0, true)
	if err != nil {
		t.Fatalf("enable 失败: %v", err)
	}
	if !enabled.Changed || !enabled.Enabled {
		t.Fatalf("enable 结果不符: %+v", enabled)
	}
	restored, _ := fake.Content()
	if restored != original {
		t.Fatalf("enable 必须逐字还原原任务:\n got %q\nwant %q", restored, original)
	}

	// 幂等：已启用再 enable 不写盘、不制造备份。
	idempotent, err := store.SetEnabled(ctx, 0, true)
	if err != nil {
		t.Fatalf("幂等 enable 失败: %v", err)
	}
	if idempotent.Changed || idempotent.BackupPath != "" {
		t.Fatalf("幂等 enable 不应写盘: %+v", idempotent)
	}
	if after, _ := fake.Content(); after != restored {
		t.Fatalf("幂等操作不得改动 crontab: %q", after)
	}
}

// TestDisablePreservesIndexAndRemove 验证停用条目仍占原下标（不重排），
// remove 能按同一 index 删掉它，且删除的是完整任务定义。
func TestDisablePreservesIndexAndRemove(t *testing.T) {
	store, fake := newTestStore(t)
	ctx := context.Background()
	_ = fake.Seed("# first\n0 1 * * * /bin/one\n# second\n0 2 * * * /bin/two\n")
	if _, err := store.SetEnabled(ctx, 0, false); err != nil {
		t.Fatalf("disable 失败: %v", err)
	}
	listed, _ := store.List(ctx)
	if listed.Count != 2 || listed.Entries[0].Enabled || !listed.Entries[1].Enabled {
		t.Fatalf("停用不得改变下标或其它任务状态: %+v", listed.Entries)
	}
	removed, err := store.Remove(ctx, 0)
	if err != nil {
		t.Fatalf("删除停用任务失败: %v", err)
	}
	if removed.Removed.Command != "/bin/one" || removed.Removed.Enabled {
		t.Fatalf("删除结果不符: %+v", removed.Removed)
	}
	content, _ := fake.Content()
	if strings.Contains(content, DisabledPrefix) || strings.Contains(content, "/bin/one") {
		t.Fatalf("删除后不得残留停用行: %q", content)
	}
	if !strings.Contains(content, "/bin/two") {
		t.Fatalf("误删了其它任务: %q", content)
	}
}

// TestReplacePreservesDisabled 验证 replace 不会"顺手启用"停用任务。
func TestReplacePreservesDisabled(t *testing.T) {
	store, fake := newTestStore(t)
	ctx := context.Background()
	_ = fake.Seed("# keep me\n" + DisabledPrefix + "0 3 * * * /bin/old\n")
	replaced, err := store.Replace(ctx, ReplaceParams{Index: 0, Schedule: "0 4 * * *", Command: "/bin/new"})
	if err != nil {
		t.Fatalf("Replace 失败: %v", err)
	}
	if replaced.Entry.Enabled || !strings.HasPrefix(replaced.Entry.Raw, "0 4 * * * ") {
		t.Fatalf("替换结果不符: %+v", replaced.Entry)
	}
	content, _ := fake.Content()
	want := "# keep me\n" + DisabledPrefix + "0 4 * * * /bin/new\n"
	if content != want {
		t.Fatalf("替换必须保留停用标记:\n got %q\nwant %q", content, want)
	}
}

// TestToggleOutOfRange 越界 index 不产生任何写入。
func TestToggleOutOfRange(t *testing.T) {
	store, fake := newTestStore(t)
	_ = fake.Seed("0 3 * * * /bin/one\n")
	for _, index := range []int{-1, 1} {
		if _, err := store.SetEnabled(context.Background(), index, false); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("index=%d 应返回 ErrInvalidParam，实际 %v", index, err)
		}
	}
	content, _ := fake.Content()
	if content != "0 3 * * * /bin/one\n" {
		t.Fatalf("越界操作不得改动 crontab: %q", content)
	}
}

// TestDisabledMarkerInCommentCannotDisable 验证普通注释无法伪造停用标记：
// 带标记文本的 comment 落盘时前面还有 "# "，不会被解析成停用任务。
func TestDisabledMarkerInCommentCannotDisable(t *testing.T) {
	store, fake := newTestStore(t)
	ctx := context.Background()
	if _, err := store.Add(ctx, AddParams{
		Schedule: "0 3 * * *", Command: "/bin/true",
		Comment: "#KORE-DISABLED# 0 3 * * * /bin/false",
	}); err != nil {
		t.Fatalf("Add 失败: %v", err)
	}
	content, _ := fake.Content()
	if !strings.Contains(content, "# #KORE-DISABLED#") {
		t.Fatalf("注释应被写成普通注释行: %q", content)
	}
	listed, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List 失败: %v", err)
	}
	if listed.Count != 1 || !listed.Entries[0].Enabled {
		t.Fatalf("伪造标记不得让任务变成停用: %+v", listed.Entries)
	}
	if _, err := store.Run(ctx, RunParams{Index: intPtr(0)}); err != nil {
		t.Fatalf("该任务应可执行: %v", err)
	}
}

// TestMalformedDisabledLineIsJustComment 验证标记后不是合法任务的行按注释处理：
// 不出现在 list 里、也不可被 run 定位。
func TestMalformedDisabledLineIsJustComment(t *testing.T) {
	store, fake := newTestStore(t)
	_ = fake.Seed(DisabledPrefix + "not a cron line\n" + DisabledPrefix + "0 3 * * *\n")
	listed, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("List 失败: %v", err)
	}
	if listed.Count != 0 {
		t.Fatalf("畸形停用行不得成为任务: %+v", listed.Entries)
	}
	if _, err := store.Run(context.Background(), RunParams{Index: intPtr(0)}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("畸形停用行不可执行，期望 ErrNotFound，实际 %v", err)
	}
}

// TestDisableBackupContainsOriginal 验证停用前的备份是完整的启用态 crontab，
// 出问题时能据此人工恢复。
func TestDisableBackupContainsOriginal(t *testing.T) {
	store, fake := newTestStore(t)
	original := "# nightly\n0 3 * * * /bin/backup\n"
	_ = fake.Seed(original)
	result, err := store.SetEnabled(context.Background(), 0, false)
	if err != nil {
		t.Fatalf("disable 失败: %v", err)
	}
	backup, err := os.ReadFile(result.BackupPath)
	if err != nil {
		t.Fatalf("读备份失败: %v", err)
	}
	if string(backup) != original {
		t.Fatalf("备份内容应为停用前原文: %q", backup)
	}
}
