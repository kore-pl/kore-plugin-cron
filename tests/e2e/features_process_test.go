// 新能力端到端：任务类型 / 日志 / 立即执行，走真实插件进程 + 真实协议帧。
//
// 隔离手段：KORE_CRON_FILE 指向临时文件（完全不调用系统 crontab）、
// KORE_CRON_LOG_FILE 指向构造的 syslog（不依赖本机 journald），
// 因此可以安全地在任意机器上跑，不碰当前用户的真实 crontab。
package e2e

import (
	"encoding/json"
	"fmt"
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

func TestTaskTypesLogsRunE2E(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("UDS 端到端仅类 Unix")
	}
	tmp := t.TempDir()
	fake, err := cronfake.Install(tmp)
	if err != nil {
		t.Fatalf("安装假 crontab 失败: %v", err)
	}
	cronFile := filepath.Join(tmp, "crontab.txt")
	logFile := filepath.Join(tmp, "syslog")
	syslog := strings.Join([]string{
		"Nov 23 10:17:01 host CRON[111]: (pt) CMD (echo hello)",
		"Nov 23 10:20:01 host CRON[222]: (pt) CMD (curl -sS -m 30 -o /dev/null -X POST 'https://example.com/api')",
	}, "\n") + "\n"
	if err := os.WriteFile(logFile, []byte(syslog), 0o600); err != nil {
		t.Fatalf("写 syslog 失败: %v", err)
	}
	t.Setenv("KORE_CRON_FILE", cronFile)
	t.Setenv("KORE_CRON_LOG_FILE", logFile)
	t.Setenv("KORE_CRON_DISABLE_JOURNAL", "1")
	t.Setenv("KORE_CRON_BACKUP_DIR", filepath.Join(tmp, "backup"))

	binary := buildPlugin(t, tmp)
	socket := filepath.Join(tmp, "cron.sock")
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

	steps := []struct {
		method  string
		params  string
		wantErr bool
	}{
		{"cron.add", `{"type":"shell","schedule":"0 3 * * *","command":"echo hello","comment":"nightly"}`, false},
		{"cron.add", `{"type":"http","schedule":"*/5 * * * *","url":"https://example.com/api","method":"POST","headers":{"X-Token":"abc"},"body":"{\"ok\":true}"}`, false},
		{"cron.add", `{"type":"plugin","schedule":"0 4 * * *","comment":"备份","plugin":"backup","method":"backup.create","params":{"scope":"files"}}`, false},
		{"cron.list", `{}`, false},
		{"cron.logs", `{"limit":10}`, false},
		{"cron.run", `{"index":0}`, false},
		{"cron.run", `{"index":0,"command":"echo pwned"}`, false},
		{"cron.run", `{"command":"echo pwned"}`, true},
		{"cron.run", `{"name":"no-such-task"}`, true},
	}
	results := make(map[string]*pluginapi.Response, len(steps))
	for i, step := range steps {
		response := request(t, conn, fmt.Sprintf("feat-%d", i), step.method, step.params)
		if step.wantErr {
			if response.Error == nil {
				t.Fatalf("%s#%d 应失败，实际 result=%s", step.method, i, response.Result)
			}
			continue
		}
		if response.Error != nil {
			t.Fatalf("%s 失败: %s: %s", step.method, response.Error.Code, response.Error.Message)
		}
		results[step.method] = response
		t.Logf("E2E_JSON %s#%d %s", step.method, i, response.Result)
	}

	// crontab 只应有两行任务（plugin 类型没有写入）。
	content, readErr := os.ReadFile(cronFile)
	if readErr != nil {
		t.Fatalf("读临时 crontab 失败: %v", readErr)
	}
	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	if len(lines) != 5 { // 注释 + 元数据 + 任务，第二条只有元数据 + 任务
		t.Fatalf("crontab 行数不符（plugin 类型不应写入）: %q", content)
	}
	if !strings.Contains(string(content), "curl -sS -m 30 -o /dev/null -X POST") {
		t.Fatalf("http 任务未生成固定 argv curl: %q", content)
	}
	if strings.Count(string(content), "__task-run") != 2 {
		t.Fatalf("两条任务都应套包装命令: %q", content)
	}

	var list croncore.ListResult
	unmarshalResult(t, results["cron.list"], &list)
	if list.Count != 2 || list.Entries[0].Type != croncore.TypeShell || list.Entries[1].Type != croncore.TypeHTTP {
		t.Fatalf("list 类型不符: %+v", list.Entries)
	}

	var logs croncore.LogsResult
	unmarshalResult(t, results["cron.logs"], &logs)
	if !logs.Available || logs.Source != logFile || logs.Count != 2 {
		t.Fatalf("logs 结果不符: %+v", logs)
	}
	if !strings.Contains(logs.Entries[0].Command, "curl") {
		t.Fatalf("logs 应最新在前: %+v", logs.Entries)
	}

	var run croncore.RunResult
	unmarshalResult(t, results["cron.run"], &run)
	if strings.TrimSpace(run.Output) != "hello" || run.ExitCode != 0 || !run.Started {
		t.Fatalf("cron.run 结果不符: %+v", run)
	}
	if run.Command != "echo hello" {
		t.Fatalf("cron.run 应执行 crontab 里的命令: %+v", run)
	}

	shutdown := request(t, conn, "feat-shutdown", "kore.shutdown", `{}`)
	if shutdown.Error != nil {
		t.Fatalf("shutdown 失败: %s", shutdown.Error.Message)
	}
	waitExit(t, process)
}

// unmarshalResult 把 result 载荷解码到目标结构；失败即终止用例。
func unmarshalResult(t *testing.T, response *pluginapi.Response, target any) {
	t.Helper()
	if err := json.Unmarshal(response.Result, target); err != nil {
		t.Fatalf("解析结果失败: %v（原始 %s）", err, response.Result)
	}
}
