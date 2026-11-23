# Changelog

## 0.4.0 (2026-11-23)

- feat(cron): 新增 `cron.enable` / `cron.disable`——单条任务可停用/启用而不删除：停用把任务行改成 `#KORE-DISABLED# <原任务行>` 注释，上方注释与 schedule/command 原文完整保留，系统 cron 不再触发；启用去掉标记即逐字还原。
- feat(cron): `cron.list` 每条 entry 新增 `enabled` 字段；停用条目仍占原 index（不重排），`raw` 返回去掉标记后的任务定义。
- fix(cron): `cron.replace` 保留停用状态，不再隐含「替换即启用」。
- feat(cron): 启停幂等——已是目标状态时返回 `changed:false` 且不写盘、不产生备份；真正变化时返回 `changed:true` + `backup_path`，沿用写前备份与失败回滚。
- fix(cron): `cron.run` 拒绝执行停用任务（`E_CONFLICT`，提示先 `cron.enable`），停用不可被立即执行绕过。
- feat(cron): `info.json` 为 `cron.enable` / `cron.disable` 声明 `system:admin` 方法级能力；版本 0.4.0。
- test(cron): 覆盖启停往返、下标稳定、替换保留停用状态、注释伪造标记无效、畸形停用行按注释处理、停用前备份；故障注入覆盖磁盘满与写失败回滚、停用任务零执行；端到端覆盖 disable → list → run 拒绝 → enable。
- docs(cron): README 补启停语义、`enabled` 字段与停用标记不可伪造的口径。

## 0.3.0 (2026-11-23)

- feat(cron): `cron.add` 支持任务类型——`shell`（缺省，原行为）/ `http`（url/method/headers/body 生成固定 argv 的 `curl -sS -m 30 -o /dev/null ...`，单引号转义、超时 30s）/ `plugin`（不落 crontab，只返回内核 scheduler 的任务描述 + `endpoint`，边界见 ADR-0019）。
- feat(cron): `cron.logs` 读系统 cron 启动日志——`journalctl -u cron` → `KORE_CRON_LOG_FILE` → `/var/log/cron.log` → `/var/log/syslog`，解析 `{time,user,command,pid}`，最新在前、`limit` 默认 50/上限 200、`name` 过滤；无日志源时 `available=false` + 原因与补救说明（不把"读不到"伪装成"没执行"）。
- feat(cron): `cron.run` 同步执行已注册任务——只接受 `index`/`name` 定位符、命令从 crontab 读取（拒绝任意命令）、`/bin/sh -c` 与系统 cron 一致、60s 超时按进程组杀、输出 64 KiB 上限、并发返回 `E_CONFLICT`。
- feat(cron): `info.json` 增加方法级 `method_capabilities`（run/add/remove/replace=`system:admin`，list/logs=`system:read`），内核据此授权与审计。
- feat(cron): `KORE_CRON_FILE` 文件后端（隔离验证用，原子写）与 `KORE_CRON_LOG_FILE` / `KORE_CRON_DISABLE_JOURNAL` 日志验证钩子。
- test(cron): 任务类型命令生成/注入拒绝、日志解析（经典 syslog + journalctl ISO + 跨年）、run 只跑已注册任务/超时取消/并发拒绝、文件后端增删、四类故障下新方法返回与零副作用；端到端用例覆盖三种类型 + 日志 + 立即执行。
- docs(cron): README 补齐任务类型、日志、立即执行、方法级能力与隔离验证口径。

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
