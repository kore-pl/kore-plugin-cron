package e2e

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/pengtang1024/kore-plugin-cron/internal/cronfake"
	"github.com/pengtang1024/kore/pkg/pluginapi"
)

func buildPlugin(t *testing.T, dir string) string {
	t.Helper()
	binary := filepath.Join(dir, "cron-plugin")
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Dir = "../.."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("构建插件失败: %v\n%s", err, out)
	}
	return binary
}

func startPlugin(t *testing.T, binary, dir, socket string, fake *cronfake.State) (*exec.Cmd, *bytes.Buffer) {
	t.Helper()
	stderr := &bytes.Buffer{}
	process := exec.Command(binary)
	process.Env = append(os.Environ(),
		"KORE_PLUGIN_SOCK="+socket,
		"KORE_PLUGIN_ID=cron",
		"KORE_PLUGIN_TOKEN=e2e",
		"KORE_CRON_BACKUP_DIR="+filepath.Join(dir, "backup"),
		"KORE_STATE_DIR="+dir,
		"PATH="+fake.Dir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	process.Stdout = io.Discard
	process.Stderr = stderr
	if err := process.Start(); err != nil {
		t.Fatalf("启动插件失败: %v", err)
	}
	t.Cleanup(func() {
		if process.ProcessState == nil {
			_ = process.Process.Kill()
			_, _ = process.Process.Wait()
		}
	})
	return process, stderr
}

func handshake(t *testing.T, conn net.Conn) {
	t.Helper()
	request := readRequest(t, conn)
	if request.Method != "kore.hello" {
		t.Fatalf("首帧应为 kore.hello，实际 %s", request.Method)
	}
	response := &pluginapi.Response{
		Type: pluginapi.TypeResponse, ID: request.ID, ReqID: request.ReqID,
		Result: json.RawMessage(`{"ok":true,"protocol":"kore.plugin.v1"}`),
	}
	if err := pluginapi.WriteJSON(conn, response); err != nil {
		t.Fatalf("回写 hello 失败: %v", err)
	}
}

func request(t *testing.T, conn net.Conn, id, method, params string) *pluginapi.Response {
	t.Helper()
	req := &pluginapi.Request{
		Type: pluginapi.TypeRequest, ID: id, Method: method, Params: json.RawMessage(params),
	}
	if err := pluginapi.WriteJSON(conn, req); err != nil {
		t.Fatalf("发送 %s 失败: %v", method, err)
	}
	return readResponse(t, conn)
}

func readRequest(t *testing.T, conn net.Conn) *pluginapi.Request {
	t.Helper()
	env, err := pluginapi.DecodeFrame(readPayload(t, conn))
	if err != nil {
		t.Fatalf("解码请求失败: %v", err)
	}
	request, ok := env.(*pluginapi.Request)
	if !ok {
		t.Fatalf("期望 request，实际 %T", env)
	}
	return request
}

func readResponse(t *testing.T, conn net.Conn) *pluginapi.Response {
	t.Helper()
	env, err := pluginapi.DecodeFrame(readPayload(t, conn))
	if err != nil {
		t.Fatalf("解码响应失败: %v", err)
	}
	response, ok := env.(*pluginapi.Response)
	if !ok {
		t.Fatalf("期望 response，实际 %T", env)
	}
	return response
}

func readPayload(t *testing.T, conn net.Conn) []byte {
	t.Helper()
	payload, err := pluginapi.ReadFrame(conn, pluginapi.MaxFrameSize)
	if err != nil {
		t.Fatalf("读帧失败: %v", err)
	}
	return payload
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	return data
}

func waitExit(t *testing.T, process *exec.Cmd) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- process.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("插件退出异常: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("插件未在 5s 内退出")
	}
}
