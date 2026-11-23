//go:build unix

package croncore

import (
	"os/exec"
	"syscall"
)

// prepareRunCommand 让手动执行的任务跑在独立进程组里，超时按组杀：
// 只杀 shell 会让命令里的子进程继续持有管道，等待时间被拖到子进程结束。
func prepareRunCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// 负 PID 表示整个进程组；失败时退回单进程 kill，至少不放弃取消。
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
}
