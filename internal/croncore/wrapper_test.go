package croncore

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestWrapperCommandQuotesUserInput 验证用户命令/任务名作为单个 argv 传给 wrapper：
// 命令里的单引号、分号、$ 不会改变参数结构，更不会改写 wrapper 路径或日志路径。
func TestWrapperCommandQuotesUserInput(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "argv.txt")
	stub := filepath.Join(dir, "stub.sh")
	script := "#!/bin/sh\nprintf '%s\\n' \"$#\" \"$1\" \"$2\" \"$3\" \"$4\" > '" + out + "'\n"
	if err := os.WriteFile(stub, []byte(script), 0o700); err != nil {
		t.Fatalf("写 stub 失败: %v", err)
	}
	name := "night'ly; $(touch /tmp/nope)"
	command := "echo 'hello'; echo \"$HOME\"; printf 'a b'"
	logPath := filepath.Join(dir, "runlog", runlogFileName)
	line := wrapperCommand(stub, logPath, name, command)

	if output, err := exec.Command("/bin/sh", "-c", line).CombinedOutput(); err != nil {
		t.Fatalf("执行包装命令失败: %v (%s)", err, output)
	}
	record, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("读取 stub 记录失败: %v", err)
	}
	want := "4\n" + taskRunMode + "\n" + logPath + "\n" + name + "\n" + command + "\n"
	if string(record) != want {
		t.Fatalf("argv 结构被用户输入改变:\n got=%q\nwant=%q", record, want)
	}
}

// TestBuildWrappedTaskFallsBackWhenTooLong：生成行超过 cron 行上限时明确退化。
func TestBuildWrappedTaskFallsBackWhenTooLong(t *testing.T) {
	t.Setenv("KORE_CRON_RUNLOG_DIR", t.TempDir())
	long := strings.Repeat("\t", maxCommandLen-1) + "x"
	_, _, name, ok := buildWrappedTask("/bin/true", "0 3 * * *", taskSpec{typ: TypeShell, command: long}, "")
	if ok {
		t.Fatal("超长命令不应声称已包装")
	}
	if name == "" {
		t.Fatal("即使退化也应给出任务名")
	}
}
