package croncore

import (
	"context"
	"io"
)

// Fault 是可注入的故障类型，供四类故障注入测试在方法级验证错误返回。
// 正常运行时一律为 FaultNone；本类型不产生任何后台行为。
type Fault int

const (
	FaultNone     Fault = iota
	FaultKilled         // 模拟 kill -9：进程被外部杀死后的读写中断
	FaultTimeout        // 模拟请求超时：context deadline exceeded
	FaultDiskFull       // 模拟磁盘满：持久化写失败
	FaultCgroup         // 模拟 cgroup 限额：资源不足
)

// Store 持有任务定义、执行记录与日志，纯内存、被动调用。
// sink 是可选的持久化出口；nil 表示不持久化（仅内存）。
type Store struct {
	jobs       []Job
	executions []Execution
	logs       []LogEntry
	sink       io.Writer
	fault      Fault
}

// NewStore 构造空存储。
func NewStore() *Store {
	return &Store{}
}

// SetSink 注入持久化出口，用于验证磁盘写失败路径。
func (s *Store) SetSink(w io.Writer) { s.sink = w }

// InjectFault 注入故障，仅测试使用；生产路径永不调用。
func (s *Store) InjectFault(f Fault) { s.fault = f }

// preflight 在所有方法入口统一检查故障与上下文，保证方法级错误返回一致。
func (s *Store) preflight(ctx context.Context, needWrite bool) error {
	if err := ctx.Err(); err != nil {
		return wrap(ErrTimeout, err.Error())
	}
	switch s.fault {
	case FaultKilled:
		return wrap(ErrInterrupted, "进程已被终止")
	case FaultTimeout:
		return wrap(ErrTimeout, "请求超出时限")
	case FaultCgroup:
		return wrap(ErrResource, "cgroup 资源限额")
	case FaultDiskFull:
		if needWrite {
			return wrap(ErrIO, "磁盘已满，无法写入")
		}
	}
	return nil
}
