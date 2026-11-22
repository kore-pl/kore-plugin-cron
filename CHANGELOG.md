# Changelog

## 0.1.0 (2026-11-22)

- feat(cron): 计划任务插件骨架，方法 cron.list / cron.add / cron.remove（任务定义 + 执行记录 + 日志）。
- feat(cron): 零轮询事件驱动设计——无周期定时器与延时回调，调度交宿主调度器与 systemd timer。
- feat(cron): 生产级四类故障注入测试（kill / timeout / disk / cgroup）。
- feat(cron): 确定性打包（显式条目 + gzip -9n + 固定 mtime/owner，size_bytes 回填偏差 ≤10%）。
