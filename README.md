# kore-plugin-cron

kore 的计划任务插件（生产级）：**真实管理当前用户的 crontab**。所有读写都通过
`crontab -l` / `crontab -` 完成，不直接改 `/var/spool/cron`；插件不内置调度器、
不起 ticker、不轮询（G-19），实际触发由系统 cron 执行。

## 协议方法（kore.plugin.v1）

| 方法 | 说明 |
| --- | --- |
| `kore.ping` | 存活与运行期指标 |
| `kore.shutdown` | 优雅退出 |
| `kore.reload` | 无状态插件，空操作 |
| `cron.list` | 执行 `crontab -l`，返回条目数组与总数 |
| `cron.add` | 校验后追加条目，写前备份，整份写回 |
| `cron.remove` | 按 0 起下标删除条目及其紧邻注释 |
| `cron.replace` | 按 0 起下标替换 schedule / command，保留注释 |

### 数据形状

`cron.list` 返回：

```json
{"entries":[{"index":0,"schedule":"0 3 * * *","command":"/usr/bin/backup","comment":"nightly backup","raw":"0 3 * * * /usr/bin/backup"}],"count":1}
```

- `index`：本次列表中的 **0 起下标**，`remove` / `replace` 直接使用。
- `comment`：紧邻任务行上方的连续注释块（去掉 `#` 前缀），无注释时省略。
- `raw`：原始行文本，便于核对。
- 无 crontab（`crontab -l` 返回 `no crontab for <user>`）时 `entries` 为 `[]`，不是错误。

`cron.add` 入参 `{"schedule":"分 时 日 月 周","command":"...","comment":"可选"}`，
成功返回 `{"entry":{...},"backup_path":"..."}`。`cron.remove` 入参
`{"index":0}`，`cron.replace` 入参 `{"index":0,"schedule":"...","command":"..."}`。
缺 `index` 或越界返回 `E_INVALID`。

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
- **零定时器**：无 `time.NewTicker` / `time.AfterFunc` / 周期循环；只有请求级
  `context.WithTimeout`（`defer cancel`）。

## 测试隔离（不污染本机 crontab）

单元测试与端到端测试在 `t.TempDir()` 里生成假 `crontab` 脚本，通过 `PATH`
前置注入；假脚本把内容写到临时状态文件，`-l` / `-` / `-r` 行为与真实命令一致。
`tests/fault/` 同样在 `TestMain` 安装假二进制，四类故障（kill / timeout /
disk / cgroup）仍覆盖方法级错误返回：`ErrInterrupted` / `ErrTimeout` /
`ErrIO` / `ErrResource`。

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
