// Package croncore 实现 cron 插件的业务逻辑：任务定义、执行记录与日志。
//
// 本包纯被动：所有方法由宿主请求驱动，不含任何后台 goroutine 或周期定时器。
// 调度由宿主调度器 / systemd timer 依据 info.json 的 triggers 触发。
package croncore

import "time"

// Job 是一条任务定义。Schedule 只做语法校验，不解析为执行计划。
type Job struct {
	Name     string `json:"name"`
	Schedule string `json:"schedule"`
	Command  string `json:"command"`
	Enabled  bool   `json:"enabled"`
}

// Execution 是一条执行记录，由宿主上报的执行结果写入，不由本插件产生。
type Execution struct {
	ID         string `json:"id"`
	JobName    string `json:"job_name"`
	Status     string `json:"status"`
	ExitCode   int    `json:"exit_code"`
	DurationMS int64  `json:"duration_ms"`
	StartedAt  string `json:"started_at"`
}

// LogEntry 是一条执行日志。
type LogEntry struct {
	Timestamp string `json:"timestamp"`
	JobName   string `json:"job_name"`
	Level     string `json:"level"`
	Message   string `json:"message"`
}

// ListResult 是 cron.list 的结构化结果。
type ListResult struct {
	Jobs       []Job       `json:"jobs"`
	Executions []Execution `json:"executions"`
	Logs       []LogEntry  `json:"logs"`
	Count      int         `json:"count"`
}

// AddParams 是 cron.add 的入参。
type AddParams struct {
	Name     string `json:"name"`
	Schedule string `json:"schedule"`
	Command  string `json:"command"`
	Enabled  *bool  `json:"enabled"`
}

// AddResult 是 cron.add 的结构化结果。
type AddResult struct {
	Name      string `json:"name"`
	Schedule  string `json:"schedule"`
	Command   string `json:"command"`
	Enabled   bool   `json:"enabled"`
	Duplicate bool   `json:"duplicate"`
}

// RemoveParams 是 cron.remove 的入参。
type RemoveParams struct {
	Name string `json:"name"`
}

// RemoveResult 是 cron.remove 的结构化结果。
type RemoveResult struct {
	Name    string `json:"name"`
	Removed bool   `json:"removed"`
}

// nowRFC3339 返回统一时间戳格式；注入时钟由调用方负责，本包不启动定时器。
func nowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339)
}
