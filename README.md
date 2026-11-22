# kore-plugin-cron

kore 的计划任务插件（M5-A 批1，生产级）。只**定义**任务、只**记录**执行与日志，
**不调度、不轮询、不起 ticker**：调度由宿主调度器或 systemd timer 按 `triggers`
声明触发，本插件是被事件唤醒的被动服务。

## 设计边界（G-19 / 裁决）

- 零轮询事件驱动：全仓无周期定时器、无延时回调、无周期循环；
  握手超时用一次性 `conn.SetDeadline`，请求级超时用 `context.WithTimeout`（均 `Stop`）。
- crontab 表达式**只做语法校验**，不解析为执行计划、不真调度。
- 只依赖 `github.com/pengtang1024/kore/pkg/pluginapi v0.1.1`，无第三方库。

## 协议方法（kore.plugin.v1）

| 方法 | 说明 |
| --- | --- |
| `kore.ping` | 存活与运行期指标 |
| `kore.shutdown` | 优雅退出 |
| `kore.reload` | 无状态插件，空操作 |
| `cron.list` | 列出任务定义与执行记录 |
| `cron.add` | 新增任务定义（校验 name / schedule / command） |
| `cron.remove` | 删除任务定义 |

`cron.add` 的 `schedule` 只校验 5 段 crontab 语法（min hour dom mon dow），非法返回
`E_INVALID`；不计算下次触发时间，不启动任何定时任务。

## 生命周期

`lifecycle=lazy`：由宿主按 `triggers` 唤醒，处理完请求即可退出。插件内部无任何
让进程续命的后台 goroutine 或定时器。

## 构建与验证

```sh
go build ./...
make build
make dist
make check
```

`Makefile` 打包显式列出 `info.json`、`bin/`、`SHA256SUMS`，用 `gzip -9n` + 固定
mtime/owner 保证确定性；`info.json.size_bytes` 三次迭代回填，偏差 ≤ 10%。

## 故障注入

`tests/fault/` 四类（`kill` / `timeout` / `disk` / `cgroup`）验证异常下方法级错误
返回，G-20 生产级门禁。

MIT License — pengtang1024
