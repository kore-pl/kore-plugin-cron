package croncore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAddRejectsInvalidParams(t *testing.T) {
	store, fake := newTestStore(t)
	cases := []AddParams{
		{Schedule: "0 3 * *", Command: "/bin/x"},
		{Schedule: "60 3 * * *", Command: "/bin/x"},
		{Schedule: "0 3 * * *", Command: ""},
		{Schedule: "0 3 * * *", Command: "   "},
		{Schedule: "0 3 * * *", Command: "echo a\necho b"},
		{Schedule: "0 3 * * *", Command: "echo a\r"},
		{Schedule: "0 3 * * *", Command: strings.Repeat("x", 513)},
		{Schedule: "0 3 * * *", Command: "/bin/x", Comment: "a\nb"},
		{Schedule: "0 3 * * *", Command: "/bin/x", Comment: strings.Repeat("x", 513)},
	}
	for _, params := range cases {
		_, err := store.Add(context.Background(), params)
		if !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("期望 ErrInvalidParam，实际 %v（params=%+v）", err, params)
		}
	}
	if content, _ := fake.Content(); content != "" {
		t.Fatalf("非法入参不应写 crontab: %q", content)
	}
}

func TestReplaceRejectsInvalidCommand(t *testing.T) {
	store, fake := newTestStore(t)
	_ = fake.Seed("0 1 * * * /bin/one\n")
	_, err := store.Replace(context.Background(), ReplaceParams{
		Index: 0, Schedule: "0 2 * * *", Command: "echo a\necho b",
	})
	if !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("期望 ErrInvalidParam，实际 %v", err)
	}
	if content, _ := fake.Content(); content != "0 1 * * * /bin/one\n" {
		t.Fatalf("非法替换不应改 crontab: %q", content)
	}
}

// TestWriteFailureRollsBack 验证写失败后旧内容被恢复、备份文件保留旧内容。
func TestWriteFailureRollsBack(t *testing.T) {
	store, fake := newTestStore(t)
	old := "0 1 * * * /bin/one\n0 2 * * * /bin/two\n"
	_ = fake.Seed(old)
	if err := fake.FailNextWrite(); err != nil {
		t.Fatalf("注入写失败失败: %v", err)
	}
	_, err := store.Add(context.Background(), AddParams{
		Schedule: "0 3 * * *", Command: "/bin/three",
	})
	if !errors.Is(err, ErrIO) {
		t.Fatalf("期望 ErrIO，实际 %v", err)
	}
	if !strings.Contains(err.Error(), "回滚") {
		t.Fatalf("错误信息应说明已回滚: %v", err)
	}
	content, _ := fake.Content()
	if content != old {
		t.Fatalf("回滚后内容应与旧内容一致:\n期望 %q\n实际 %q", old, content)
	}
	result, listErr := store.List(context.Background())
	if listErr != nil || result.Count != 2 {
		t.Fatalf("回滚后应仍是 2 条: %+v, err=%v", result, listErr)
	}
}

func TestBackupDirDefaultsToStateDir(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("KORE_CRON_BACKUP_DIR", "")
	t.Setenv("KORE_STATE_DIR", stateDir)
	store := NewStore()
	want := filepath.Join(stateDir, "cron-backup")
	if got := store.backupDirPath(); got != want {
		t.Fatalf("默认备份目录不符: got %q want %q", got, want)
	}
	if err := os.MkdirAll(want, 0o700); err != nil {
		t.Fatalf("准备目录失败: %v", err)
	}
}
