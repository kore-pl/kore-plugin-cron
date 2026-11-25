# 配置 — 计划任务插件

## 能力声明（`info.json`）

- 协议：`kore.plugin.v1`；生命周期：`lazy`；深度等级见仓库 `info.json`。
- 能力：以 `info.json.capabilities` 与 `method_capabilities` 为准；读方法 `system:read`，写方法 `system:admin`。

## 配置优先级与存储

统一配置契约遵循 `kore/docs/PLUGIN-CONFIG.md`：`config.json` > 环境变量 > 代码默认值；配置文件位于 `<KORE_STATE_DIR>/config.json`，0600 原子写（临时文件 + fsync + rename）。保存后同一进程下一次调用立即生效（`restart_required=false`）；非法值返回 `E_INVALID` 且旧配置不变。

## 本插件可配项

任务写前备份、整份写回；停用保留原定义只加注释标记，启用逐字还原；`cron.logs` 读系统 cron 启动日志（journalctl / cron.log / syslog）；`cron.runlog` 由 `cron.run` 与 `__task-run` 包装命令追加到 `<state>/runlog/runlog.jsonl`，查询只读文件尾部、零后台收集器，输出只留前 512 字节并标 `truncated`。配置四项 `backup_dir` / `runlog_dir` / `log_file` / `disable_journal` 保存后写回 `KORE_CRON_*` 环境变量即时生效。

## 状态目录

状态目录由环境变量指定（各插件不同，如 `KORE_APP_DEPLOY_STATE`、`KORE_STATE_DIR`），缺省一般位于 `<root>/state`。目录 0700、文件 0600；记录损坏或版本高于支持版本一律报错，不静默重置。
