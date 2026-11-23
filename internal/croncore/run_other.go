//go:build !unix

package croncore

import "os/exec"

// prepareRunCommand 是非 Unix 平台的兜底：没有进程组语义，取消只杀直接子进程。
// cron 插件以 Linux（Tier A）为主，该分支只保证可编译。
func prepareRunCommand(cmd *exec.Cmd) {}
