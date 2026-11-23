// 启停端到端：走真实插件进程与真实协议帧，验证 disable/enable 的对外契约，
// 以及停用任务在 cron.run 上被拒绝。隔离沿用 KORE_CRON_FILE（临时文件）+ 假 crontab。
package e2e

import (
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/pengtang1024/kore-plugin-cron/internal/croncore"
	"github.com/pengtang1024/kore-plugin-cron/internal/cronfake"
	"github.com/pengtang1024/kore/pkg/pluginapi"
)

func TestToggleProcessE2E(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("UDS 端到端仅类 Unix")
	}
	tmp := t.TempDir()
	fake, err := cronfake.Install(tmp)
	if err != nil {
		t.Fatalf("安装假 crontab 失败: %v", err)
	}
	cronFile := filepath.Join(tmp, "crontab.txt")
	t.Setenv("KORE_CRON_FILE", cronFile)
	t.Setenv("KORE_CRON_BACKUP_DIR", filepath.Join(tmp, "backup"))

	binary := buildPlugin(t, tmp)
	socket := filepath.Join(tmp, "toggle.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("监听 UDS 失败: %v", err)
	}
	defer func() { _ = listener.Close() }()
	_ = listener.(*net.UnixListener).SetDeadline(time.Now().Add(15 * time.Second))
	process, stderr := startPlugin(t, binary, tmp, socket, fake)
	conn, err := listener.Accept()
	if err != nil {
		t.Fatalf("接受插件连接失败: %v (stderr=%s)", err, stderr.String())
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(20 * time.Second))
	handshake(t, conn)

	added := request(t, conn, "t0", "cron.add",
		`{"schedule":"0 3 * * *","command":"echo toggle-me","comment":"nightly toggle"}`)
	if added.Error != nil {
		t.Fatalf("cron.add 失败: %s", added.Error.Message)
	}

	disabled := request(t, conn, "t1", "cron.disable", `{"index":0}`)
	if disabled.Error != nil {
		t.Fatalf("cron.disable 失败: %s: %s", disabled.Error.Code, disabled.Error.Message)
	}
	var toggle croncore.ToggleResult
	unmarshalResult(t, disabled, &toggle)
	if !toggle.Changed || toggle.Enabled || toggle.Entry.Enabled || toggle.BackupPath == "" {
		t.Fatalf("disable 结果不符: %+v", toggle)
	}

	listed := request(t, conn, "t2", "cron.list", `{}`)
	if listed.Error != nil {
		t.Fatalf("cron.list 失败: %s", listed.Error.Message)
	}
	var list croncore.ListResult
	unmarshalResult(t, listed, &list)
	if list.Count != 1 || list.Entries[0].Enabled {
		t.Fatalf("停用任务应列出且 enabled=false: %+v", list.Entries)
	}
	if list.Entries[0].Comment != "nightly toggle" || list.Entries[0].Command != "echo toggle-me" {
		t.Fatalf("停用不得丢定义/注释: %+v", list.Entries[0])
	}

	run := request(t, conn, "t3", "cron.run", `{"index":0}`)
	if run.Error == nil || run.Error.Code != pluginapi.E_CONFLICT {
		t.Fatalf("停用任务 cron.run 应返回 E_CONFLICT: %+v", run)
	}

	enabled := request(t, conn, "t4", "cron.enable", `{"index":0}`)
	if enabled.Error != nil {
		t.Fatalf("cron.enable 失败: %s: %s", enabled.Error.Code, enabled.Error.Message)
	}
	unmarshalResult(t, enabled, &toggle)
	if !toggle.Changed || !toggle.Enabled || !toggle.Entry.Enabled {
		t.Fatalf("enable 结果不符: %+v", toggle)
	}

	missing := request(t, conn, "t5", "cron.enable", `{}`)
	if missing.Error == nil || missing.Error.Code != pluginapi.E_INVALID {
		t.Fatalf("缺 index 应返回 E_INVALID: %+v", missing)
	}

	data, readErr := os.ReadFile(cronFile)
	if readErr != nil {
		t.Fatalf("读临时 crontab 失败: %v", readErr)
	}
	content := string(data)
	if !strings.Contains(content, "# nightly toggle\n0 3 * * * echo toggle-me\n") {
		t.Fatalf("enable 后应逐字还原: %q", content)
	}

	shutdown := request(t, conn, "t6", "kore.shutdown", `{}`)
	if shutdown.Error != nil {
		t.Fatalf("shutdown 失败: %s", shutdown.Error.Message)
	}
	waitExit(t, process)
}
