package croncore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	// crontabBinary 是系统 crontab 命令；测试通过 PATH 前置假二进制隔离。
	crontabBinary = "crontab"
	// backupDirName 是 <state> 下的备份子目录名。
	backupDirName = "cron-backup"
	// rollbackTimeout 是写失败后回滚的独立预算，不受已取消的请求上下文影响。
	rollbackTimeout = 10 * time.Second
)

// readCrontab 执行 `crontab -l`。无 crontab 时命令以 exit 1 返回
// "no crontab for <user>"，按空内容处理；其它失败统一映射为 ErrIO。
func (s *Store) readCrontab(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, crontabBinary, "-l")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return stdout.String(), nil
	}
	if ctx.Err() != nil {
		return "", wrap(ErrTimeout, "crontab -l 超时: "+ctx.Err().Error())
	}
	combined := stdout.String() + stderr.String()
	if isNoCrontab(combined) {
		return "", nil
	}
	detail := strings.TrimSpace(stderr.String())
	if detail == "" {
		detail = err.Error()
	}
	if isPermissionError(combined) {
		return "", wrap(ErrNoPermission, "crontab -l 权限不足: "+detail)
	}
	return "", wrap(ErrIO, "crontab -l 失败: "+detail)
}

// isNoCrontab 识别“当前用户没有 crontab”的提示。
func isNoCrontab(output string) bool {
	return strings.Contains(strings.ToLower(output), "no crontab for")
}

// isPermissionError 识别权限类错误（英文 locale 与中文 locale 都要覆盖）。
// 典型原文：`crontab: permission denied` / `fopen: 权限不够`。
func isPermissionError(output string) bool {
	text := strings.ToLower(output)
	for _, marker := range []string{
		"permission denied", "operation not permitted", "not permitted",
		"权限不够", "权限不足", "没有权限", "无权访问",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

// writeCrontab 以 `crontab -` 从 stdin 写入整份内容；系统 crontab 负责替换
// 与落盘，插件不直接触碰 /var/spool/cron。
func (s *Store) writeCrontab(ctx context.Context, content string) error {
	cmd := exec.CommandContext(ctx, crontabBinary, "-")
	cmd.Stdin = strings.NewReader(content)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return wrap(ErrTimeout, "crontab - 写入超时: "+ctx.Err().Error())
		}
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return wrap(ErrIO, "crontab - 写入失败: "+detail)
	}
	return nil
}

// saveBackup 把旧内容写入带时间戳的备份文件，返回备份路径。
// 文件名纳秒级且用 O_EXCL 创建，避免并发或同一时刻覆盖已有备份。
func (s *Store) saveBackup(content string) (string, error) {
	dir := s.backupDirPath()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", wrap(ErrIO, "创建备份目录失败: "+err.Error())
	}
	stamp := time.Now().UTC().Format("20060102T150405.000000000Z")
	for attempt := 0; attempt < 10; attempt++ {
		name := "crontab-" + stamp
		if attempt > 0 {
			name += "-" + itoa(attempt)
		}
		path := filepath.Join(dir, name+".txt")
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			if errors.Is(err, os.ErrExist) {
				continue
			}
			return "", wrap(ErrIO, "创建备份文件失败: "+err.Error())
		}
		_, writeErr := file.WriteString(content)
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			_ = os.Remove(path) // 不留下半截备份
		}
		if writeErr != nil {
			return "", wrap(ErrIO, "写备份文件失败: "+writeErr.Error())
		}
		if closeErr != nil {
			return "", wrap(ErrIO, "关闭备份文件失败: "+closeErr.Error())
		}
		return path, nil
	}
	return "", wrap(ErrIO, "备份文件名冲突: "+stamp)
}

// commit 是唯一的写路径：先备份旧内容，再整份写回；写失败用独立预算回滚，
// 并把备份路径与失败原因一起返回。
func (s *Store) commit(ctx context.Context, oldContent, newContent string) (string, error) {
	path, err := s.saveBackup(oldContent)
	if err != nil {
		return "", err
	}
	if err := s.writeCrontab(ctx, newContent); err != nil {
		rollbackCtx, cancel := context.WithTimeout(context.Background(), rollbackTimeout)
		defer cancel()
		if rerr := s.writeCrontab(rollbackCtx, oldContent); rerr != nil {
			return path, wrap(ErrIO, fmt.Sprintf("%v；从备份 %s 回滚失败: %v", err, path, rerr))
		}
		return path, wrap(ErrIO, fmt.Sprintf("%v；已从备份回滚", err))
	}
	return path, nil
}
