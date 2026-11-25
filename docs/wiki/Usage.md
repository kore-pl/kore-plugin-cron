# 使用方法 — 计划任务插件

## 协议面

协议 `kore.plugin.v1`，`lifecycle=lazy`。方法包括 `kore.ping` / `shutdown` / `reload` 与 `cron.list` / `add` / `remove` / `replace` / `disable` / `enable` / `logs` / `runlog` / `run` / `templates` / `batch` / `migrate` / `config.get` / `config.set`。`cron.runlog` 只读执行记录，`limit` 缺省 50、上限 200；`cron.batch` 一次最多 50 条，部分失败不回滚；`cron.migrate` 把指向版本目录的 wrapper 行迁到稳定路径并声明 `post_upgrade`。

## 核心能力

任务写前备份、整份写回；停用保留原定义只加注释标记，启用逐字还原；`cron.logs` 读系统 cron 启动日志（journalctl / cron.log / syslog）；`cron.runlog` 由 `cron.run` 与 `__task-run` 包装命令追加到 `<state>/runlog/runlog.jsonl`，查询只读文件尾部、零后台收集器，输出只留前 512 字节并标 `truncated`。配置四项 `backup_dir` / `runlog_dir` / `log_file` / `disable_journal` 保存后写回 `KORE_CRON_*` 环境变量即时生效。

## 典型流程

1. 握手：调用 `kore.ping` 确认插件存活（`kore.plugin.v1`）。
2. 读操作：调用本插件的只读方法（列表 / 详情 / 探测 / 查询），确认返回结构。
3. 写操作：确认 `info.json` 已声明对应能力，按方法要求提供显式确认参数（如 `confirm: true`）。
4. 失败处理：错误以结构化 `{"code","message"}` 返回；按 `code` 分流，不解析中文文案。

## 安全边界

插件只管理系统 crontab、只管本插件生成的任务行，认不出的行原样保留并计数告警；删除类操作写前备份，可回溯。插件类型任务不自行执行，交内核 scheduler，避免插件进程获得超出声明的执行权限；`cron.run` 只执行已注册任务的命令。能力声明 `system:read`（读方法）/ `system:admin`（写方法）。绕过 crontab 接口直接改 spool 目录不属本插件支持范围，属环境限制。
