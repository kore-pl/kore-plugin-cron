package boundary_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pengtang1024/kore-plugin-cron/internal/croncore"
)

// 并发（判据① 第 2 类）：真 goroutine 同时打同一个 Store、同一份隔离 crontab。
//
// 断言的是进程内写锁 writeMu 的不变式：并发 add / replace / remove 后，
// 最终条目数必须精确等于 1(种子) + 成功 Add 数 - 成功 Remove 数；不允许丢写、
// 也不允许出现被截断/拼接的半行。配合 `go test -race` 验证无数据竞争。
func TestConcurrentMutateSameTask(t *testing.T) {
	store, cronFile := newIsolatedStore(t)
	if _, err := store.Add(ctx(), croncore.AddParams{
		Schedule: "0 3 * * *", Command: "/bin/seed", Comment: "seed",
	}); err != nil {
		t.Fatalf("预置种子任务失败: %v", err)
	}

	const workers = 90
	type outcome struct {
		kind string
		err  error
	}
	results := make(chan outcome, workers)
	start := make(chan struct{})
	var wg sync.WaitGroup

	allowed := map[string]bool{"/bin/seed": true}
	for i := 0; i < workers; i++ {
		allowed["/bin/add-"+strconv.Itoa(i)] = true
		allowed["/bin/replace-"+strconv.Itoa(i)] = true
	}

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			<-start
			switch n % 3 {
			case 0:
				_, err := store.Add(ctx(), croncore.AddParams{
					Schedule: "*/5 * * * *", Command: "/bin/add-" + strconv.Itoa(n),
				})
				results <- outcome{kind: "add", err: err}
			case 1:
				_, err := store.Replace(ctx(), croncore.ReplaceParams{
					Index: 0, Schedule: "0 4 * * *", Command: "/bin/replace-" + strconv.Itoa(n),
				})
				results <- outcome{kind: "replace", err: err}
			default:
				_, err := store.Remove(ctx(), 0)
				results <- outcome{kind: "remove", err: err}
			}
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)

	adds, replaces, removes := 0, 0, 0
	for out := range results {
		if out.err == nil {
			switch out.kind {
			case "add":
				adds++
			case "replace":
				replaces++
			case "remove":
				removes++
			}
			continue
		}
		// 并发下 index 被别的 goroutine 删掉是合法竞态结果，但只能是参数错误；
		// 任何 I/O / 超时 / 未知错误都说明写路径被并发破坏。
		if !errors.Is(out.err, croncore.ErrInvalidParam) && !errors.Is(out.err, croncore.ErrNotFound) {
			t.Fatalf("并发 %s 出现非参数类错误: %v", out.kind, out.err)
		}
	}

	want := 1 + adds - removes
	result, err := store.List(ctx())
	if err != nil {
		t.Fatalf("并发写入后 List 失败: %v", err)
	}
	if result.Count != want || len(result.Entries) != want {
		t.Fatalf("并发写入丢写/多写: 期望 %d 条（1+%d add-%d remove），实际 count=%d entries=%d",
			want, adds, removes, result.Count, len(result.Entries))
	}
	for _, entry := range result.Entries {
		if !allowed[entry.Command] {
			t.Fatalf("出现用例未写入的命令（疑似写路径串行错乱）: %q", entry.Command)
		}
		if err := croncore.ValidateSchedule(entry.Schedule); err != nil {
			t.Fatalf("出现非法 schedule（疑似半截行）: %q: %v", entry.Schedule, err)
		}
		if strings.TrimSpace(entry.Raw) == "" {
			t.Fatalf("出现空的任务行: %+v", entry)
		}
	}

	data := readCrontabBytes(t, cronFile)
	if len(data) > 0 && data[len(data)-1] != '\n' {
		t.Fatalf("crontab 未以换行结尾（疑似被截断）: %q", string(data))
	}
	if strings.ContainsRune(string(data), '\x00') {
		t.Fatalf("crontab 出现 NUL 字节（疑似写坏）: %q", string(data))
	}
	// 原子写（临时文件 + rename）不得遗留 .cron-*.tmp。
	for _, name := range dirEntries(t, filepath.Dir(cronFile)) {
		if strings.HasPrefix(name, ".cron-") {
			t.Fatalf("发现残留临时文件 %q，原子写清理不完整", name)
		}
	}

	// 备份数量必须与发生写盘的次数一致（Add 成功 + Remove 成功），证明"先备份、
	// 再整份写回"没有被并发绕过；种子 Add 也产生 1 个备份。
	backupDir := os.Getenv("KORE_CRON_BACKUP_DIR")
	wantBackups := 1 + adds + replaces + removes
	if got := len(dirEntries(t, backupDir)); got != wantBackups {
		t.Fatalf("备份文件数不符: 期望 %d，实际 %d", wantBackups, got)
	}
}

// 并发（判据① 第 2 类）：同一任务被并发触发时必须串行——只有一个执行实例，
// 其余立刻拿到 ErrBusy，而不是排队或并发跑出脏数据（runMu 的不变式）。
//
// 用"执行体等 release 文件"把第一个执行者钉在临界区里：其余 goroutine 无论
// 何时被调度，都只可能看到锁被占用，因此断言完全确定，不依赖 sleep 竞速。
func TestConcurrentRunSameTask(t *testing.T) {
	store, cronFile := newIsolatedStore(t)
	dir := t.TempDir()
	started := filepath.Join(dir, "started")
	release := filepath.Join(dir, "release")
	command := "/bin/sh -c 'touch " + started + "; while [ ! -e " + release + " ]; do sleep 0.02; done'"
	seedCrontab(t, cronFile, "0 3 * * * "+command+"\n")

	const workers = 8
	index := 0
	type outcome struct {
		result *croncore.RunResult
		err    error
	}
	results := make(chan outcome, workers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	runCtx, cancel := context.WithTimeout(ctx(), 30*time.Second)
	defer cancel()

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			result, err := store.Run(runCtx, croncore.RunParams{Index: &index})
			results <- outcome{result: result, err: err}
		}()
	}
	close(start)

	waitFile(t, started, 10*time.Second)

	// 执行者被 release 钉住，另外 7 个必须全部 ErrBusy。
	busy := 0
	for busy < workers-1 {
		select {
		case out := <-results:
			if !errors.Is(out.err, croncore.ErrBusy) {
				t.Fatalf("同一任务并发触发应返回 ErrBusy，实际 result=%+v err=%v", out.result, out.err)
			}
			busy++
		case <-time.After(10 * time.Second):
			t.Fatalf("等待 %d 个 ErrBusy 超时（已收到 %d）", workers-1, busy)
		}
	}

	if err := os.WriteFile(release, []byte("go"), 0o600); err != nil {
		t.Fatalf("放行执行者失败: %v", err)
	}
	select {
	case out := <-results:
		if out.err != nil {
			t.Fatalf("唯一获准的执行应成功，实际 %v", out.err)
		}
		if out.result == nil || !out.result.Started {
			t.Fatalf("执行结果缺少 started 标记: %+v", out.result)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("等待执行者结束超时")
	}
	wg.Wait()

	// 并发触发不得写多条执行记录：runlog 恰好 1 行。
	runlogDir := os.Getenv("KORE_CRON_RUNLOG_DIR")
	files := dirEntries(t, runlogDir)
	if len(files) != 1 {
		t.Fatalf("runlog 文件数不符: %v", files)
	}
	data := readCrontabBytes(t, filepath.Join(runlogDir, files[0]))
	trimmed := strings.TrimSpace(string(data))
	lines := 0
	if trimmed != "" {
		lines = strings.Count(trimmed, "\n") + 1
	}
	if lines != 1 {
		t.Fatalf("并发触发写入了 %d 条 runlog，期望 1 条:\n%s", lines, string(data))
	}
}

// waitFile 轮询等待文件出现（并发用例的同步点，只用于确认执行者真的进了临界区）。
func waitFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("等待文件出现超时: %s", path)
}
