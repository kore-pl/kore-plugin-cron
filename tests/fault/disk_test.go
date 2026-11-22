// Package fault 的 disk 用例：模拟磁盘满（持久化 sink 写失败）后的方法级错误返回。
//
// 断言：注入 FailWriter 作为持久化出口后，Add / Remove 返回 ErrIO 且结果为空；
// 只读的 List 不受磁盘故障影响（方法级故障隔离）。
package fault

import (
	"context"
	"errors"
	"testing"

	"github.com/pengtang1024/kore-plugin-cron/internal/croncore"
)

// failWriter 模拟磁盘满：任何写都返回错误。
type failWriter struct{}

func (failWriter) Write(p []byte) (int, error) {
	return 0, errors.New("no space left on device")
}

func TestDiskFullFaultAdd(t *testing.T) {
	store := croncore.NewStore()
	store.SetSink(failWriter{})
	_, err := store.Add(context.Background(), croncore.AddParams{
		Name: "logrotate", Schedule: "0 3 * * *", Command: "/usr/sbin/logrotate",
	})
	assertIO(t, err)
}

func TestDiskFullFaultRemove(t *testing.T) {
	store := croncore.NewStore()
	// 先在无 sink 时成功加入任务
	if _, err := store.Add(context.Background(), croncore.AddParams{
		Name: "logrotate", Schedule: "0 3 * * *", Command: "/usr/sbin/logrotate",
	}); err != nil {
		t.Fatalf("准备数据失败: %v", err)
	}
	store.SetSink(failWriter{})
	_, err := store.Remove(context.Background(), "logrotate")
	assertIO(t, err)
}

// TestDiskFullFaultListUnaffected 断言磁盘故障只影响写方法，只读 List 仍可用，
// 体现方法级故障隔离而非全局崩溃。
func TestDiskFullFaultListUnaffected(t *testing.T) {
	store := croncore.NewStore()
	store.InjectFault(croncore.FaultDiskFull)
	result, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("只读 List 不应受磁盘满影响: %v", err)
	}
	if result == nil {
		t.Fatal("List 返回 nil 结果")
	}
}

// TestDiskFullFaultNoMutation 断言写失败后内存状态未被部分修改（原子性）。
func TestDiskFullFaultNoMutation(t *testing.T) {
	store := croncore.NewStore()
	store.SetSink(failWriter{})
	_, _ = store.Add(context.Background(), croncore.AddParams{
		Name: "partial", Schedule: "0 3 * * *", Command: "/bin/true",
	})
	result, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("List 失败: %v", err)
	}
	if len(result.Jobs) != 0 {
		t.Fatalf("磁盘写失败却污染了内存状态: %d 个任务", len(result.Jobs))
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
