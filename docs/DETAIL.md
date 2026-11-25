# 计划任务插件（kore-plugin-cron）

## 简介

本插件是生产级计划任务插件，真实管理当前用户的 crontab：所有读写都通过 `crontab -l` / `crontab -` 完成，不直接改 `/var/spool/cron`；插件不内置调度器、不起 ticker、不轮询（G-19），实际触发由系统 cron 执行。能力边界（ADR-0019）：只管系统 crontab（shell / http 任务）；"到点调用插件方法"的 plugin 类型任务归内核 scheduler，插件只生成建议描述，不创建、不执行。

## 协议面

协议 `kore.plugin.v1`，`lifecycle=lazy`。方法包括 `kore.ping` / `shutdown` / `reload` 与 `cron.list` / `add` / `remove` / `replace` / `disable` / `enable` / `logs` / `runlog` / `run` / `templates` / `batch` / `migrate` / `config.get` / `config.set`。`cron.runlog` 只读执行记录，`limit` 缺省 50、上限 200；`cron.batch` 一次最多 50 条，部分失败不回滚；`cron.migrate` 把指向版本目录的 wrapper 行迁到稳定路径并声明 `post_upgrade`。

## 核心能力

任务写前备份、整份写回；停用保留原定义只加注释标记，启用逐字还原；`cron.logs` 读系统 cron 启动日志（journalctl / cron.log / syslog）；`cron.runlog` 由 `cron.run` 与 `__task-run` 包装命令追加到 `<state>/runlog/runlog.jsonl`，查询只读文件尾部、零后台收集器，输出只留前 512 字节并标 `truncated`。配置四项 `backup_dir` / `runlog_dir` / `log_file` / `disable_journal` 保存后写回 `KORE_CRON_*` 环境变量即时生效。

## 安全边界

插件只管理系统 crontab、只管本插件生成的任务行，认不出的行原样保留并计数告警；删除类操作写前备份，可回溯。插件类型任务不自行执行，交内核 scheduler，避免插件进程获得超出声明的执行权限；`cron.run` 只执行已注册任务的命令。能力声明 `system:read`（读方法）/ `system:admin`（写方法）。绕过 crontab 接口直接改 spool 目录不属本插件支持范围，属环境限制。

## 构建与验证

日常验证用本地 `make check`（gofmt → go vet → go test -timeout 200s）；发布前 `make dist` 连跑两次核对 sha256 一致。GitHub Actions 只手动触发，Gitea Actions 按版本 tag 触发。深度等级 `production`；`cron.runlog` 等新方法的契约测试随常规门禁一起跑。

## 相关链接

- [源码](https://github.com/kore-pl/kore-plugin-cron)
- [kore 内核与协议](https://github.com/kore-pl/kore)
- [插件索引](https://github.com/kore-pl/kore-plugins)
