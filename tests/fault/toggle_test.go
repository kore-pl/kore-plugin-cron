// 启停能力的故障注入用例：停用/启用在磁盘满与写失败下不得污染 crontab；
// 停用任务在任何路径下都不得被执行。
package fault

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pengtang1024/kore-plugin-cron/internal/croncore"
)

// TestFaultDisableDiskFull：磁盘满时 disable 返回 ErrIO，crontab 与启用态不变。
func TestFaultDisableDiskFull(t *testing.T) {
	resetCrontab(t)
	store := croncore.NewStore()
	if _, err := store.Add(context.Background(), croncore.AddParams{
		Schedule: "0 3 * * *", Command: "/bin/true", Comment: "keep enabled",
	}); err != nil {
		t.Fatalf("准备数据失败: %v", err)
	}
	before, _ := fakeCrontab.Content()
	store.InjectFault(croncore.FaultDiskFull)
	if _, err := store.SetEnabled(context.Background(), 0, false); !errors.Is(err, croncore.ErrIO) {
		t.Fatalf("应返回 ErrIO，实际 %v", err)
	}
	after, _ := fakeCrontab.Content()
	if after != before {
		t.Fatalf("失败写入不得污染 crontab: before=%q after=%q", before, after)
	}
}

// TestFaultDisableRollback：写 crontab 失败时回滚到停用前内容，任务仍启用。
func TestFaultDisableRollback(t *testing.T) {
	resetCrontab(t)
	store := croncore.NewStore()
	if _, err := store.Add(context.Background(), croncore.AddParams{
		Schedule: "0 3 * * *", Command: "/bin/true",
	}); err != nil {
		t.Fatalf("准备数据失败: %v", err)
	}
	before, _ := fakeCrontab.Content()
	if err := fakeCrontab.FailNextWrite(); err != nil {
		t.Fatalf("注入写失败失败: %v", err)
	}
	_, err := store.SetEnabled(context.Background(), 0, false)
	if !errors.Is(err, croncore.ErrIO) {
		t.Fatalf("写失败应返回 ErrIO，实际 %v", err)
	}
	after, _ := fakeCrontab.Content()
	if after != before {
		t.Fatalf("回滚后应与停用前一致: before=%q after=%q", before, after)
	}
	listed, _ := store.List(context.Background())
	if listed.Count != 1 || !listed.Entries[0].Enabled {
		t.Fatalf("回滚后任务必须仍是启用态: %+v", listed.Entries)
	}
}

// TestFaultDisabledNotRunnable：停用任务即使被 run 定位到也不得执行命令，
// 且不得留下任何副作用（marker 不存在）。
func TestFaultDisabledNotRunnable(t *testing.T) {
	resetCrontab(t)
	marker := filepath.Join(t.TempDir(), "must-not-exist")
	store := croncore.NewStore()
	if _, err := store.Add(context.Background(), croncore.AddParams{
		Schedule: "0 3 * * *", Command: "touch " + marker,
	}); err != nil {
		t.Fatalf("准备数据失败: %v", err)
	}
	if _, err := store.SetEnabled(context.Background(), 0, false); err != nil {
		t.Fatalf("disable 失败: %v", err)
	}
	for _, params := range []croncore.RunParams{{Index: intPtr(0)}, {Name: "touch"}} {
		_, err := store.Run(context.Background(), params)
		if !errors.Is(err, croncore.ErrDisabled) {
			t.Fatalf("停用任务应返回 ErrDisabled，实际 %v", err)
		}
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatalf("停用任务不得执行命令，marker stat=%v", statErr)
	}
}

// TestFaultDisabledMarkerIsComment：停用行落盘后必须仍是注释行（系统 cron 不执行），
// 且注入故障后 enable 失败也不得丢失原定义。
func TestFaultDisabledMarkerIsComment(t *testing.T) {
	resetCrontab(t)
	store := croncore.NewStore()
	if _, err := store.Add(context.Background(), croncore.AddParams{
		Schedule: "0 3 * * *", Command: "/bin/true", Comment: "nightly",
	}); err != nil {
		t.Fatalf("准备数据失败: %v", err)
	}
	if _, err := store.SetEnabled(context.Background(), 0, false); err != nil {
		t.Fatalf("disable 失败: %v", err)
	}
	content, _ := fakeCrontab.Content()
	if !strings.Contains(content, "# nightly\n# kore-cron:") ||
		!strings.Contains(content, croncore.DisabledPrefix+"0 3 * * * ") ||
		!strings.Contains(content, "'/bin/true'") {
		t.Fatalf("停用行必须是带标记的注释行: %q", content)
	}
	store.InjectFault(croncore.FaultDiskFull)
	if _, err := store.SetEnabled(context.Background(), 0, true); !errors.Is(err, croncore.ErrIO) {
		t.Fatalf("磁盘满 enable 应返回 ErrIO，实际 %v", err)
	}
	after, _ := fakeCrontab.Content()
	if after != content {
		t.Fatalf("enable 失败不得丢失原定义: %q", after)
	}
}
