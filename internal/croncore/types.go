// Package croncore 实现 cron 插件的业务逻辑：通过系统 crontab 命令管理
// 当前用户的任务条目（不直接改 /var/spool/cron）。
//
// 本包纯被动：所有方法由宿主请求驱动，不含任何后台 goroutine 或周期定时器；
// 实际调度由系统 cron 负责，插件只管理配置，因此零 ticker（G-19）。
package croncore

// Entry 是一条 crontab 任务。Index 为本次 list 结果中的 0 起下标，
// Comment 是紧邻任务行上方的注释，Raw 是原始行文本。
type Entry struct {
	Index    int    `json:"index"`
	Schedule string `json:"schedule"`
	Command  string `json:"command"`
	Comment  string `json:"comment,omitempty"`
	Raw      string `json:"raw"`
}

// ListResult 是 cron.list 的结构化结果；无 crontab 时 Entries 为空数组。
type ListResult struct {
	Entries []Entry `json:"entries"`
	Count   int     `json:"count"`
}

// AddParams 是 cron.add 的入参。
type AddParams struct {
	Schedule string `json:"schedule"`
	Command  string `json:"command"`
	Comment  string `json:"comment,omitempty"`
}

// AddResult 是 cron.add 的结构化结果：新条目 + 写前备份路径。
type AddResult struct {
	Entry      Entry  `json:"entry"`
	BackupPath string `json:"backup_path"`
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
