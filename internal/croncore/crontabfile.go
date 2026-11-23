package croncore

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// KORE_CRON_FILE 让插件把"系统 crontab"换成一份普通文件：readCrontab /
// writeCrontab 直接读写该文件，完全不再调用 crontab 命令。
//
// 用途只有一个：隔离验证——在 /tmp 里建一份临时 crontab，验证增删/日志/立即执行
// 而绝不碰当前用户真实 crontab（G-29 运行态写边界）。生产部署不设置该变量时
// 行为与旧版本完全一致（仍走 `crontab -l` / `crontab -`）。
func crontabFilePath() string {
	return strings.TrimSpace(os.Getenv("KORE_CRON_FILE"))
}

// readCrontabFile 读 KORE_CRON_FILE；文件不存在等同"没有 crontab"。
func readCrontabFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		if errors.Is(err, os.ErrPermission) {
			return "", wrap(ErrNoPermission, "读取 "+path+" 权限不足")
		}
		return "", wrap(ErrIO, "读取 "+path+" 失败: "+err.Error())
	}
	return string(data), nil
}

// writeCrontabFile 原子写 KORE_CRON_FILE：同目录临时文件写完再 rename，
// 避免写一半被读或断电留下半截内容。
func writeCrontabFile(path, content string) error {
	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, ".cron-*.tmp")
	if err != nil {
		return wrap(ErrIO, "创建临时 crontab 文件失败: "+err.Error())
	}
	tempName := temp.Name()
	cleanup := func() { _ = os.Remove(tempName) }
	if _, err := temp.WriteString(content); err != nil {
		_ = temp.Close()
		cleanup()
		return wrap(ErrIO, "写临时 crontab 文件失败: "+err.Error())
	}
	if err := temp.Close(); err != nil {
		cleanup()
		return wrap(ErrIO, "关闭临时 crontab 文件失败: "+err.Error())
	}
	if err := os.Chmod(tempName, 0o600); err != nil {
		cleanup()
		return wrap(ErrIO, "设置临时 crontab 文件权限失败: "+err.Error())
	}
	if err := os.Rename(tempName, path); err != nil {
		cleanup()
		return wrap(ErrIO, "替换 crontab 文件失败: "+err.Error())
	}
	return nil
}
