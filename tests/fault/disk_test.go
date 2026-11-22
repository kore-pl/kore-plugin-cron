// Package fault 的 disk 用例：模拟磁盘满（备份或 crontab 写入失败）后的
// 方法级错误返回。断言：写方法返回 ErrIO 且不污染 crontab，只读 List 不受影响。
package fault

import (
	"context"
	"errors"
	"testing"

	"github.com/pengtang1024/kore-plugin-cron/internal/croncore"
)

func TestDiskFullFaultAdd(t *testing.T) {
	store := croncore.NewStore()
	store.InjectFault(croncore.FaultDiskFull)
	_, err := store.Add(context.Background(), croncore.AddParams{
		Schedule: "0 3 * * *", Command: "/usr/sbin/logrotate",
	})
	assertIO(t, err)
}

func TestDiskFullFaultRemove(t *testing.T) {
	store := croncore.NewStore()
	// 先在无故障时成功加入任务
	if _, err := store.Add(context.Background(), croncore.AddParams{
		Schedule: "0 3 * * *", Command: "/usr/sbin/logrotate",
	}); err != nil {
		t.Fatalf("准备数据失败: %v", err)
	}
	store.InjectFault(croncore.FaultDiskFull)
	_, err := store.Remove(context.Background(), 0)
	assertIO(t, err)
}

// TestDiskFullFaultListUnaffected 断言磁盘故障只影响写方法，只读 List 仍可用，
// 体现方法级故障隔离而非全局崩溃。
func TestDiskFullFaultListUnaffected(t *testing.T) {
	resetCrontab(t)
	store := croncore.NewStore()
	store.InjectFault(croncore.FaultDiskFull)
	result, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("只读 List 不应受磁盘满影响: %v", err)
	}
	if result == nil || result.Count != 0 {
		t.Fatalf("List 结果不符: %+v", result)
	}
}

// TestDiskFullFaultNoMutation 断言写失败后 crontab 未被部分修改（原子性）。
func TestDiskFullFaultNoMutation(t *testing.T) {
	resetCrontab(t)
	store := croncore.NewStore()
	store.InjectFault(croncore.FaultDiskFull)
	_, _ = store.Add(context.Background(), croncore.AddParams{
		Schedule: "0 3 * * *", Command: "/bin/true",
	})
	store.InjectFault(croncore.FaultNone)
	result, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("List 失败: %v", err)
	}
	if result.Count != 0 {
		t.Fatalf("磁盘写失败却污染了 crontab: %d 条任务", result.Count)
	}
}

func assertIO(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("期望 ErrIO，实际 nil")
	}
	if !errors.Is(err, croncore.ErrIO) {
		t.Fatalf("错误链不含 ErrIO: %v", err)
	}
}
