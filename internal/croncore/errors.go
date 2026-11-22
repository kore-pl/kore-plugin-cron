package croncore

import "errors"

// 业务错误哨兵：故障注入测试据此断言方法级错误返回，而非仅断言 error != nil。
var (
	ErrInvalidParam = errors.New("cron: invalid parameter")
	// ErrNoPermission 表示当前用户没有 crontab 权限（环境限制，不是服务端故障）。
	ErrNoPermission = errors.New("cron: crontab permission denied")
	ErrNotFound     = errors.New("cron: job not found")
	ErrIO           = errors.New("cron: storage I/O error")
	ErrResource     = errors.New("cron: resource limit exceeded")
	ErrInterrupted  = errors.New("cron: interrupted")
	ErrTimeout      = errors.New("cron: deadline exceeded")
)

// wrap 给底层错误加上下文，保留 errors.Is 可判定性。
func wrap(base error, msg string) error {
	return &cronError{base: base, msg: msg}
}

// cronError 携带上下文同时实现 Unwrap，供 errors.Is 穿透到哨兵。
type cronError struct {
	base error
	msg  string
}

func (e *cronError) Error() string {
	if e.base == nil {
		return e.msg
	}
	return e.msg + ": " + e.base.Error()
}

func (e *cronError) Unwrap() error { return e.base }
