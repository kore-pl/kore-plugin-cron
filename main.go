// Package main 是 kore 的 cron 插件：只定义计划任务、记录执行与日志。
//
// 调度由宿主调度器 / systemd timer 按 info.json 的 triggers 触发；本插件
// 是被请求唤醒的被动服务，不内置调度器、不起 ticker、不轮询（G-19）。
//
// 协议面（kore.plugin.v1）：连接内核注入的 UDS → kore.hello 握手 → 会话循环
// 读 request、按 method 分发并回 response；stdout 留给协议，日志写 stderr。
package main

import (
	"fmt"
	"log/slog"
	"net"
	"os"

	"github.com/pengtang1024/kore-plugin-cron/internal/croncore"
)

func main() {
	// 包装执行模式：由 crontab 里的 `__task-run` 命令触发，不连接内核。
	// 必须放在 loadConfig 之前——cron 环境没有 KORE_PLUGIN_SOCK。
	if code, handled := croncore.RunTaskMode(os.Args[1:], os.Stdout, os.Stderr); handled {
		os.Exit(code)
	}
	logger := newLogger(os.Getenv("KORE_LOG_LEVEL"))
	if err := run(logger); err != nil {
		logger.Error("插件退出", "err", err)
		os.Exit(1)
	}
}

// run 建连 → 握手 → 会话循环；收到 kore.shutdown 后返回 nil。
func run(logger *slog.Logger) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	conn, err := net.Dial("unix", cfg.socket)
	if err != nil {
		return fmt.Errorf("连接主壳 socket %s: %w", cfg.socket, err)
	}
	defer func() { _ = conn.Close() }()
	if err := handshake(conn, cfg); err != nil {
		return err
	}
	logger.Info("hello 完成", "plugin_id", cfg.id, "version", cfg.version)
	store := croncore.NewStore()
	return serve(conn, cfg, store, logger)
}
