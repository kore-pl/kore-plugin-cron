// Package boundary_test 承载 cron 插件的 production 判据①（边界用例）：
// 异常输入 / 并发 / 权限不足 / 资源耗尽 四类各至少一例，随 `go test ./...`
// 自动进入 CI（Makefile 的 check 目标与 .github/workflows/ci.yml 都跑全包）。
//
// 隔离口径（G-29 运行态写边界）：所有用例只把 KORE_CRON_FILE 指向 t.TempDir()
// 里的普通文件，读改写全部落在临时目录；绝不调用宿主 crontab 命令，也绝不
// 触碰真实用户 crontab。备份目录与 runlog 目录同样指向临时目录。
package boundary_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/pengtang1024/kore-plugin-cron/internal/croncore"
)

// newIsolatedStore 返回一个完全隔离的 Store 与它使用的 crontab 文件路径。
//
// 注意：t.Setenv 与 t.Parallel 互斥（Go 标准库会 panic），因此本包不使用
// t.Parallel；并发证据来自真实 goroutine + `go test -race`。
func newIsolatedStore(t *testing.T) (*croncore.Store, string) {
	t.Helper()
	root := t.TempDir()
	cronFile := filepath.Join(root, "crontab")
	t.Setenv("KORE_CRON_FILE", cronFile)
	t.Setenv("KORE_CRON_BACKUP_DIR", filepath.Join(root, "backup"))
	t.Setenv("KORE_CRON_RUNLOG_DIR", filepath.Join(root, "runlog"))
	t.Setenv("KORE_STATE_DIR", filepath.Join(root, "state"))
	return croncore.NewStore(), cronFile
}

// isolateEnv 只设置 crontab 路径与派生目录，供需要自定义 KORE_CRON_FILE 的用例使用。
func isolateEnv(t *testing.T, cronFile string) *croncore.Store {
	t.Helper()
	root := filepath.Dir(cronFile)
	t.Setenv("KORE_CRON_FILE", cronFile)
	t.Setenv("KORE_CRON_BACKUP_DIR", filepath.Join(root, "backup"))
	t.Setenv("KORE_CRON_RUNLOG_DIR", filepath.Join(root, "runlog"))
	t.Setenv("KORE_STATE_DIR", filepath.Join(root, "state"))
	return croncore.NewStore()
}

// seedCrontab 直接写隔离文件，模拟"当前用户已有任务"。
func seedCrontab(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("预置隔离 crontab 失败: %v", err)
	}
}

// readCrontabBytes 读取隔离文件，用于断言"失败时不留下半截内容"。
func readCrontabBytes(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取隔离 crontab 失败: %v", err)
	}
	return data
}

// mustNotExist 断言失败入参/权限失败没有创建 crontab 文件。
func mustNotExist(t *testing.T, path string) {
	t.Helper()
	_, err := os.Stat(path)
	if err == nil {
		t.Fatalf("预期不写 crontab，但文件已存在: %s", path)
	}
	if !os.IsNotExist(err) {
		t.Fatalf("检查 crontab 文件失败: %v", err)
	}
}

// dirEntries 返回目录下的普通文件名（目录不存在时返回空）。
func dirEntries(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("读取目录 %s 失败: %v", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

// ctx 返回测试用上下文；并发用例各自派生带超时的子上下文。
func ctx() context.Context { return context.Background() }
