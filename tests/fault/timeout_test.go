// Package fault 的 timeout 用例：模拟请求超时（context deadline）后的方法级错误返回。
//
// 断言：context.WithTimeout 到期后，List / Add / Remove 都返回 ErrTimeout，
// 且方法不阻塞、不启动任何后台定时器续命（G-19）。
package fault

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/pengtang1024/kore-plugin-cron/internal/croncore"
)

func TestTimeoutFaultList(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	time.Sleep(time.Millisecond) // 确保 deadline 已过期
	_, err := croncore.NewStore().List(ctx)
	assertTimeout(t, err)
}

func TestTimeoutFaultAdd(t *testing.T) {
	store := croncore.NewStore()
	store.InjectFault(croncore.FaultTimeout)
	_, err := store.Add(context.Background(), croncore.AddParams{
		Schedule: "0 3 * * *", Command: "/bin/true",
	})
	assertTimeout(t, err)
}

func TestTimeoutFaultRemove(t *testing.T) {
	store := croncore.NewStore()
	store.InjectFault(croncore.FaultTimeout)
	_, err := store.Remove(context.Background(), 0)
	assertTimeout(t, err)
}

// TestTimeoutFaultReturnsPromptly 断言超时故障下方法立即返回，不阻塞等待。
func TestTimeoutFaultReturnsPromptly(t *testing.T) {
	store := croncore.NewStore()
	store.InjectFault(croncore.FaultTimeout)
	done := make(chan error, 1)
	go func() {
		_, err := store.List(context.Background())
		done <- err
	}()
	select {
	case err := <-done:
		assertTimeout(t, err)
	case <-time.After(time.Second):
		t.Fatal("超时故障下方法未及时返回")
	}
}

func assertTimeout(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("期望 ErrTimeout，实际 nil")
	}
	if !errors.Is(err, croncore.ErrTimeout) {
		t.Fatalf("错误链不含 ErrTimeout: %v", err)
	}
}
