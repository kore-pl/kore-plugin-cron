package boundary_test

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/pengtang1024/kore-plugin-cron/internal/croncore"
)

// 资源耗尽（判据① 第 4 类）：必须真实注入，不接受进程内 FaultDiskFull 旗标。
//
// 注入方式：把工作进程的 RLIMIT_FSIZE 设为 0（POSIX `ulimit -f 0`），让内核在
// 写普通文件时返回 EFBIG（"file too large"）。这与真实 ENOSPC（磁盘满）落在
// 完全相同的 write(2) 失败路径：writeCrontabFile 的 WriteString 报错 → cleanup
// → commit 回滚。断言的是插件在这条路径上的真实行为，而不是一个 if 分支。
//
// 为什么不用 /dev/full：Add 先 read 再 write，而 KORE_CRON_FILE=/dev/full 会
// 在读取阶段无限读零（os.ReadFile 永不 EOF），且原子写的临时文件必须落在
// 目标目录——非 root 无法在 /dev 建临时文件、rename 也不能覆盖设备节点。
// 因此 /dev/full 不能作为本插件的 crontab 路径；用 ulimit -f 才是同一失败路径。
const writeHelperEnv = "KORE_BOUNDARY_WRITE_HELPER"

// TestBoundaryWriteHelperProcess 只作为子进程被 resource 用例重新执行，
// 不参与常规 `go test` 断言（父进程通过 stderr 上的结构化标记取证）。
func TestBoundaryWriteHelperProcess(t *testing.T) {
	if os.Getenv(writeHelperEnv) != "1" {
		t.Skip("仅由资源耗尽用例以子进程方式调用")
	}
	store := croncore.NewStore()
	_, err := store.Add(ctx(), croncore.AddParams{Schedule: "0 3 * * *", Command: "/bin/true"})
	fmt.Fprintf(os.Stderr, "BOUNDARY_HELPER_IS_IO=%t\n", errors.Is(err, croncore.ErrIO))
	fmt.Fprintf(os.Stderr, "BOUNDARY_HELPER_ERR=%v\n", err)
}

// TestResourceExhaustionCrontabWrite：旧内容为空时，先通过备份写（0 字节），
// 再在写 crontab 时撞上文件大小限额 → ErrIO + 已回滚 + 目标文件回到空内容。
func TestResourceExhaustionCrontabWrite(t *testing.T) {
	root := t.TempDir()
	cronFile := filepath.Join(root, "crontab") // 不存在等同"没有 crontab"
	backupDir := filepath.Join(root, "backup")

	out := runWriteHelperUnderFileLimit(t, cronFile, backupDir)
	if !strings.Contains(out, "BOUNDARY_HELPER_IS_IO=true") {
		t.Fatalf("真实 EFBIG 必须映射为 ErrIO，helper 输出:\n%s", out)
	}
	errLine := helperErrorLine(out)
	if !strings.Contains(errLine, "file too large") {
		t.Fatalf("错误必须来自真实内核限制（file too large），实际: %s", errLine)
	}
	if !strings.Contains(errLine, "回滚") || strings.Contains(errLine, "回滚失败") {
		t.Fatalf("写 crontab 失败后必须成功回滚，实际: %s", errLine)
	}

	info, err := os.Stat(cronFile)
	if err != nil {
		t.Fatalf("回滚应重建空 crontab 文件，实际 stat 失败: %v", err)
	}
	if info.Size() != 0 {
		t.Fatalf("写入失败后不得留下半截 crontab，实际 %d 字节", info.Size())
	}
	backups := dirEntries(t, backupDir)
	if len(backups) != 1 {
		t.Fatalf("回滚前应先留下 1 份备份，实际 %v", backups)
	}
	backupInfo, err := os.Stat(filepath.Join(backupDir, backups[0]))
	if err != nil || backupInfo.Size() != 0 {
		t.Fatalf("备份内容应为空旧内容，实际 size=%v err=%v", backupInfo, err)
	}
}

// TestResourceExhaustionBackupWrite：旧内容非空时，限额先打在备份写上——
// 必须返回 ErrIO，且原 crontab 一个字节都不能变（宁可不写，不能只写一半）。
func TestResourceExhaustionBackupWrite(t *testing.T) {
	root := t.TempDir()
	cronFile := filepath.Join(root, "crontab")
	backupDir := filepath.Join(root, "backup")
	const seed = "0 1 * * * /bin/one\n"
	seedCrontab(t, cronFile, seed)

	out := runWriteHelperUnderFileLimit(t, cronFile, backupDir)
	if !strings.Contains(out, "BOUNDARY_HELPER_IS_IO=true") {
		t.Fatalf("写备份时的真实 EFBIG 必须映射为 ErrIO，helper 输出:\n%s", out)
	}
	errLine := helperErrorLine(out)
	if !strings.Contains(errLine, "写备份文件失败") {
		t.Fatalf("限额应先打在备份写上，实际: %s", errLine)
	}
	if !strings.Contains(errLine, "file too large") {
		t.Fatalf("错误必须来自真实内核限制（file too large），实际: %s", errLine)
	}
	if got := string(readCrontabBytes(t, cronFile)); got != seed {
		t.Fatalf("备份写失败时 crontab 不得改动:\n期望 %q\n实际 %q", seed, got)
	}
	if backups := dirEntries(t, backupDir); len(backups) != 0 {
		t.Fatalf("写失败的备份不得残留，实际 %v", backups)
	}
}

// runWriteHelperUnderFileLimit 用 `sh -c 'ulimit -f 0; exec <test-bin> -test.run ...'`
// 重新执行本测试二进制，把 RLIMIT_FSIZE=0 精确注入到真正执行写盘的那个进程。
func runWriteHelperUnderFileLimit(t *testing.T, cronFile, backupDir string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("ulimit -f 是 POSIX shell 能力，Windows 不做该注入（Tier B 降级）")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("取测试二进制路径失败: %v", err)
	}
	script := `ulimit -f 0; exec "$0" -test.run '^TestBoundaryWriteHelperProcess$' -test.timeout=120s`
	cmd := exec.Command("sh", "-c", script, exe)
	cmd.Env = append(envWithout("KORE_CRON_FILE", "KORE_CRON_BACKUP_DIR", "KORE_CRON_RUNLOG_DIR", "KORE_STATE_DIR", writeHelperEnv),
		writeHelperEnv+"=1",
		"KORE_CRON_FILE="+cronFile,
		"KORE_CRON_BACKUP_DIR="+backupDir,
		"KORE_CRON_RUNLOG_DIR="+filepath.Join(filepath.Dir(cronFile), "runlog"),
		"KORE_STATE_DIR="+filepath.Join(filepath.Dir(cronFile), "state"),
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("RLIMIT_FSIZE 子进程执行失败: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	return stderr.String()
}

// envWithout 过滤掉指定环境变量，避免父进程同名变量覆盖子进程注入值。
func envWithout(keys ...string) []string {
	blocked := make(map[string]bool, len(keys))
	for _, key := range keys {
		blocked[key] = true
	}
	env := make([]string, 0, len(os.Environ()))
	for _, item := range os.Environ() {
		name := item
		if idx := strings.IndexByte(item, '='); idx >= 0 {
			name = item[:idx]
		}
		if blocked[name] {
			continue
		}
		env = append(env, item)
	}
	return env
}

// helperErrorLine 取 helper 打印的错误行；缺失时返回空串让断言给出明确失败。
func helperErrorLine(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "BOUNDARY_HELPER_ERR=") {
			return strings.TrimPrefix(line, "BOUNDARY_HELPER_ERR=")
		}
	}
	return ""
}
