# Changelog

## 0.2.1 (2026-11-23)

- fix: 权限不足返回空列表 + warning，不再当服务故障
- build: 位级可复现打包（stage chmod + tar --mode 归一、size_bytes 收敛），二进制改嵌 manifest.json，消除同版本 sha 漂移

## 0.2.0 (2026-11-22)

- feat(cron): cron.list / cron.add / cron.remove / cron.replace 真实管理当前用户 crontab（`crontab -l` / `crontab -`），不再只是内存骨架。
- feat(cron): 写前备份到 `KORE_CRON_BACKUP_DIR`（默认 `<state>/cron-backup/`，纳秒时间戳、0600），写失败自动回滚并返回 backup_path。
- feat(cron): 命令注入防护——拒绝换行、回车、NUL、空命令与超长内容；comment 同样校验；0 起 index 删除/替换。
- feat(cron): 进程内写锁串行化读改写；复用 validate.go 5 段表达式校验；零 ticker/AfterFunc（G-19）。
- test(cron): 假 crontab 二进制经 PATH 注入，单元 + 协议分发端到端覆盖 list/add/remove/replace、备份、回滚与注入拒绝；四类故障注入测试适配真实写路径。

## 0.1.0 (2026-11-22)

- feat(cron): 计划任务插件骨架，方法 cron.list / cron.add / cron.remove（任务定义 + 执行记录 + 日志）。
- feat(cron): 零轮询事件驱动设计——无周期定时器与延时回调，调度交宿主调度器与 systemd timer。
- feat(cron): 生产级四类故障注入测试（kill / timeout / disk / cgroup）。
- feat(cron): 确定性打包（显式条目 + gzip -9n + 固定 mtime/owner，size_bytes 回填偏差 ≤10%）。

## [Unreleased]

### Fixed

- `cron.list` 区分「无 crontab」（正常空）与「权限不足」（环境限制）：权限错误返回成功 + 空列表 + `warning` 字段，不再以 500 / 原始 stderr 甩给界面；真正 I/O 失败仍返回错误。
