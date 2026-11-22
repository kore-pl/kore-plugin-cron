// Package cronfake 在测试中安装一个假的 crontab 可执行文件：测试把它的目录
// 前置到 PATH，所有 `crontab -l` / `crontab -` 都落到临时状态文件，不触碰
// 本机真实用户 crontab。仅测试导入，不进入插件二进制。
package cronfake

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// State 是一次假 crontab 安装的路径集合。
type State struct {
	Dir       string
	statePath string
	failPath  string
}

const scriptTemplate = `#!/bin/sh
STATE='%s'
FAIL='%s'
case "${1:-}" in
  -l)
    if [ ! -f "$STATE" ]; then
      echo "no crontab for $(id -un)" >&2
      exit 1
    fi
    cat "$STATE"
    exit 0
    ;;
  -)
    if [ -f "$FAIL" ]; then
      rm -f "$FAIL"
      echo "fake crontab: simulated write failure" >&2
      exit 1
    fi
    cat > "$STATE"
    exit 0
    ;;
  -r)
    rm -f "$STATE"
    exit 0
    ;;
  *)
    echo "fake crontab: unsupported args: $*" >&2
    exit 2
    ;;
esac
`

// Install 在 dir 下写入假 crontab 脚本，返回可操作的状态句柄。
func Install(dir string) (*State, error) {
	state := &State{
		Dir:       dir,
		statePath: filepath.Join(dir, "crontab.state"),
		failPath:  filepath.Join(dir, "fail-once"),
	}
	script := []byte(fmt.Sprintf(scriptTemplate, state.statePath, state.failPath))
	if err := os.WriteFile(filepath.Join(dir, "crontab"), script, 0o700); err != nil {
		return nil, err
	}
	return state, nil
}

// ApplyEnv 把假 crontab 前置到 PATH，并把备份目录指向临时目录。
func (s *State) ApplyEnv() {
	os.Setenv("PATH", s.Dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	os.Setenv("KORE_CRON_BACKUP_DIR", filepath.Join(s.Dir, "cron-backup"))
}

// Use 为单个测试注入 PATH 与备份目录，测试结束自动恢复。
func (s *State) Use(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", s.Dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("KORE_CRON_BACKUP_DIR", filepath.Join(s.Dir, "cron-backup"))
}

// Seed 预置 crontab 内容，模拟当前用户已有任务。
func (s *State) Seed(content string) error {
	return os.WriteFile(s.statePath, []byte(content), 0o600)
}

// Content 读取当前假 crontab 内容；文件不存在视为空。
func (s *State) Content() (string, error) {
	data, err := os.ReadFile(s.statePath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(data), nil
}

// Reset 清空假 crontab 状态，模拟当前用户从未创建过 crontab。
func (s *State) Reset() error {
	if err := os.Remove(s.statePath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// FailNextWrite 让下一次 `crontab -` 失败一次，用于验证回滚。
func (s *State) FailNextWrite() error {
	return os.WriteFile(s.failPath, []byte("1"), 0o600)
}

// BackupDir 返回注入的备份目录路径。
func (s *State) BackupDir() string { return filepath.Join(s.Dir, "cron-backup") }
