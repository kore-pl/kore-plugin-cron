package croncore

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"
)

// 手动触发的边界：只跑 crontab 里已存在的命令，固定 /bin/sh -c，
// 输出最多保留 64 KiB（避免一条刷屏命令把响应撑爆）。
const (
	maxRunOutputBytes = 64 << 10
	// runShell 是执行 crontab 命令的 shell：crontab 本身按 /bin/sh -c 执行，
	// 手动触发必须与系统 cron 一致，否则"手动成功、定时失败"。
	runShell = "/bin/sh"
)

// Run 同步执行一条**已注册**任务（cron.run）。
//
// 安全边界：执行目标只从 crontab 解析结果里取，入参只接受 index/name 定位符，
// 不接受任意命令；找不到或匹配到多条都返回错误，绝不"猜一个执行"。
func (s *Store) Run(ctx context.Context, params RunParams) (*RunResult, error) {
	if err := s.preflight(ctx, false); err != nil {
		return nil, err
	}
	content, err := s.readCrontab(ctx)
	if err != nil {
		return nil, err
	}
	entries := parseCrontab(content).result()
	target, err := resolveRunTarget(entries, params)
	if err != nil {
		return nil, err
	}
	// 停用任务的定义还在 crontab 里，但它是注释行——系统不会执行它，
	// 立即执行也不许绕过启停开关（否则"停用"形同虚设）。
	if !target.Enabled {
		return nil, wrap(ErrDisabled, "任务 index "+itoa(target.Index)+" 已停用，请先调用 cron.enable 恢复后再执行")
	}
	if !s.runMu.TryLock() {
		return nil, wrap(ErrBusy, "已有任务正在手动执行，请等它结束再试")
	}
	defer s.runMu.Unlock()
	return executeEntry(ctx, target)
}

// resolveRunTarget 按 index 优先、name 次之定位任务；name 匹配备注或命令。
func resolveRunTarget(entries []Entry, params RunParams) (Entry, error) {
	if len(entries) == 0 {
		return Entry{}, wrap(ErrNotFound, "当前 crontab 没有可执行任务")
	}
	if params.Index != nil {
		index := *params.Index
		if index < 0 || index >= len(entries) {
			return Entry{}, wrap(ErrInvalidParam, "index 越界: "+itoa(index)+"，共 "+itoa(len(entries))+" 条")
		}
		return entries[index], nil
	}
	name := strings.TrimSpace(params.Name)
	if name == "" {
		return Entry{}, wrap(ErrInvalidParam, "index 或 name 必填")
	}
	lower := strings.ToLower(name)
	matched := make([]Entry, 0, 2)
	for _, entry := range entries {
		if strings.Contains(strings.ToLower(entry.Comment), lower) ||
			strings.Contains(strings.ToLower(entry.Command), lower) {
			matched = append(matched, entry)
		}
	}
	switch len(matched) {
	case 0:
		return Entry{}, wrap(ErrNotFound, "没有匹配 name="+name+" 的任务（只按已注册任务的备注或命令匹配）")
	case 1:
		return matched[0], nil
	default:
		indexes := make([]string, 0, len(matched))
		for _, entry := range matched {
			indexes = append(indexes, itoa(entry.Index))
		}
		return Entry{}, wrap(ErrInvalidParam, "name="+name+" 匹配到 "+itoa(len(matched))+" 条任务（index "+strings.Join(indexes, ", ")+"），请改用 index 精确指定")
	}
}

// executeEntry 用 /bin/sh -c 执行任务命令并汇总结果。
//
// 超时不返回 error：调用方需要看到已产生的部分输出与 timed_out 标记，
// 因此统一放进 RunResult，由界面展示（错误信息会丢掉输出，等于白跑）。
func executeEntry(ctx context.Context, entry Entry) (*RunResult, error) {
	started := time.Now()
	buffer := &limitedBuffer{limit: maxRunOutputBytes}
	cmd := exec.CommandContext(ctx, runShell, "-c", entry.Command)
	// 超时后必须连同子进程一起杀掉：只杀 /bin/sh 时，命令里的 sleep/curl 仍持有
	// 输出管道，cmd.Run 会一直等到子进程自己退出（实测 5s 命令拖满 5s）。
	prepareRunCommand(cmd)
	// WaitDelay 兜底：即使有孙进程拒绝退出，也在 2s 后关闭管道返回。
	cmd.WaitDelay = 2 * time.Second
	cmd.Stdout = buffer
	cmd.Stderr = buffer
	err := cmd.Run()
	duration := time.Since(started)

	result := &RunResult{
		Index: entry.Index, Schedule: entry.Schedule, Command: entry.Command,
		Started: true, Output: buffer.String(), ExitCode: 0,
		DurationMS: duration.Milliseconds(),
	}
	if err == nil {
		return result, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
	} else {
		// 进程没起来（shell 缺失等）：这不是"命令失败"，如实返回 I/O 错误。
		return nil, wrap(ErrIO, "启动任务进程失败: "+err.Error())
	}
	if ctx.Err() != nil {
		result.TimedOut = true
		if result.ExitCode == 0 {
			result.ExitCode = -1
		}
	}
	return result, nil
}

// limitedBuffer 是带上限的字节缓冲：超限后继续消费但丢弃多余输出，
// 避免子进程因管道写满而阻塞。
type limitedBuffer struct {
	buffer bytes.Buffer
	limit  int
}

// Write 实现 io.Writer；超限部分静默丢弃（保留前 limit 字节）。
func (b *limitedBuffer) Write(data []byte) (int, error) {
	remaining := b.limit - b.buffer.Len()
	if remaining > 0 {
		if len(data) > remaining {
			b.buffer.Write(data[:remaining])
		} else {
			b.buffer.Write(data)
		}
	}
	return len(data), nil
}

// String 返回已保留的输出文本。
func (b *limitedBuffer) String() string { return b.buffer.String() }
