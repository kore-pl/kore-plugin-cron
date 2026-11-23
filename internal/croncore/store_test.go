package croncore

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/pengtang1024/kore-plugin-cron/internal/cronfake"
)

// newTestStore 安装假 crontab 并返回使用临时备份目录的 Store。
func newTestStore(t *testing.T) (*Store, *cronfake.State) {
	t.Helper()
	fake, err := cronfake.Install(t.TempDir())
	if err != nil {
		t.Fatalf("安装假 crontab 失败: %v", err)
	}
	fake.Use(t)
	return NewStore(), fake
}

func TestListNoCrontabReturnsEmptyArray(t *testing.T) {
	store, _ := newTestStore(t)
	result, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("无 crontab 不应报错: %v", err)
	}
	if result.Count != 0 || len(result.Entries) != 0 {
		t.Fatalf("无 crontab 应返回空数组: %+v", result)
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	if !strings.Contains(string(data), `"entries":[]`) {
		t.Fatalf("entries 必须序列化为空数组而非 null: %s", data)
	}
}

func TestAddListWithComment(t *testing.T) {
	store, fake := newTestStore(t)
	ctx := context.Background()
	added, err := store.Add(ctx, AddParams{
		Schedule: "0 3 * * *", Command: "/usr/bin/backup", Comment: "nightly backup",
	})
	if err != nil {
		t.Fatalf("Add 失败: %v", err)
	}
	if added.Entry == nil || added.Entry.Index != 0 || added.Entry.Schedule != "0 3 * * *" {
		t.Fatalf("Add 返回条目不符: %+v", added.Entry)
	}
	if added.BackupPath == "" {
		t.Fatal("Add 未返回 backup_path")
	}
	if _, err := os.Stat(added.BackupPath); err != nil {
		t.Fatalf("备份文件不存在: %v", err)
	}
	content, _ := fake.Content()
	if !strings.Contains(content, "# nightly backup\n0 3 * * * /usr/bin/backup\n") {
		t.Fatalf("写回内容不符: %q", content)
	}
	result, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List 失败: %v", err)
	}
	if result.Count != 1 || result.Entries[0].Comment != "nightly backup" {
		t.Fatalf("List 结果不符: %+v", result)
	}
}

func TestAddAppendsAfterExistingEntries(t *testing.T) {
	store, fake := newTestStore(t)
	_ = fake.Seed("0 1 * * * /bin/one\n")
	if _, err := store.Add(context.Background(), AddParams{
		Schedule: "0 2 * * *", Command: "/bin/two",
	}); err != nil {
		t.Fatalf("Add 失败: %v", err)
	}
	result, _ := store.List(context.Background())
	if result.Count != 2 || result.Entries[0].Command != "/bin/one" ||
		result.Entries[1].Command != "/bin/two" {
		t.Fatalf("追加顺序不符: %+v", result.Entries)
	}
}

func TestRemoveMiddleEntryAndComment(t *testing.T) {
	store, fake := newTestStore(t)
	old := "# first\n0 1 * * * /bin/one\n# second\n0 2 * * * /bin/two\n0 3 * * * /bin/three\n"
	_ = fake.Seed(old)
	removed, err := store.Remove(context.Background(), 1)
	if err != nil {
		t.Fatalf("Remove 失败: %v", err)
	}
	if removed.Removed.Command != "/bin/two" || removed.Removed.Comment != "second" {
		t.Fatalf("被删除条目不符: %+v", removed.Removed)
	}
	content, _ := fake.Content()
	if strings.Contains(content, "/bin/two") || strings.Contains(content, "# second") {
		t.Fatalf("注释或任务未删除: %q", content)
	}
	result, _ := store.List(context.Background())
	if result.Count != 2 || result.Entries[0].Command != "/bin/one" ||
		result.Entries[1].Command != "/bin/three" {
		t.Fatalf("删除后条目不符: %+v", result.Entries)
	}
	backup, err := os.ReadFile(removed.BackupPath)
	if err != nil || string(backup) != old {
		t.Fatalf("备份内容应为删除前的完整 crontab: %q, err=%v", backup, err)
	}
}

func TestRemoveOutOfRange(t *testing.T) {
	store, fake := newTestStore(t)
	_ = fake.Seed("0 1 * * * /bin/one\n")
	for _, index := range []int{-1, 1} {
		_, err := store.Remove(context.Background(), index)
		if !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("index=%d 期望 ErrInvalidParam，实际 %v", index, err)
		}
	}
}

func TestReplaceKeepsComment(t *testing.T) {
	store, fake := newTestStore(t)
	_ = fake.Seed("# keep me\n0 1 * * * /bin/old\n")
	replaced, err := store.Replace(context.Background(), ReplaceParams{
		Index: 0, Schedule: "*/10 * * * *", Command: "/bin/new --flag",
	})
	if err != nil {
		t.Fatalf("Replace 失败: %v", err)
	}
	if replaced.Entry.Schedule != "*/10 * * * *" || replaced.Entry.Comment != "keep me" {
		t.Fatalf("替换结果不符: %+v", replaced.Entry)
	}
	content, _ := fake.Content()
	if !strings.Contains(content, "# keep me\n*/10 * * * * /bin/new --flag\n") ||
		strings.Contains(content, "/bin/old") {
		t.Fatalf("替换后内容不符: %q", content)
	}
}

func TestReplaceOutOfRange(t *testing.T) {
	store, _ := newTestStore(t)
	_, err := store.Replace(context.Background(), ReplaceParams{
		Index: 0, Schedule: "0 1 * * *", Command: "/bin/x",
	})
	if !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("空 crontab 替换应返回 ErrInvalidParam，实际 %v", err)
	}
}

// TestConcurrentAdds 验证进程内写锁：并发 Add 不丢失、不覆盖。
func TestConcurrentAdds(t *testing.T) {
	store, _ := newTestStore(t)
	const workers = 8
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_, err := store.Add(context.Background(), AddParams{
				Schedule: "0 3 * * *", Command: "/bin/job-" + itoa(n),
			})
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("并发 Add 失败: %v", err)
		}
	}
	result, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("List 失败: %v", err)
	}
	if result.Count != workers {
		t.Fatalf("并发写入丢失: 期望 %d 条，实际 %d", workers, result.Count)
	}
}

// writeFakeCrontab 把 PATH 指向只含一个假 crontab 的临时目录。
func writeFakeCrontab(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "crontab")
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("写入假 crontab 失败: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestListPermissionDeniedReturnsWarning 验证权限不足是环境限制：
// 返回成功 + 空列表 + warning，而不是把它当服务故障。
func TestListPermissionDeniedReturnsWarning(t *testing.T) {
	writeFakeCrontab(t, "#!/bin/sh\necho 'fopen: 权限不够' >&2\nexit 1\n")
	result, err := NewStore().List(context.Background())
	if err != nil {
		t.Fatalf("权限不足不应返回错误: %v", err)
	}
	if result.Count != 0 || len(result.Entries) != 0 || result.Warning == "" {
		t.Fatalf("权限不足应返回空列表 + warning: %+v", result)
	}
}

// TestListRealIOFailureStillErrors 验证真正的 I/O 失败不被吞掉。
func TestListRealIOFailureStillErrors(t *testing.T) {
	writeFakeCrontab(t, "#!/bin/sh\necho 'boom: cannot open spool' >&2\nexit 1\n")
	if _, err := NewStore().List(context.Background()); !errors.Is(err, ErrIO) {
		t.Fatalf("真实 I/O 失败仍应为 ErrIO，实际: %v", err)
	}
}
