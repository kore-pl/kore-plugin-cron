//go:build unix

package croncore

import (
	"os"
	"syscall"
)

// lockFile 对 runlog 取排他锁：多个包装进程/手动执行同时结束时，
// 追加与轮转不会互相截断。锁只覆盖单次写入，不跨请求持有。
func lockFile(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_EX)
}

// unlockFile 释放排他锁（关闭文件也会释放，这里显式调用便于阅读）。
func unlockFile(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
}
