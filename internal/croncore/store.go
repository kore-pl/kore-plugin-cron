package croncore

import (
	"context"
	"os"
	"path/filepath"
	"sync"
)

// Fault 是可注入的故障类型，供四类故障注入测试在方法级验证错误返回。
// 正常运行时一律为 FaultNone；本类型不产生任何后台行为。
type Fault int

const (
	FaultNone     Fault = iota
	FaultKilled         // 模拟 kill -9：进程被外部杀死后的读写中断
	FaultTimeout        // 模拟请求超时：context deadline exceeded
	FaultDiskFull       // 模拟磁盘满：备份或 crontab 写入失败
	FaultCgroup         // 模拟 cgroup 限额：资源不足
)

// Store 是 crontab 管理器：所有读写都经系统 crontab 命令完成，
// 不直接改 /var/spool/cron。纯被动，由请求驱动，不含后台 goroutine。
type Store struct {
	// writeMu 保证同一时刻只有一个读-改-写事务，避免并发请求互相覆盖。
	writeMu sync.Mutex
	// backupDir 为空时按 KORE_CRON_BACKUP_DIR / <state>/cron-backup 解析。
	backupDir string
	fault     Fault
}

// NewStore 构造管理器；备份目录在写入时按环境变量解析。
func NewStore() *Store { return &Store{} }

// NewStoreWithBackupDir 构造管理器并固定备份目录（测试或嵌入场景使用）。
func NewStoreWithBackupDir(dir string) *Store { return &Store{backupDir: dir} }

// InjectFault 注入故障，仅测试使用；生产路径永不调用。
func (s *Store) InjectFault(f Fault) { s.fault = f }

// preflight 在方法入口统一检查上下文与故障，保证方法级错误返回一致。
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

// backupDirPath 解析备份目录：显式配置 > KORE_CRON_BACKUP_DIR >
// <KORE_STATE_DIR>/cron-backup，KORE_STATE_DIR 缺省 /var/lib/kore。
func (s *Store) backupDirPath() string {
	if s.backupDir != "" {
		return s.backupDir
	}
	if dir := os.Getenv("KORE_CRON_BACKUP_DIR"); dir != "" {
		return dir
	}
	state := os.Getenv("KORE_STATE_DIR")
	if state == "" {
		state = "/var/lib/kore"
	}
	return filepath.Join(state, backupDirName)
}
