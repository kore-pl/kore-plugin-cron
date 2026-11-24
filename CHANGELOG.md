# Changelog

## 0.8.0 (2026-11-24)

- feat(cron): 补齐插件配置契约 `cron.config.get` / `cron.config.set`（`system:read` / `system:admin`）——统一配置面板现在有 cron 自己的设置表单。schema 四项全部来自本仓真实能力：`backup_dir`（任务备份目录）/`runlog_dir`（执行日志目录）/`log_file`（系统 cron 日志文件）/`disable_journal`（禁用 journalctl 读取）；不臆造日志保留天数 / 默认用户 / 时区 / 并发上限。
- feat(cron): 配置持久化到 `<KORE_STATE_DIR>/config.json`（0600，临时文件 + fsync + rename 原子写），优先级 `config.json` > 环境变量 > 默认值；非法值返回 `E_INVALID` 且旧配置不变；写失败不改内存、不留半份文件。
- feat(cron): 保存后把生效值写回 croncore 读取的 `KORE_CRON_*` 环境变量——下一次业务调用即用新值，`restart_required=false`（真热生效，不重启插件进程）。
- feat(cron): `info.json` methods 与 method_capabilities 声明两个新方法；`manifest.json` 版本同步 0.8.0。
- test(cron): 新增契约测试覆盖 schema 结构（PLUGIN-CONFIG §2）、info.json 声明、dispatch 命中带域/裸名、set 校验+0600 原子落盘+source=file+env 热生效、非法值不改盘、env 来源、sensitive 掩码与占位语义。

## 0.7.0 (2026-11-24)

- feat(cron): 新增 `cron.migrate`（`system:admin`，幂等）——把宿主 crontab 里指向 `<pluginRoot>/<id>/<版本>/bin/<id>` 的 wrapper 行迁移到稳定路径 `current/bin/<id>`，返回 `rewritten` / `backup_path` / `stable_path` / `stable_ready` / `skipped` / `note`；与启动期迁移共用同一实现。
- feat(cron): `info.json` 声明 `post_upgrade: "cron.migrate"`——内核在安装/升级重载成功后按声明调用一次，**升级后即使插件从未被打开/调用也会完成迁移**，消灭「crontab 继续跑旧版本二进制且无任何提示」的静默失效；启动期迁移保留为兜底（老内核忽略该字段时行为不变）。
- docs(cron): README 方法表补 `cron.migrate` 与升级后置动作说明；版本 0.7.0。

## 0.6.0 (2026-11-24)

- fix(cron): crontab 不再写死版本目录——shell / http 的 wrapper 命令改用稳定路径 `<pluginRoot>/<id>/current/bin/<id>`（内核 installer 维护的 `current` 符号链接，原子切换），升级后系统 cron 自动执行新版本，不再依赖“记得重写 crontab”。
- feat(cron): 插件启动时迁移存量 crontab——把指向 `<pluginRoot>/<id>/<旧版本>/bin/<id>` 的 wrapper 行改写到稳定路径；只改本插件行，保留停用标记 / schedule / 注释 / runlog / 任务名 / 命令逐字不变；写前备份，认不出的行原样保留并计数告警，迁移幂等。
- feat(cron): `cron.list` 每条 wrapper 条目新增 `binary` / `binary_status`（`stable` / `ok` / `stale` / `missing`）/ `binary_note`，结果新增 `stale_count` / `stable_path` / `stable_path_state`；指向旧版本或二进制不存在时给出可操作文案，失败不再静默。
- docs(cron): README 补稳定路径、存量迁移、`binary_status` 取值域与降级态说明；版本 0.6.0。

## 0.5.0 (2026-11-23)

- feat(cron): 新增只读 `cron.templates`：4 个常用模板（每日备份站点 / 每小时健康检查 / 每周清理日志 / 每月证书检查），返回 `{id,name,description,schedule,type,command?,url?,comment?}`，不写 crontab、不执行任何动作。
- feat(cron): 新增 `cron.batch`：`{action: enable|disable|remove|run, ids:[0起下标]}`（去重、最多 50 条），逐条执行并把结果分成 `succeeded` / `failed` 两组；部分失败不回滚已成功条目；remove 从大到小执行避免下标漂移。
- feat(cron): `info.json` 声明 `methods` 清单与 `cron.templates=system:read` / `cron.batch=system:admin`；版本 0.5.0。
- test(cron): 批量部分失败/幂等启用/批量删除下标漂移/非法动作、模板只读与返回值防篡改。
- docs(cron): 前端启停与立即执行接线在主壳 `kore/web/tasks.js`（乐观更新 + 失败回滚、停用行置灰、停用任务点执行给「先启用」提示）；**通知不做**——失败通知属主壳告警中心（跨插件边界，cron 不内建第二套通知通道），不在本插件实现。

## 0.4.1 (2026-11-23)

- feat(cron): `cron.add` 为 shell / http 任务生成固定包装命令（插件自身二进制 `__task-run`），定时执行结束时把 `{task_name, started_at, duration_ms, exit_code, output_head, output_bytes, truncated}` 追加到 `<state>/runlog/runlog.jsonl`（目录 0700、文件 0600）；`cron.run` 的立即执行同样记录，执行结果新增 `task_name` / `output_bytes` / `log_error`。
- feat(cron): wrapper 路径来自 `os.Executable()`、日志路径来自状态目录，用户无法指定；用户命令作为单个 argv 传入并由 `/bin/sh -c` 执行，`cron.list` 仍返回逻辑命令（元数据注释保存在任务行上方）。
- test(cron): wrapper 引号/注入结构、超长退化、runlog 追加/过滤/limit、`__task-run` 真实二进制退出码与 0600 权限端到端。
- 注：`cron.runlog` 查询方法与 `backup` 类型裁决留到下一轮。

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
