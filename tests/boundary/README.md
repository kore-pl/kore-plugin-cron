# tests/boundary — production 判据①（边界用例）

`docs/PLUGIN-CATALOG.md:90` 的升级判据①要求：**异常输入 / 并发 / 权限不足 / 资源耗尽
各 ≥1 例，并进 CI**。本目录就是它的执行面。

| 类别 | 用例 | 断言 |
| --- | --- | --- |
| 异常输入 | `TestInvalidInputRejected`（非法 cron 表达式 ×2、任务名非法、命令为空、命令注入换行、命令超长）、`TestInvalidIndexRejected` | `errors.Is(err, ErrInvalidParam)`；拒绝后 crontab 文件不被创建/改动 |
| 并发 | `TestConcurrentMutateSameTask`（90 goroutine 同时 add/replace/remove 同一任务）、`TestConcurrentRunSameTask`（8 goroutine 同时触发同一任务） | 最终条目数 = 1 + 成功 Add − 成功 Remove；无截断/拼接行、无残留临时文件、备份数与写盘次数一致；同一任务只有一个执行实例，其余 `ErrBusy`，runlog 恰好 1 行 |
| 权限不足 | `TestPermissionDeniedCrontabFile`（真实 `chmod 000`，EACCES）、`TestPermissionDeniedRootOnlyPath`（非 root 访问 `/root`） | 写方法返回 `ErrNoPermission`；`List` 按契约返回空列表 + warning；不产生备份、不改 crontab |
| 资源耗尽 | `TestResourceExhaustionCrontabWrite`、`TestResourceExhaustionBackupWrite`（子进程真实 `ulimit -f 0` → 内核 EFBIG） | 返回 `ErrIO`、错误含 `file too large`、写 crontab 失败后已回滚且无半截内容、原 crontab 一字节不变 |

## 隔离口径（G-29）

所有用例把 `KORE_CRON_FILE` 指向 `t.TempDir()` 下的普通文件，备份 / runlog / state
目录也在临时目录；**不调用宿主 `crontab` 命令，不写真实用户 crontab**。

## 为什么没有 `t.Parallel`

`t.Setenv` 与 `t.Parallel` 互斥（标准库直接 panic），而本目录每个用例都需要把
`KORE_CRON_FILE` 等环境变量切到独立临时目录。并发证据来自真实 goroutine
（`go func` + `sync.WaitGroup`）并由 `-race` 验证，不是用 `t.Parallel` 凑数。

## 为什么不用 `/dev/full`

`Store.Add` 先 read 再 write；把 `KORE_CRON_FILE` 指到 `/dev/full` 会在读取阶段
无限读零（`os.ReadFile` 永不 EOF）。而且 crontab 采用"同目录临时文件 + rename"的
原子写：非 root 无法在 `/dev` 建临时文件，rename 也不能覆盖设备节点。因此资源耗尽
用 `ulimit -f 0` 注入真实 `EFBIG`——它与磁盘满（ENOSPC）落在同一个 `write(2)` 失败
路径上，覆盖 `writeCrontabFile` 报错 → cleanup → commit 回滚。

## 命令

```sh
GOWORK=off go test ./tests/boundary/ -count=1          # 常规
GOWORK=off go test -race ./tests/boundary/ -count=1    # 并发/数据竞争
```

CI 口径：`Makefile` 的 `check` 目标与 `.github/workflows/ci.yml` 都执行
`go test ./...`，本目录随之进入 CI；`-race` 是并发专项的本地/评审验证命令。
