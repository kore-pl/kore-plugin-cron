package croncore

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// seedCronFile 写入一份 KORE_CRON_FILE，并返回 store。
func seedCronFile(t *testing.T, content string) *Store {
	t.Helper()
	path := tempCronFile(t)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("写入 crontab 文件失败: %v", err)
	}
	return NewStore()
}

// TestRunOnlyRegisteredTask：只接受已注册任务的定位符，不存在/越界/空参数都报错。
func TestRunOnlyRegisteredTask(t *testing.T) {
	store := seedCronFile(t, "0 3 * * * echo hello\n")
	cases := []struct {
		name   string
		params RunParams
		want   error
	}{
		{"不存在", RunParams{Name: "no-such-job"}, ErrNotFound},
		{"index 越界", RunParams{Index: intPtr(9)}, ErrInvalidParam},
		{"空参数", RunParams{}, ErrInvalidParam},
	}
	for _, tc := range cases {
		if _, err := store.Run(context.Background(), tc.params); !errors.Is(err, tc.want) {
			t.Fatalf("%s 应返回 %v，实际 %v", tc.name, tc.want, err)
		}
	}
}

// TestRunExecutesRegisteredCommand：同步执行，返回真实输出与退出码。
func TestRunExecutesRegisteredCommand(t *testing.T) {
	store := seedCronFile(t, "# nightly\n0 3 * * * echo hello\n")
	result, err := store.Run(context.Background(), RunParams{Index: intPtr(0)})
	if err != nil {
		t.Fatalf("Run 失败: %v", err)
	}
	if !result.Started || result.ExitCode != 0 || strings.TrimSpace(result.Output) != "hello" {
		t.Fatalf("执行结果不符: %+v", result)
	}
	if result.Command != "echo hello" || result.Schedule != "0 3 * * *" {
		t.Fatalf("执行目标不符（应取 crontab 里的命令）: %+v", result)
	}
	if result.DurationMS < 0 {
		t.Fatalf("duration_ms 非法: %d", result.DurationMS)
	}
}

// TestRunReturnsNonZeroExit：命令失败时返回真实退出码，不伪装成功。
func TestRunReturnsNonZeroExit(t *testing.T) {
	store := seedCronFile(t, "0 3 * * * exit 3\n")
	result, err := store.Run(context.Background(), RunParams{Index: intPtr(0)})
	if err != nil {
		t.Fatalf("Run 失败: %v", err)
	}
	if result.ExitCode != 3 || result.Started != true {
		t.Fatalf("退出码不符: %+v", result)
	}
}

// TestRunTimeoutCancels：context 到期后进程被取消，返回 timed_out 与部分输出，
// 且不拖住调用方（60s 预算由调度层给，测试用 150ms 验证取消链路）。
func TestRunTimeoutCancels(t *testing.T) {
	store := seedCronFile(t, "0 3 * * * echo before; sleep 5; echo after\n")
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	started := time.Now()
	result, err := store.Run(ctx, RunParams{Index: intPtr(0)})
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("超时应返回结构化结果而不是错误: %v", err)
	}
	if !result.TimedOut {
		t.Fatalf("应标记 timed_out: %+v", result)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("超时取消耗时过长: %v", elapsed)
	}
	if !strings.Contains(result.Output, "before") {
		t.Fatalf("应保留已产生的部分输出: %q", result.Output)
	}
}

// TestRunAmbiguousName：name 命中多条时要求改用 index，绝不猜一个执行。
func TestRunAmbiguousName(t *testing.T) {
	store := seedCronFile(t, "# backup\n0 3 * * * echo a\n# backup\n0 4 * * * echo b\n")
	_, err := store.Run(context.Background(), RunParams{Name: "backup"})
	if !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("多条匹配应返回 ErrInvalidParam，实际 %v", err)
	}
}

// TestRunResolvesByName：name 唯一匹配时执行对应命令。
func TestRunResolvesByName(t *testing.T) {
	store := seedCronFile(t, "# nightly backup\n0 3 * * * echo unique-name\n")
	result, err := store.Run(context.Background(), RunParams{Name: "nightly backup"})
	if err != nil {
		t.Fatalf("Run 失败: %v", err)
	}
	if strings.TrimSpace(result.Output) != "unique-name" || result.Index != 0 {
		t.Fatalf("按 name 执行结果不符: %+v", result)
	}
}

// TestRunBusyRejectsConcurrent：同一时刻只允许一次手动执行。
func TestRunBusyRejectsConcurrent(t *testing.T) {
	store := seedCronFile(t, "0 3 * * * sleep 0.4\n")
	store.runMu.Lock()
	_, err := store.Run(context.Background(), RunParams{Index: intPtr(0)})
	store.runMu.Unlock()
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("并发执行应返回 ErrBusy，实际 %v", err)
	}
}

// TestLimitedBufferCapsOutput：输出超过上限后仍然消费（不阻塞子进程）但只保留上限。
func TestLimitedBufferCapsOutput(t *testing.T) {
	buffer := &limitedBuffer{limit: 4}
	written, err := buffer.Write([]byte("abcdefgh"))
	if err != nil || written != 8 {
		t.Fatalf("Write 返回不符: n=%d err=%v", written, err)
	}
	if buffer.String() != "abcd" {
		t.Fatalf("保留内容不符: %q", buffer.String())
	}
}

func intPtr(value int) *int { return &value }
