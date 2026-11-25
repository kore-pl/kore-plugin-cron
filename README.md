# kore-plugin-cron

语言：[简体中文](docs/DETAIL.md) · [English](docs/DETAIL.en.md) · [Español](docs/DETAIL.es.md) · [Français](docs/DETAIL.fr.md) · [Deutsch](docs/DETAIL.de.md) · [Русский](docs/DETAIL.ru.md) · [Português](docs/DETAIL.pt.md)

kore 的计划任务插件（生产级）：**真实管理当前用户的 crontab**。所有读写都通过
`crontab -l` / `crontab -` 完成，不直接改 `/var/spool/cron`；插件不内置调度器、
不起 ticker、不轮询（G-19），实际触发由系统 cron 执行。

**能力边界（ADR-0019）**：本插件只管**系统 crontab**（shell / http 任务）；需要
"到点调用某个插件方法"的 plugin 类型任务归**内核 scheduler**（`/api/v1/scheduler/tasks`），
插件只生成建议的任务描述，不创建、不执行。

## 协议方法（kore.plugin.v1）

| 方法 | 说明 |
| --- | --- |
| `kore.ping` | 存活与运行期指标 |
| `kore.shutdown` | 优雅退出 |
| `kore.reload` | 无状态插件，空操作 |
| `cron.list` | 执行 `crontab -l`，返回条目数组与总数；权限不足返回 `entries: []` + `warning`（环境限制，非故障） |
| `cron.add` | 校验后追加条目，写前备份，整份写回 |
| `cron.remove` | 按 0 起下标删除条目及其紧邻注释 |
| `cron.replace` | 按 0 起下标替换 schedule / command，保留注释与启停状态 |
| `cron.disable` | 按 0 起下标停用任务：保留原定义，只加注释标记，系统不再执行 |
| `cron.enable` | 按 0 起下标重新启用：去掉注释标记，逐字还原原任务 |
| `cron.logs` | 读系统 cron 启动日志（journalctl / cron.log / syslog），返回 `{available, source, entries[], note}` |
| `cron.runlog` | 逐次执行记录（只读）：`{name?, since?, until?, limit?}` → `{available, entries[{task_name, started_at, duration_ms, exit_code, output_head, output_bytes, truncated, mode, timed_out}], count, note}`，最新在前；`limit` 缺省 50、上限 200 |
| `cron.run` | 同步执行**已注册任务**的命令，返回 `{started, output, exit_code, duration_ms}` |
| `cron.templates` | 只读内置模板（4 条：每日备份 / 每小时健康检查 / 每周清理日志 / 每月证书检查），返回 `{templates[], count, note}`；不写 crontab、无副作用 |
| `cron.batch` | 批量动作 `{action: enable\|disable\|remove\|run, ids:[0 起下标]}`：一次最多 50 条，逐条执行、部分失败不回滚，返回 `{action,total,succeeded[],failed[],note?}` |
| `cron.migrate` | 升级后置动作（幂等）：把 crontab 里指向版本目录的 wrapper 行迁移到稳定路径，返回 `{rewritten,backup_path?,stable_path?,stable_ready,skipped?,note?}`；`rewritten=0` 表示无需迁移，不写盘 |
| `cron.config.get` | 统一配置面板表单描述（`system:read`）：返回 `{schema:{title,fields[]},values,source}`，四项可配项见下 |
| `cron.config.set` | 保存配置（`system:admin`）：入参 `{values:{...}}`，校验 → 0600 原子写 → 热生效，返回 `{saved,applied,restart_required:false,warnings:[]}` |

### 数据形状

`cron.list` 返回：

```json
{"entries":[{"index":0,"type":"shell","schedule":"0 3 * * *","command":"/usr/bin/backup","comment":"nightly backup","raw":"0 3 * * * /usr/bin/backup","enabled":true}],"count":1}
```

- `index`：本次列表中的 **0 起下标**，`remove` / `replace` 直接使用。
- `comment`：紧邻任务行上方的连续注释块（去掉 `#` 前缀），无注释时省略。
- `raw`：原始行文本，便于核对；停用行返回**去掉标记后**的任务定义。
- `enabled`：任务当前是否生效。`false` 表示该任务已被 `cron.disable` 停用
  （crontab 里是 `#KORE-DISABLED# ` 注释行），定义仍在、可随时 `cron.enable` 还原。
- 无 crontab（`crontab -l` 返回 `no crontab for <user>`）时 `entries` 为 `[]`，不是错误。
- **权限不足**（`crontab -l` 报 `permission denied` / `权限不够`）时同样返回成功：`entries` 为 `[]`、`count` 为 0，并附 `warning`（如「当前用户无 crontab 权限」）——这是环境限制，不映射成服务端故障；真正的 I/O 失败仍返回错误。

`cron.remove` 入参 `{"index":0}`，`cron.replace` 入参
`{"index":0,"schedule":"...","command":"..."}`。缺 `index` 或越界返回 `E_INVALID`。

### 任务类型（cron.add 的 type）

| type | 入参 | 落点 | 说明 |
| --- | --- | --- | --- |
| `shell`（缺省） | `command` | crontab | 命令原样保存，系统 cron 到点执行 |
| `http` | `url`（必填）、`method`（缺省 GET）、`headers`、`body` | crontab | 生成固定 argv 的 `curl -sS -m 30 -o /dev/null ...`，所有可变部分单引号转义；超时 30s |
| `plugin` | `plugin`、`method`、`params` | **不落 crontab** | 只返回 `scheduler_task` 建议描述 + `endpoint`；由前端 `POST /api/v1/scheduler/tasks` 创建 |

`type=http` 的边界：`url` 只支持 http/https、`method` 只支持
GET/POST/PUT/PATCH/DELETE/HEAD；`url`/`headers`/`body` 均拒绝换行、回车、NUL 与控制字符
（`body` ≤ 4 KiB、`headers` ≤ 16 个），生成命令 ≤ 8 KiB——这些值最终要落进 crontab 的一行。

```json
{"type":"http","schedule":"*/5 * * * *","url":"https://example.com/health","method":"POST","headers":{"X-Token":"abc"},"body":"{\"ok\":true}","comment":"健康检查"}
```

`cron.list` 的每条 entry 带 `type`（`shell` / `http`）：**按命令形态尽力识别**——
只有本插件生成的 curl 命令带固定前缀 `curl -sS -m 30 -o /dev/null `，用户手写的同形
curl 也会被识别为 http。`cron.replace` 仍只接收 schedule + command（替换后按新命令
重新识别 type）；http/plugin 任务要改参数就删除后重建。

### 执行日志（cron.logs）

系统 crontab **默认不记录执行输出**，只由 cron/journald 记录"启动行"。
`cron.logs` 的读取顺序：`journalctl -u cron` → `KORE_CRON_LOG_FILE` →
`/var/log/cron.log` → `/var/log/syslog`；第一个解析出 `CRON[pid]: (user) CMD (...)` 的源
即 `source`，条目按**最新在前**返回，`limit` 默认 50、上限 200。

- 解析出的字段：`{time, user, command, pid}`（syslog 无年份，按当前日期推断并处理跨年）。
- `name` 过滤：先匹配 crontab 条目的备注/命令，再按命令过滤日志行（尽力而为）。
- **没有日志源时诚实返回** `available=false` + `source:"none"` + `note`（说明原因与
  "把命令重定向到日志文件"的补救方式），不把"读不到日志"伪装成"没有执行"。

### 逐次执行历史（cron.runlog，0.9.0+）

`cron.logs` 是 cron 守护进程的启动行，**不含退出码与输出**；要看"每次到底成没成功"用 `cron.runlog`：

- 记录来源只有两个写入口：`cron.run`（立即执行，`mode=manual`）与 `cron.add` 生成的包装命令
  `__task-run`（定时执行，`mode=scheduled`），执行结束当场追加一行到
  `<state>/runlog/runlog.jsonl`（目录 0700、文件 0600，输出摘要按敏感处理）；**无后台收集器、无 ticker**。
- 单条记录：`{task_name, started_at(RFC3339), duration_ms, exit_code, output_head(前 512 字节),
  output_bytes(实际总量), truncated, mode, timed_out?}`；`task_name` 与 `cron.list` 的`entry.task_name`
  同一口径（备注优先，其次命令，≤64 字节）。
- 查询：`name` 大小写不敏感子串过滤、`since`/`until` 为 RFC3339 时间窗（含边界）、`limit`
  缺省 50 / 上限 200；返回**最新在前**。文件超过 2 MiB 时按行轮转、只保留最近 400 行。
- 还没有任何记录时返回 `available=false` + `note`（不是错误）；主壳计划任务页的「执行历史」
  面板直接读这个方法，行内「最近执行」也优先用它（刷新不丢）。

### 稳定二进制路径与升级迁移（0.6.0+）

crontab 条目的 wrapper 命令只写**稳定路径** `<pluginRoot>/<id>/current/bin/<id>`：
内核 installer 在 `<pluginRoot>/<id>/current` 维护一个指向当前版本目录的**相对符号链接**，
安装 / 升级时用临时链接 + `rename` 原子切换。因此升级后系统 cron 自动执行新版本，
不依赖“还记得重写 crontab”。

- **存量迁移**：旧版本写入的 `<pluginRoot>/<id>/<版本>/bin/<id>` 条目由插件启动时自动迁移。
  只改指向本插件的 wrapper 行，停用标记 / schedule / 注释 / runlog / 任务名 / 命令
  逐字保留；写前先备份；认不出的行**原样保留并 WARN**（不猜测重写）；迁移幂等。
- **降级态可见**：稳定路径不存在时（旧内核未建 `current` 链接），新任务退化为当前版本
  路径并在 `cron.add` 的 `note` 说明；`cron.list` 返回 `stable_path_state:"missing"` + warning。
- **失败可见**：`cron.list` 每条 wrapper 条目带 `binary_status`：
  `stable`（稳定路径且存在）/ `ok`（存在但不是稳定路径）/ `stale`（指向旧版本）/
  `missing`（二进制不存在，系统 cron 到点会静默失败）。`stale` / `missing` 计入
  `stale_count` 并配 `binary_note` 可操作提示。

```json
{"index":0,"wrapped":true,"binary":"/var/lib/kore/plugins/cron/current/bin/cron","binary_status":"stale","binary_note":"这条任务指向旧版本 0.4.1（当前运行 0.6.0），升级后仍会执行旧二进制，请用 cron.replace 重新保存该条（或重装插件）；插件下次启动也会自动迁移到 /var/lib/kore/plugins/cron/current/bin/cron"}
```

### 启停（cron.enable / cron.disable）

入参都是 `{"index":0}`（与 `cron.remove` / `cron.replace` 同一套 0 起下标）。

- **不删除、不丢定义**：停用只把任务行改成 `#KORE-DISABLED# <原任务行>`，上方注释与
  `schedule + command` 原文完整保留；启用只去掉该前缀，逐字还原。系统 cron 把 `#` 开头的
  行当注释，因此停用任务不会被触发。
- **幂等**：任务已是目标状态时返回 `changed:false`，**不写盘、不产生备份**；真正变化时
  返回 `changed:true` + `backup_path`（与 add/remove/replace 相同的写前备份、失败回滚）。
- **下标稳定**：停用不改变条目顺序或 `index`，停用后仍可按同一下标 `remove` / `replace` /
  `enable`。`replace` 会保留停用状态，不会"顺手启用"。
- **不可立即执行**：`cron.run` 命中停用任务返回 `E_CONFLICT`（错误信息提示先 enable），
  命令不会被执行——否则"停用"可被 run 绕过。
- **标记不可伪造**：普通 `comment` 落盘时总会带 `# ` 前缀，写成
  `# #KORE-DISABLED# ...`，不会被解析成停用任务；`#KORE-DISABLED#` 后不是合法 5 段
  任务的定义按普通注释处理，不出现在 `cron.list`、也不可被 `cron.run` 定位。

```json
{"index":0}
```

返回：

```json
{"entry":{"index":0,"type":"shell","schedule":"0 3 * * *","command":"/usr/bin/backup","raw":"0 3 * * * /usr/bin/backup","enabled":false},"enabled":false,"changed":true,"backup_path":"<state>/cron-backup/crontab-...txt"}
```

### 立即执行（cron.run）

`{"index":0}` 或 `{"name":"nightly backup"}`（备注或命令唯一匹配）；**不接受 command 字段**——
执行目标永远从 crontab 解析结果里取，找不到 / 匹配多条都返回错误，绝不猜一个执行。
执行方式与系统 cron 一致：`/bin/sh -c "<该条命令>"`，超时 60s（进程组一起杀），
同步返回 `{started, output, exit_code, duration_ms, timed_out?}`，输出最多保留 64 KiB。
同一时刻只允许一次手动执行（并发触发返回 `E_CONFLICT`）。**停用任务不可执行**：
`cron.run` 返回 `E_CONFLICT`，必须先 `cron.enable`。

### 方法级能力（info.json）

`cron.run` / `cron.add` / `cron.remove` / `cron.replace` / `cron.enable` /
`cron.disable` / `cron.batch` / `cron.migrate` / `cron.config.set` 声明为 `system:admin`，`cron.list` / `cron.logs` /
`cron.runlog` / `cron.templates` / `cron.config.get` 为 `system:read`：内核在 `POST /api/v1/invoke/...` 转发前按
`method_capabilities` 校验并决定是否落审计（只读方法不落 `invoke.*` 审计）。

### 设置表单（cron.config.get / cron.config.set）

统一配置面板通过这两个方法渲染 cron 自己的设置页（PLUGIN-CONFIG）：`config.json`（`<KORE_STATE_DIR>/config.json`，0600、原子写）> 环境变量 > 默认值。可配四项均对应本仓真实能力：

| key | type | 默认 | 对应环境变量 |
| --- | --- | --- | --- |
| `backup_dir` | string | 空（用 `<state>/cron-backup`） | `KORE_CRON_BACKUP_DIR` |
| `runlog_dir` | string | 空（用 `<state>/runlog`） | `KORE_CRON_RUNLOG_DIR` |
| `log_file` | string | 空（journalctl → /var/log/cron.log） | `KORE_CRON_LOG_FILE` |
| `disable_journal` | bool | `false` | `KORE_CRON_DISABLE_JOURNAL` |

保存后把生效值写回上述环境变量；croncore 每次方法调用都读取这些开关，因此下一次调用即生效，`restart_required=false`，不重启插件进程或 kored。路径项必须是绝对路径，否则 `E_INVALID` 且旧配置不变。

### 升级后置动作（post_upgrade → cron.migrate）

`info.json` 声明 `post_upgrade: "cron.migrate"`：内核在**安装/升级重载成功后**按这条声明调用一次，因此升级后即使插件从未被打开或调用，存量 crontab 也会被迁移到稳定路径——不再出现「系统 cron 继续执行旧版本二进制且没有任何提示」。内核只认识「清单里声明的动作」，不认识 cron 与迁移语义（薄壳边界）；调用失败不会让安装失败，但会写告警中心。插件启动时的迁移保留为兜底（老内核忽略 `post_upgrade` 时行为不变）。

### 模板与批量（cron.templates / cron.batch）

- `cron.templates` 无入参、只读：模板只描述「怎么配」（schedule + 命令 / URL），前端一键填表后仍需用户确认路径与地址，**保存才写 crontab**。
- `cron.batch` 的 `ids` 是 `cron.list` 的 0 起下标，去重后执行；`remove` 按**倒序**删除以免下标漂移，其余动作逐条独立成败，返回里 `succeeded[]` / `failed[]` 分开列，单条失败不影响其它条目。处理预算 120s（最长于单条 `run` 的 60s）。

## 安全与一致性

- **表达式校验**：复用 5 段 crontab 语法（min hour dom mon dow），非法返回
  `E_INVALID`；不解析为执行计划。
- **命令注入防护**：`command` 拒绝换行、回车、NUL、空命令与超过 512 字节的
  内容；命令只作为字符串存储，不解析、不执行。`comment` 同样拒绝换行/回车。
- **写前备份**：每次写操作先把旧 crontab 写入备份文件，返回 `backup_path`。
  目录优先级：`KORE_CRON_BACKUP_DIR` > `KORE_STATE_DIR/cron-backup` >
  `/var/lib/kore/cron-backup`；文件名带 UTC 时间戳与纳秒，权限 0600。
- **失败回滚**：`crontab -` 写失败时用独立超时预算把备份内容写回；回滚也失败
  时错误信息同时给出失败原因与备份路径。
- **并发保护**：进程内 `sync.Mutex` 串行化“读 → 改 → 写”，两个请求不会互相覆盖。
- **手动执行的安全边界**：`cron.run` 只执行 crontab 里已存在的命令；入参只接受
  `index` / `name` 定位符，伪造的 `command` 字段被忽略，错误信息不回显用户输入。
- **零定时器**：无 `time.NewTicker` / `time.AfterFunc` / 周期循环；只有请求级
  `context.WithTimeout`（`defer cancel`）。

## 测试隔离（不污染本机 crontab）

两层隔离，任选其一：

1. **假 crontab 二进制**：单元测试与端到端测试在 `t.TempDir()` 里生成假 `crontab`
   脚本，通过 `PATH` 前置注入；`-l` / `-` / `-r` 行为与真实命令一致。
2. **文件后端 `KORE_CRON_FILE`**：设为一个临时文件路径后，插件直接读写该文件、
   **完全不再调用 crontab 命令**（原子写：同目录临时文件 + rename）。用于隔离
   验证增删/日志/立即执行，绝不碰当前用户真实 crontab；生产不设该变量即保持
   原行为。`KORE_CRON_LOG_FILE`（指定日志文件）与 `KORE_CRON_DISABLE_JOURNAL=1`
   是日志侧的同类验证钩子。

`tests/fault/` 同样在 `TestMain` 安装假二进制，四类故障（kill / timeout /
disk / cgroup）覆盖方法级错误返回：`ErrInterrupted` / `ErrTimeout` /
`ErrIO` / `ErrResource`，并断言故障下**不执行任务命令、不写 crontab**。

## 生命周期

`lifecycle=lazy`：由宿主按 `triggers` 唤醒，处理完请求即可退出；插件内部没有
让进程续命的后台 goroutine 或定时器。

## 构建与验证

```sh
gofmt -l .
go vet ./...
go test -timeout 200s ./...
make build
make dist
make check
```

`Makefile` 打包显式列出 `info.json`、`bin/`、`SHA256SUMS`，用 `gzip -9n` + 固定
mtime/owner 保证确定性；`info.json.size_bytes` 按真实 `.tar.gz` 字节数迭代回填，
连续两次 `make dist` 的 sha256 逐字一致。

MIT License — pengtang1024

<!-- oss:begin -->
## 开源与贡献

[![CI](https://github.com/kore-pl/kore-plugin-cron/actions/workflows/ci.yml/badge.svg)](https://github.com/kore-pl/kore-plugin-cron/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Go 1.26](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)](go.mod)

本仓库是 **kore** 的 `计划任务插件`（id `cron`），独立仓库、独立版本线、独立 CI：GitHub Actions 只手动触发（`workflow_dispatch`），按版本 tag 自动触发在 Gitea Actions（`.gitea/workflows/ci.yml`，push `v*` + 手动）。内核与协议见 [kore](https://github.com/kore-pl/kore)。

- **许可**：MIT（inbound = outbound，不要求 CLA）；提交用 `git commit -s` 签 DCO。
- **贡献**：[CONTRIBUTING.md](CONTRIBUTING.md)（构建、门禁、发版、边界）。
- **安全**：[SECURITY.md](SECURITY.md)——漏洞不要开公开 issue。
- **行为准则**：[CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md)。
- **兼容性**：`protocol: kore.plugin.v1` · `requires_kore: >=0.1.0 <0.2.0` · `lifecycle: lazy` · 深度等级 `production`。
- **验证方式**：本地 `make check` 优先（GitHub CI 只手动触发，Gitea CI 按版本 tag 触发；发布前 `make dist` 连跑两次核对 sha 一致）。
- **发布产物**：`make dist` → `dist/cron-0.9.0.tar.gz` + `.sha256`（位级可复现，连跑两次 sha 相同）。
- **相关仓库**：[索引 kore-plugins](https://github.com/kore-pl/kore-plugins) · [协议文档](https://github.com/kore-pl/kore/blob/main/docs/PLUGIN-PROTOCOL.md) · [SDK 指南](https://github.com/kore-pl/kore/blob/main/docs/PLUGIN-SDK.md)

<!-- oss:end -->

## 支持本项目

如果这个项目对你有帮助，欢迎在 GitHub 仓库右上角点个 ⭐ Star——你的支持是作者持续维护的动力。也欢迎提 issue、PR 或参与讨论。
