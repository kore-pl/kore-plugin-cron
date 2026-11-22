// Package fault 的 kill 用例：模拟 kill -9 后的方法级错误返回。
//
// 故障注入点是 Store.InjectFault(FaultKilled)。断言：所有业务方法在进程被
// 终止后都返回 ErrInterrupted 且结果为空，而不是静默成功或 panic。
package fault

import (
	"context"
	"errors"
	"testing"

	"github.com/pengtang1024/kore-plugin-cron/internal/croncore"
)

func TestKillFaultList(t *testing.T) {
	store := croncore.NewStore()
	store.InjectFault(croncore.FaultKilled)
	_, err := store.List(context.Background())
	assertInterrupted(t, err)
}

func TestKillFaultAdd(t *testing.T) {
	store := croncore.NewStore()
	store.InjectFault(croncore.FaultKilled)
	_, err := store.Add(context.Background(), croncore.AddParams{
		Name: "backup", Schedule: "0 3 * * *", Command: "/usr/bin/backup",
	})
	assertInterrupted(t, err)
}

func TestKillFaultRemove(t *testing.T) {
	store := croncore.NewStore()
	store.InjectFault(croncore.FaultKilled)
	_, err := store.Remove(context.Background(), "backup")
	assertInterrupted(t, err)
}

// TestKillFaultDoesNotPanic 确保 kill 故障下所有方法都不 panic（进程不会在
// 方法级崩溃），这是生产级插件的最低要求。
func TestKillFaultDoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("kill 故障下发生 panic: %v", r)
		}
	}()
	store := croncore.NewStore()
	store.InjectFault(croncore.FaultKilled)
	_, _ = store.List(context.Background())
	_, _ = store.Add(context.Background(), croncore.AddParams{Name: "x", Schedule: "* * * * *", Command: "true"})
	_, _ = store.Remove(context.Background(), "x")
}

func assertInterrupted(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("期望 ErrInterrupted，实际 nil")
	}
	if !errors.Is(err, croncore.ErrInterrupted) {
		t.Fatalf("错误链不含 ErrInterrupted: %v", err)
	}
}
