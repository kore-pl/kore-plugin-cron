package croncore

import "encoding/json"

// 任务类型：cron 插件只负责系统 crontab 的 shell / http 任务；
// plugin 类型不在本插件执行，只生成交给内核 scheduler 的任务描述。
const (
	// TypeShell 是普通 shell 命令（crontab 行原样保存 command）。
	TypeShell = "shell"
	// TypeHTTP 是把 url/method/headers/body 生成为固定 argv 的 curl 命令。
	TypeHTTP = "http"
	// TypePlugin 是内核 scheduler 任务（cron 插件不写 crontab、不执行）。
	TypePlugin = "plugin"
)

// Entry 是一条 crontab 任务。Index 为本次 list 结果中的 0 起下标，
// Comment 是紧邻任务行上方的注释，Raw 是原始行文本（停用行返回去掉
// "#KORE-DISABLED# " 标记后的定义），Enabled 表示该任务当前是否生效。
//
// Type 是按命令形态识别出的任务类型：生成命令带固定前缀时识别为 http，
// 其余为 shell；识别是尽力而为（用户自己写的同形 curl 也会被识别为 http）。
type Entry struct {
	Index    int    `json:"index"`
	Type     string `json:"type"`
	Schedule string `json:"schedule"`
	Command  string `json:"command"`
	Comment  string `json:"comment,omitempty"`
	Raw      string `json:"raw"`
	Enabled  bool   `json:"enabled"`
	// TaskName 是 runlog 里使用的任务名（备注优先，其次命令）；Wrapped 表示该任务
	// 由包装命令执行并会写逐任务执行日志。
	TaskName string `json:"task_name,omitempty"`
	Wrapped  bool   `json:"wrapped,omitempty"`
}

// ListResult 是 cron.list 的结构化结果；无 crontab 时 Entries 为空数组。
//
// Warning 非空表示"当前环境读不到 crontab"（如权限不足）——这是环境限制，
// 返回成功 + 空列表，让界面显示灰底提示条而不是把 500 甩给用户。
type ListResult struct {
	Entries []Entry `json:"entries"`
	Count   int     `json:"count"`
	Warning string  `json:"warning,omitempty"`
}

// AddParams 是 cron.add 的入参。
//
// Type 缺省为 shell（向后兼容只给 schedule + command 的老调用方）。
// http 用 url / method / headers / body；plugin 用 plugin / method / params。
// Method 在 http 下是 HTTP 方法、在 plugin 下是插件方法名，二者不会同时使用。
type AddParams struct {
	Type     string            `json:"type,omitempty"`
	Schedule string            `json:"schedule"`
	Command  string            `json:"command,omitempty"`
	Comment  string            `json:"comment,omitempty"`
	URL      string            `json:"url,omitempty"`
	Method   string            `json:"method,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
	Body     string            `json:"body,omitempty"`
	Plugin   string            `json:"plugin,omitempty"`
	Params   json.RawMessage   `json:"params,omitempty"`
}

// SchedulerTaskSuggestion 是 type=plugin 时返回的"建议的任务描述"。
//
// 它对应内核 ADR-0019 的调度任务形状，由前端 POST /api/v1/scheduler/tasks
// 创建；cron 插件只生成描述，不创建、不执行、不落 crontab。
type SchedulerTaskSuggestion struct {
	Name     string          `json:"name"`
	Plugin   string          `json:"plugin"`
	Method   string          `json:"method"`
	Params   json.RawMessage `json:"params"`
	Schedule string          `json:"schedule"`
	TimeoutS int             `json:"timeout_s"`
	Enabled  bool            `json:"enabled"`
	Endpoint string          `json:"endpoint"`
}

// AddResult 是 cron.add 的结构化结果。
//
// shell / http：Entry 与 BackupPath 有值；plugin：SchedulerTask 有值、无 Entry
// 也无 BackupPath（因为完全没碰 crontab），Note 说明下一步。
type AddResult struct {
	Type          string                   `json:"type"`
	Entry         *Entry                   `json:"entry,omitempty"`
	BackupPath    string                   `json:"backup_path,omitempty"`
	SchedulerTask *SchedulerTaskSuggestion `json:"scheduler_task,omitempty"`
	// ScheduledLog 表示该任务已套上包装命令，定时执行会写 runlog；
	// false 时任务仍可执行，只是没有逐任务日志（Note 说明原因）。
	ScheduledLog bool   `json:"scheduled_log"`
	Note         string `json:"note,omitempty"`
}

// RemoveResult 是 cron.remove 的结构化结果。
type RemoveResult struct {
	Removed    Entry  `json:"removed"`
	BackupPath string `json:"backup_path"`
}

// ReplaceParams 是 cron.replace 的入参：index 为 0 起下标。
type ReplaceParams struct {
	Index    int    `json:"index"`
	Schedule string `json:"schedule"`
	Command  string `json:"command"`
}

// ReplaceResult 是 cron.replace 的结构化结果。
type ReplaceResult struct {
	Entry      Entry  `json:"entry"`
	BackupPath string `json:"backup_path"`
}

// ToggleResult 是 cron.enable / cron.disable 的结构化结果。
//
// Changed=false 表示任务本来就处于目标状态：此时没有写盘、没有备份，
// BackupPath 省略——幂等操作不应制造无意义的备份文件。
type ToggleResult struct {
	Entry      Entry  `json:"entry"`
	Enabled    bool   `json:"enabled"`
	Changed    bool   `json:"changed"`
	BackupPath string `json:"backup_path,omitempty"`
}

// LogsParams 是 cron.logs 的入参：name 为可选过滤（匹配备注或命令），
// limit 为返回条数上限（默认 50，最大 200）。
type LogsParams struct {
	Name  string `json:"name,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

// LogEntry 是解析出的一条 cron 启动记录。
type LogEntry struct {
	Time    string `json:"time"`
	User    string `json:"user"`
	Command string `json:"command"`
	PID     int    `json:"pid"`
}

// LogsResult 是 cron.logs 的结构化结果。
//
// Available 为 false 表示本机没有可读的 cron 日志源，此时 Source 为 "none"、
// Entries 为空，Note 说明原因与补救方式——不伪装"没有执行记录"就是"没有执行"。
type LogsResult struct {
	Available bool       `json:"available"`
	Source    string     `json:"source"`
	Entries   []LogEntry `json:"entries"`
	Count     int        `json:"count"`
	Note      string     `json:"note,omitempty"`
}

// RunParams 是 cron.run 的入参：index（0 起）优先，其次 name（备注或命令匹配）。
//
// 只接受"已注册任务的定位符"，不接受 command 字段——执行目标永远从 crontab 读取。
type RunParams struct {
	Index *int   `json:"index,omitempty"`
	Name  string `json:"name,omitempty"`
}

// RunResult 是 cron.run 的同步执行结果。
type RunResult struct {
	Index      int    `json:"index"`
	Schedule   string `json:"schedule"`
	Command    string `json:"command"`
	Started    bool   `json:"started"`
	Output     string `json:"output"`
	ExitCode   int    `json:"exit_code"`
	DurationMS int64  `json:"duration_ms"`
	TimedOut   bool   `json:"timed_out,omitempty"`
	// OutputBytes 是实际输出总字节数（Output 只保留前 64 KiB）。
	OutputBytes int64 `json:"output_bytes,omitempty"`
	// TaskName 是本次执行在 runlog 里的任务名。
	TaskName string `json:"task_name,omitempty"`
	// LogError 非空表示执行本身成功、但 runlog 写入失败（不改变执行结果）。
	LogError string `json:"log_error,omitempty"`
}
