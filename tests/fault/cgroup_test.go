// Package fault 的 cgroup 用例：模拟 cgroup 资源超限后的方法级错误返回。
//
// 断言：注入 FaultCgroup 后，List / Add / Remove 都返回 ErrResource，
// 结果为空，且插件不因资源不足而 panic 或挂起。
package fault

import (
	"context"
	"errors"
	"testing"

	"github.com/pengtang1024/kore-plugin-cron/internal/croncore"
)

func TestCgroupFaultList(t *testing.T) {
	store := croncore.NewStore()
	store.InjectFault(croncore.FaultCgroup)
	_, err := store.List(context.Background())
	assertResource(t, err)
}

func TestCgroupFaultAdd(t *testing.T) {
	store := croncore.NewStore()
	store.InjectFault(croncore.FaultCgroup)
	_, err := store.Add(context.Background(), croncore.AddParams{
		Schedule: "*/5 * * * *", Command: "/usr/local/bin/repair",
	})
	assertResource(t, err)
}

func TestCgroupFaultRemove(t *testing.T) {
	store := croncore.NewStore()
	store.InjectFault(croncore.FaultCgroup)
	_, err := store.Remove(context.Background(), 0)
	assertResource(t, err)
}

// TestCgroupFaultNoPanic 确保资源限额下所有方法不 panic。
func TestCgroupFaultNoPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("cgroup 限额下发生 panic: %v", r)
		}
	}()
	store := croncore.NewStore()
	store.InjectFault(croncore.FaultCgroup)
	_, _ = store.List(context.Background())
	_, _ = store.Add(context.Background(), croncore.AddParams{Schedule: "* * * * *", Command: "true"})
	_, _ = store.Remove(context.Background(), 0)
}

// TestCgroupFaultClearedRecovers 断言清除故障后方法恢复正常，证明故障是
// 可逆注入而非状态污染——生产环境资源恢复后插件应能继续服务。
func TestCgroupFaultClearedRecovers(t *testing.T) {
	store := croncore.NewStore()
	store.InjectFault(croncore.FaultCgroup)
	_, err := store.List(context.Background())
	assertResource(t, err)
	store.InjectFault(croncore.FaultNone)
	result, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("清除故障后应恢复: %v", err)
	}
	if result == nil {
		t.Fatal("恢复后 List 返回 nil")
	}
}

func assertResource(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("期望 ErrResource，实际 nil")
	}
	if !errors.Is(err, croncore.ErrResource) {
		t.Fatalf("错误链不含 ErrResource: %v", err)
	}
}
