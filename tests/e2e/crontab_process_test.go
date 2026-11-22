// Package e2e 用真实插件进程跑 crontab 生命周期：进程经 UDS 完成 kore.hello
// 握手后，cron.* 请求全部落到 PATH 前置的假 crontab，不触碰本机真实 crontab。
package e2e

import (
	"fmt"
	"net"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/pengtang1024/kore-plugin-cron/internal/cronfake"
	"github.com/pengtang1024/kore/pkg/pluginapi"
)

func TestCrontabProcessE2E(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("UDS 端到端仅类 Unix")
	}
	tmp := t.TempDir()
	fake, err := cronfake.Install(tmp)
	if err != nil {
		t.Fatalf("安装假 crontab 失败: %v", err)
	}
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
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	handshake(t, conn)

	steps := []struct {
		method  string
		params  string
		wantErr bool
	}{
		{"cron.add", `{"schedule":"0 3 * * *","command":"/usr/bin/backup --full","comment":"nightly backup"}`, false},
		{"cron.add", `{"schedule":"*/5 * * * *","command":"/bin/echo tick"}`, false},
		{"cron.list", `{}`, false},
		{"cron.remove", `{"index":0}`, false},
		{"cron.list", `{}`, false},
		{"cron.replace", `{"index":0,"schedule":"10 2 * * *","command":"/bin/echo replaced"}`, false},
		{"cron.list", `{}`, false},
		{"cron.add", `{"schedule":"0 4 * * *","command":"echo one\necho injected"}`, true},
	}
	for i, step := range steps {
		response := request(t, conn, fmt.Sprintf("e2e-%d", i), step.method, step.params)
		if step.wantErr {
			if response.Error == nil || response.Error.Code != pluginapi.E_INVALID {
				t.Fatalf("%s 应返回 E_INVALID: %+v", step.method, response)
			}
		} else if response.Error != nil {
			t.Fatalf("%s 失败: %s", step.method, response.Error.Message)
		}
		payload := response.Result
		if response.Error != nil {
			payload = mustJSON(t, response.Error)
		}
		t.Logf("E2E_PROCESS_JSON %s %s", step.method, payload)
	}

	content, _ := fake.Content()
	if content != "10 2 * * * /bin/echo replaced\n" {
		t.Fatalf("最终 crontab 不符: %q", content)
	}

	response := request(t, conn, "e2e-shutdown", "kore.shutdown", `{}`)
	if response.Error != nil {
		t.Fatalf("shutdown 失败: %s", response.Error.Message)
	}
	waitExit(t, process)
}
