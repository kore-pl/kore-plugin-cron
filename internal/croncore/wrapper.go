package croncore

import (
	"os"
	"strings"
	"unicode/utf8"
)

// wrapper 任务的生成边界。
//
// wrapper 就是插件自己的二进制：路径来自 os.Executable()，用户无法指定；
// 日志路径由状态目录解析，用户无法指定。用户能控制的只有"要执行的命令"，
// 它作为单个 argv 传给 wrapper，再由 /bin/sh -c 执行（与系统 cron / cron.run 同语义）。
const (
	// taskRunMode 是二进制的隐藏子命令；不是协议方法，不经过内核。
	taskRunMode = "__task-run"
	// maxWrappedLineLen 是生成的 crontab 行 / 元数据行的长度上限。
	// 常见 cron 实现把过长的行视为错误或截断；超过上限时退化为不包装的普通任务。
	maxWrappedLineLen = 900
	// wrapperNameMaxLen 是 runlog 里 task_name 的最大字节数（按 rune 边界截断）。
	wrapperNameMaxLen = 64
)

// wrapperCommand 生成定时执行的包装命令：
//
//	<plugin-bin> __task-run <runlog-path> <task-name> <command>
//
// 四个参数都做单引号转义，因此命令里的空格、分号、$、反引号、单引号
// 都不会改变 argv 结构——它们只会原样成为 wrapper 的第 4 个参数，
// 再由 wrapper 交给 /bin/sh -c 执行。
func wrapperCommand(bin, logPath, name, command string) string {
	return shellQuote(bin) + " " + taskRunMode + " " +
		shellQuote(logPath) + " " + shellQuote(name) + " " + shellQuote(command)
}

// deriveTaskName 计算 runlog 的 task_name：优先用用户备注，其次用命令。
func deriveTaskName(comment, command string) string {
	name := strings.TrimSpace(comment)
	if name == "" {
		name = strings.TrimSpace(command)
	}
	return truncateRunes(name, wrapperNameMaxLen)
}

// truncateRunes 按 rune 边界截断，避免把 UTF-8 字符切成半个。
func truncateRunes(value string, max int) string {
	if len(value) <= max {
		return value
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut]
}

// buildWrappedTask 生成包装后的任务行与元数据行。
//
// ok=false 表示包装不可用（取不到自身路径或生成的行过长），调用方应退化为
// 原始命令（任务仍能执行，只是没有逐任务执行日志），并在结果里如实说明。
func buildWrappedTask(schedule string, spec taskSpec, comment string) (line, metaLine, name string, ok bool) {
	bin, err := os.Executable()
	if err != nil || bin == "" {
		return "", "", deriveTaskName(comment, spec.command), false
	}
	name = deriveTaskName(comment, spec.command)
	logPath := runlogFilePath()
	wrapped := wrapperCommand(bin, logPath, name, spec.command)
	meta := taskMeta{V: metaVersion, Type: spec.typ, Name: name, Command: spec.command}
	metaLine = meta.encode()
	if metaLine == "" || len(metaLine) > maxWrappedLineLen {
		return "", "", name, false
	}
	if len(schedule)+1+len(wrapped) > maxWrappedLineLen {
		return "", "", name, false
	}
	return wrapped, metaLine, name, true
}
