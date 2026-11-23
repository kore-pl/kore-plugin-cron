# kore-plugin-cron

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
| `cron.run` | 同步执行**已注册任务**的命令，返回 `{started, output, exit_code, duration_ms}` |

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
`cron.disable` 声明为 `system:admin`，`cron.list` / `cron.logs` 为 `system:read`：内核在 `POST /api/v1/invoke/...` 转发前按
`method_capabilities` 校验并决定是否落审计（只读方法不落 `invoke.*` 审计）。

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
