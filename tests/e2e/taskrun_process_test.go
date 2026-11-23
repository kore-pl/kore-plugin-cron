// 包装执行模式端到端：真实二进制 `__task-run` 的执行、退出码透传与 runlog 追加。
package e2e

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pengtang1024/kore-plugin-cron/internal/croncore"
)

func TestTaskRunModeE2E(t *testing.T) {
	tmp := t.TempDir()
	binary := buildPlugin(t, tmp)
	logPath := filepath.Join(tmp, "runlog", "runlog.jsonl")
	name := "night'ly; $(touch /tmp/nope)"
	command := `echo "it's ok"; printf 'a b'; exit 7`

	cmd := exec.Command(binary, "__task-run", logPath, name, command)
	output, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 7 {
		t.Fatalf("退出码应透传为 7，实际 err=%v output=%s", err, output)
	}
	if !strings.Contains(string(output), "it's ok") {
		t.Fatalf("命令输出应透传到 stdout/stderr: %s", output)
	}

	data, readErr := os.ReadFile(logPath)
	if readErr != nil {
		t.Fatalf("读 runlog 失败: %v", readErr)
	}
	var entry croncore.RunlogEntry
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &entry); err != nil {
		t.Fatalf("解析 runlog 失败: %v (%s)", err, data)
	}
	if entry.TaskName != name || entry.ExitCode != 7 || entry.Mode != "scheduled" ||
		!strings.Contains(entry.OutputHead, "it's ok") || entry.OutputBytes == 0 {
		t.Fatalf("runlog 记录不符: %+v", entry)
	}
	info, _ := os.Stat(logPath)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("runlog 权限应为 0600，实际 %o", info.Mode().Perm())
	}

	// 非法日志路径必须拒绝，且不创建文件。
	bad := exec.Command(binary, "__task-run", filepath.Join(tmp, "evil.txt"), "x", "echo hi")
	if err := bad.Run(); err == nil {
		t.Fatal("非 runlog.jsonl 路径应被拒绝")
	}
	if _, statErr := os.Stat(filepath.Join(tmp, "evil.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("拒绝路径不得被创建: %v", statErr)
	}
}
