# Scheduled task plugin（kore-plugin-cron）

## Overview

This production-grade plugin manages the current user’s crontab for real: all reads and writes go through `crontab -l` / `crontab -`, never touching `/var/spool/cron` directly; the plugin embeds no scheduler, starts no ticker and does no polling (G-19) — actual triggering is performed by the system cron. Scope (ADR-0019): it only manages the system crontab (shell / http jobs); "call a plugin method on schedule" jobs belong to the kernel scheduler, and the plugin only produces a suggested description without creating or executing them.

## Protocol surface

Protocol `kore.plugin.v1`, `lifecycle=lazy`. Methods include `kore.ping` / `shutdown` / `reload` plus `cron.list` / `add` / `remove` / `replace` / `disable` / `enable` / `logs` / `runlog` / `run` / `templates` / `batch` / `migrate` / `config.get` / `config.set`. `cron.runlog` is a read-only execution log with `limit` defaulting to 50 and capped at 200; `cron.batch` handles at most 50 entries per call without rollback on partial failure; `cron.migrate` rewrites wrapper lines pointing at version directories to the stable path and is declared as `post_upgrade`.

## Core capabilities

Every write is backed up and rewritten as a whole; disabling keeps the original definition and only adds a comment marker, while enabling restores it verbatim; `cron.logs` reads system cron startup logs (journalctl / cron.log / syslog); `cron.runlog` is appended by `cron.run` and the `__task-run` wrapper to `<state>/runlog/runlog.jsonl`, querying only the file tail with zero background collectors, keeping the first 512 bytes and setting `truncated`. Four config options — `backup_dir` / `runlog_dir` / `log_file` / `disable_journal` — are written back to `KORE_CRON_*` environment variables on save for immediate effect.

## Security boundary

The plugin only manages the system crontab and only job lines it generated; unrecognized lines are preserved verbatim and counted with a warning; destructive operations are backed up before writing for traceability. Plugin-type jobs are never executed by the plugin itself — they are handed to the kernel scheduler, avoiding execution rights beyond the declared scope; `cron.run` only executes commands of already-registered tasks. Capabilities are `system:read` (read methods) and `system:admin` (write methods). Bypassing the crontab interface to edit the spool directory directly is outside the plugin’s supported scope and is an environment limitation.

## Build and verification

Daily verification uses local `make check` (gofmt → go vet → go test -timeout 200s); before release run `make dist` twice and confirm identical sha256. GitHub Actions is manual-only, Gitea Actions triggers on version tags. Depth level `production`; contract tests for new methods such as `cron.runlog` run with the regular gate.

## Related links

- [源码](https://github.com/kore-pl/kore-plugin-cron)
- [kore 内核与协议](https://github.com/kore-pl/kore)
- [插件索引](https://github.com/kore-pl/kore-plugins)
