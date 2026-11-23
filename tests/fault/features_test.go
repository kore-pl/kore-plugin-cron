// Package fault 的新能力用例：任务类型 / 日志 / 立即执行在四类故障下的返回，
// 以及在故障下"绝不产生副作用"（不写 crontab、不执行任务命令）。
package fault

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pengtang1024/kore-plugin-cron/internal/croncore"
)

// TestFaultRunKilled：进程被终止类故障下，cron.run 直接返回 ErrInterrupted，
// 且不执行任务命令（副作用文件不存在）。
func TestFaultRunKilled(t *testing.T) {
	resetCrontab(t)
	marker := filepath.Join(t.TempDir(), "killed-marker")
	store := croncore.NewStore()
	if _, err := store.Add(context.Background(), croncore.AddParams{
		Schedule: "0 3 * * *", Command: "touch " + marker,
	}); err != nil {
		t.Fatalf("准备数据失败: %v", err)
	}
	store.InjectFault(croncore.FaultKilled)
	_, err := store.Run(context.Background(), croncore.RunParams{Index: intPtr(0)})
	if !errors.Is(err, croncore.ErrInterrupted) {
		t.Fatalf("应返回 ErrInterrupted，实际 %v", err)
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatalf("故障下不得执行任务命令，marker stat=%v", statErr)
	}
}

// TestFaultRunTimeout：超时故障下 Run 立即返回 ErrTimeout，不阻塞。
func TestFaultRunTimeout(t *testing.T) {
	store := croncore.NewStore()
	store.InjectFault(croncore.FaultTimeout)
	started := time.Now()
	_, err := store.Run(context.Background(), croncore.RunParams{Index: intPtr(0)})
	if !errors.Is(err, croncore.ErrTimeout) {
		t.Fatalf("应返回 ErrTimeout，实际 %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("超时故障应立即返回，实际 %v", elapsed)
	}
}

// TestFaultLogsResource：cgroup 限额故障下 logs 返回 ErrResource。
func TestFaultLogsResource(t *testing.T) {
	store := croncore.NewStore()
	store.InjectFault(croncore.FaultCgroup)
	_, err := store.Logs(context.Background(), croncore.LogsParams{})
	if !errors.Is(err, croncore.ErrResource) {
		t.Fatalf("应返回 ErrResource，实际 %v", err)
	}
}

// TestFaultHTTPAddDiskFull：磁盘满时 http 任务写入失败返回 ErrIO，crontab 不变。
func TestFaultHTTPAddDiskFull(t *testing.T) {
	resetCrontab(t)
	store := croncore.NewStore()
	if _, err := store.Add(context.Background(), croncore.AddParams{
		Schedule: "0 3 * * *", Command: "/bin/true",
	}); err != nil {
		t.Fatalf("准备数据失败: %v", err)
	}
	store.InjectFault(croncore.FaultDiskFull)
	_, err := store.Add(context.Background(), croncore.AddParams{
		Type: croncore.TypeHTTP, Schedule: "0 4 * * *", URL: "https://example.com/",
	})
	if !errors.Is(err, croncore.ErrIO) {
		t.Fatalf("应返回 ErrIO，实际 %v", err)
	}
	content, readErr := fakeCrontab.Content()
	if readErr != nil {
		t.Fatalf("读假 crontab 失败: %v", readErr)
	}
	if content != "0 3 * * * /bin/true\n" {
		t.Fatalf("失败写入不得污染 crontab: %q", content)
	}
}

// TestFaultPluginAddNoWrite：plugin 类型不落 crontab，磁盘满不应让它失败。
func TestFaultPluginAddNoWrite(t *testing.T) {
	store := croncore.NewStore()
	store.InjectFault(croncore.FaultDiskFull)
	result, err := store.Add(context.Background(), croncore.AddParams{
		Type: croncore.TypePlugin, Schedule: "0 3 * * *",
		Plugin: "backup", Method: "backup.create",
	})
	if err != nil {
		t.Fatalf("plugin 类型不写盘，不应受磁盘满影响: %v", err)
	}
	if result.SchedulerTask == nil {
		t.Fatal("缺少 scheduler_task 建议")
	}
}

func intPtr(value int) *int { return &value }
