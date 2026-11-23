// Package fault 的公共入口：为四类故障注入测试安装假 crontab，确保测试从不
// 触碰本机真实用户 crontab。
package fault

import (
	"os"
	"testing"

	"github.com/pengtang1024/kore-plugin-cron/internal/cronfake"
)

// fakeCrontab 是包级假 crontab 状态；每个用例按需 Reset，避免相互污染。
var fakeCrontab *cronfake.State

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "cron-fault-*")
	if err != nil {
		panic(err)
	}
	fake, err := cronfake.Install(dir)
	if err != nil {
		panic(err)
	}
	fake.ApplyEnv()
	os.Setenv("KORE_STATE_DIR", dir)
	fakeCrontab = fake
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// resetCrontab 在每个用例开始前清空假 crontab，保证用例相互独立。
func resetCrontab(t *testing.T) {
	t.Helper()
	if err := fakeCrontab.Reset(); err != nil {
		t.Fatalf("清空假 crontab 失败: %v", err)
	}
}
