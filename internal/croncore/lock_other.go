//go:build !unix

package croncore

import "os"

// lockFile 是非 Unix 平台的兜底：没有 flock，退化为进程内串行
// （cron 插件以 Linux 为 Tier A，该分支只保证可编译）。
func lockFile(file *os.File) error { return nil }

// unlockFile 与 lockFile 配对，非 Unix 下无操作。
func unlockFile(file *os.File) error { return nil }
