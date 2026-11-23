package croncore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// RunTaskMode 是二进制的包装执行模式（`cron __task-run <log> <name> <command>`）。
//
// 它由 crontab 里的包装命令触发，不连接内核、不参与协议会话；执行结束把一条
// 结构化记录追加到 runlog 后，按任务真实退出码退出（供 cron 邮件/审计判读）。
// handled=false 表示不是该模式，main 继续走正常插件启动流程。
func RunTaskMode(args []string, stdout, stderr io.Writer) (int, bool) {
	if len(args) == 0 || args[0] != taskRunMode {
		return 0, false
	}
	if len(args) != 4 {
		fmt.Fprintln(stderr, "cron __task-run: 参数必须是 <runlog-path> <task-name> <command>")
		return 2, true
	}
	logPath, name, command := args[1], args[2], args[3]
	if err := validateTaskRunArgs(logPath, name, command); err != nil {
		fmt.Fprintln(stderr, "cron __task-run: "+err.Error())
		fmt.Fprintln(stderr, "提示：该模式只由 cron.add 生成的包装命令调用，请勿手工改写 crontab 中的包装参数")
		return 2, true
	}

	started := time.Now()
	recorder := &runOutput{headLimit: outputHeadBytes}
	ctx, cancel := context.WithTimeout(context.Background(), taskRunTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, runShell, "-c", command)
	prepareRunCommand(cmd)
	cmd.WaitDelay = 2 * time.Second
	cmd.Stdout = io.MultiWriter(stdout, recorder)
	cmd.Stderr = io.MultiWriter(stderr, recorder)
	err := cmd.Run()
	duration := time.Since(started)

	exitCode := 0
	timedOut := false
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 127
			fmt.Fprintln(stderr, "cron __task-run: 启动任务进程失败: "+err.Error())
		}
	}
	if ctx.Err() != nil {
		timedOut = true
		exitCode = 124
	}
	entry := RunlogEntry{
		TaskName: name, StartedAt: started.UTC().Format(time.RFC3339),
		DurationMS: duration.Milliseconds(), ExitCode: exitCode,
		OutputHead: recorder.head.String(), OutputBytes: recorder.total,
		Truncated: recorder.total > int64(recorder.head.Len()),
		Mode:      "scheduled", TimedOut: timedOut,
	}
	if logErr := appendRunLog(logPath, entry); logErr != nil {
		// 日志失败不能改变任务本身的退出码；如实报到 stderr，避免静默丢记录。
		fmt.Fprintln(stderr, "cron __task-run: 写执行日志失败: "+logErr.Error())
	}
	return exitCode, true
}

// validateTaskRunArgs 校验包装参数。日志路径必须是本插件约定的绝对路径，
// 防止有人通过改写 crontab 把插件当任意文件写入器使用。
func validateTaskRunArgs(logPath, name, command string) error {
	if !filepath.IsAbs(logPath) || filepath.Base(logPath) != runlogFileName {
		return errors.New("runlog 路径非法（必须是绝对路径且以 " + runlogFileName + " 结尾）")
	}
	if strings.TrimSpace(name) == "" || len(name) > wrapperNameMaxLen*4 {
		return errors.New("任务名非法")
	}
	if strings.TrimSpace(command) == "" || len(command) > maxCommandLen {
		return errors.New("任务命令为空或超过 " + itoa(maxCommandLen) + " 字节")
	}
	return nil
}

// runOutput 是执行输出记录器：保留前 headLimit 字节作为 output_head，
// 同时统计全部输出字节数；超出部分继续消费（不阻塞子进程）但不保存。
type runOutput struct {
	head      bytes.Buffer
	headLimit int
	total     int64
}

// Write 实现 io.Writer。
func (r *runOutput) Write(data []byte) (int, error) {
	r.total += int64(len(data))
	if room := r.headLimit - r.head.Len(); room > 0 {
		if len(data) > room {
			r.head.Write(data[:room])
		} else {
			r.head.Write(data)
		}
	}
	return len(data), nil
}
