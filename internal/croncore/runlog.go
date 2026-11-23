package croncore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// 逐任务执行日志（runlog）的落盘与查询边界。
//
// 设计：纯追加的 JSONL 文件，写入口只有两个——cron.run（立即执行）与
// 包装命令 __task-run（定时执行）。没有后台收集器、没有 ticker（G-19）：
// 执行结束当场追加一行，查询时读文件尾部。
const (
	// runlogDirName 是 <state> 下的子目录名。
	runlogDirName = "runlog"
	// runlogFileName 是追加文件；所有任务共用一份，用 task_name 区分。
	runlogFileName = "runlog.jsonl"
	// runlogDirMode / runlogFileMode：目录 0700、文件 0600（含输出摘要，按敏感处理）。
	runlogDirMode  = 0o700
	runlogFileMode = 0o600
	// runlogMaxBytes 超过后按行轮转，只保留最近 runlogKeepLines 行。
	runlogMaxBytes  = 2 << 20
	runlogKeepLines = 400
	// runlogReadTailBytes 是单次查询读取的尾部字节数。
	runlogReadTailBytes = 4 << 20
	// outputHeadBytes 是 output_head 保留的字节数（其余只计数）。
	outputHeadBytes = 512
	// defaultRunlogLimit / maxRunlogLimit 是 cron.runlog 的返回条数。
	defaultRunlogLimit = 50
	maxRunlogLimit     = 200
	// taskRunTimeout 是包装命令的单次执行上限：系统 cron 本身不设超时，
	// 这里给 1 小时兜底，避免异常任务永久占住包装进程（超时按进程组杀）。
	taskRunTimeout = time.Hour
)

// RunlogEntry 是一条执行记录。OutputHead 是前 outputHeadBytes 字节，
// OutputBytes 是实际输出总字节数，Truncated 表示输出被截断。
type RunlogEntry struct {
	TaskName    string `json:"task_name"`
	StartedAt   string `json:"started_at"`
	DurationMS  int64  `json:"duration_ms"`
	ExitCode    int    `json:"exit_code"`
	OutputHead  string `json:"output_head"`
	OutputBytes int64  `json:"output_bytes"`
	Truncated   bool   `json:"truncated"`
	Mode        string `json:"mode"`
	TimedOut    bool   `json:"timed_out,omitempty"`
}

// RunlogParams 是 cron.runlog 的入参：name 为任务名子串过滤，
// since / until 为 RFC3339 时间窗（含边界），limit 为条数上限。
type RunlogParams struct {
	Name  string `json:"name,omitempty"`
	Since string `json:"since,omitempty"`
	Until string `json:"until,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

// RunlogResult 是 cron.runlog 的结构化结果。
//
// Available=false 表示还没有任何执行记录（文件不存在）；这不是错误，
// Note 说明记录由谁写入，避免把"还没跑过"误判成"功能坏了"。
type RunlogResult struct {
	Available bool          `json:"available"`
	Entries   []RunlogEntry `json:"entries"`
	Count     int           `json:"count"`
	Note      string        `json:"note,omitempty"`
}

// runlogDirPath 解析日志目录：KORE_CRON_RUNLOG_DIR > <KORE_STATE_DIR>/runlog >
// /var/lib/kore/runlog。与备份目录同样支持环境变量覆盖（测试/隔离验证）。
func runlogDirPath() string {
	if dir := strings.TrimSpace(os.Getenv("KORE_CRON_RUNLOG_DIR")); dir != "" {
		return dir
	}
	state := strings.TrimSpace(os.Getenv("KORE_STATE_DIR"))
	if state == "" {
		state = "/var/lib/kore"
	}
	return filepath.Join(state, runlogDirName)
}

// runlogFilePath 是追加文件路径。
func runlogFilePath() string { return filepath.Join(runlogDirPath(), runlogFileName) }

// appendRunLog 追加一条执行记录，必要时按行轮转。
//
// 写入用 O_APPEND + 文件锁：同一任务并发或不同任务同时结束时，行不会互相截断。
// 轮转在锁内做临时文件 + rename，查询端最多看到"轮转前/后"两个完整版本。
func appendRunLog(path string, entry RunlogEntry) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, runlogDirMode); err != nil {
		return wrap(ErrIO, "创建 runlog 目录失败: "+err.Error())
	}
	line, err := json.Marshal(entry)
	if err != nil {
		return wrap(ErrIO, "序列化 runlog 记录失败: "+err.Error())
	}
	line = append(line, '\n')
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, runlogFileMode)
	if err != nil {
		return wrap(ErrIO, "打开 runlog 失败: "+err.Error())
	}
	defer func() { _ = file.Close() }()
	if err := lockFile(file); err != nil {
		return wrap(ErrIO, "锁定 runlog 失败: "+err.Error())
	}
	defer func() { _ = unlockFile(file) }()
	if _, err := file.Write(line); err != nil {
		return wrap(ErrIO, "追加 runlog 失败: "+err.Error())
	}
	info, err := file.Stat()
	if err != nil {
		return wrap(ErrIO, "读取 runlog 大小失败: "+err.Error())
	}
	if info.Size() <= runlogMaxBytes {
		return nil
	}
	return rotateRunLog(path)
}

// rotateRunLog 只保留最近 runlogKeepLines 行（调用方已持有文件锁）。
func rotateRunLog(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return wrap(ErrIO, "读取 runlog 失败: "+err.Error())
	}
	lines := bytes.Split(bytes.TrimSuffix(data, []byte("\n")), []byte("\n"))
	if len(lines) <= runlogKeepLines {
		return nil
	}
	kept := lines[len(lines)-runlogKeepLines:]
	var out bytes.Buffer
	for _, line := range kept {
		if len(line) == 0 {
			continue
		}
		out.Write(line)
		out.WriteByte('\n')
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".runlog-*.tmp")
	if err != nil {
		return wrap(ErrIO, "创建 runlog 临时文件失败: "+err.Error())
	}
	tempName := temp.Name()
	cleanup := func() { _ = os.Remove(tempName) }
	if _, err := temp.Write(out.Bytes()); err != nil {
		_ = temp.Close()
		cleanup()
		return wrap(ErrIO, "写 runlog 临时文件失败: "+err.Error())
	}
	if err := temp.Close(); err != nil {
		cleanup()
		return wrap(ErrIO, "关闭 runlog 临时文件失败: "+err.Error())
	}
	if err := os.Chmod(tempName, runlogFileMode); err != nil {
		cleanup()
		return wrap(ErrIO, "设置 runlog 临时文件权限失败: "+err.Error())
	}
	if err := os.Rename(tempName, path); err != nil {
		cleanup()
		return wrap(ErrIO, "替换 runlog 失败: "+err.Error())
	}
	return nil
}

// Runlog 查询最近的执行记录，按任务名/时间窗过滤，最新在前。
func (s *Store) Runlog(ctx context.Context, params RunlogParams) (*RunlogResult, error) {
	if err := s.preflight(ctx, false); err != nil {
		return nil, err
	}
	since, until, err := parseRunlogWindow(params.Since, params.Until)
	if err != nil {
		return nil, err
	}
	limit := normalizeRunlogLimit(params.Limit)
	path := runlogFilePath()
	data, readErr := readFileTail(path, runlogReadTailBytes)
	if readErr != nil {
		if errors.Is(readErr, os.ErrNotExist) {
			return &RunlogResult{
				Available: false, Entries: []RunlogEntry{}, Count: 0,
				Note: "还没有执行记录：记录由 cron.run 与受管定时任务的包装命令在每次执行结束时追加",
			}, nil
		}
		return nil, wrap(ErrIO, "读取 runlog 失败: "+readErr.Error())
	}

	entries := make([]RunlogEntry, 0, 32)
	for _, raw := range strings.Split(string(data), "\n") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		var entry RunlogEntry
		if err := json.Unmarshal([]byte(raw), &entry); err != nil {
			continue // 半截行/旧格式跳过，不因一行坏数据让整个查询失败
		}
		if !matchRunlogName(entry, params.Name) {
			continue
		}
		if !matchRunlogWindow(entry, since, until) {
			continue
		}
		entries = append(entries, entry)
	}
	if len(entries) > limit {
		entries = entries[len(entries)-limit:]
	}
	reversed := make([]RunlogEntry, 0, len(entries))
	for i := len(entries) - 1; i >= 0; i-- {
		reversed = append(reversed, entries[i])
	}
	return &RunlogResult{
		Available: true, Entries: reversed, Count: len(reversed),
		Note: "记录来自 cron.run（立即执行）与 cron.add 生成的包装命令（定时执行）；output_head 只保留前 512 字节",
	}, nil
}

// parseRunlogWindow 解析时间过滤参数，空值表示不限。
func parseRunlogWindow(sinceRaw, untilRaw string) (time.Time, time.Time, error) {
	var since, until time.Time
	if raw := strings.TrimSpace(sinceRaw); raw != "" {
		parsed, err := parseISOTime(raw)
		if err != nil {
			return time.Time{}, time.Time{}, wrap(ErrInvalidParam, "since 必须是 RFC3339 时间: "+err.Error())
		}
		since = parsed
	}
	if raw := strings.TrimSpace(untilRaw); raw != "" {
		parsed, err := parseISOTime(raw)
		if err != nil {
			return time.Time{}, time.Time{}, wrap(ErrInvalidParam, "until 必须是 RFC3339 时间: "+err.Error())
		}
		until = parsed
	}
	if !since.IsZero() && !until.IsZero() && since.After(until) {
		return time.Time{}, time.Time{}, wrap(ErrInvalidParam, "since 不得晚于 until")
	}
	return since, until, nil
}

// matchRunlogName 按任务名做大小写不敏感的子串过滤。
func matchRunlogName(entry RunlogEntry, name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return true
	}
	return strings.Contains(strings.ToLower(entry.TaskName), strings.ToLower(name))
}

// matchRunlogWindow 判断记录时间是否落在窗口内（含边界）。
func matchRunlogWindow(entry RunlogEntry, since, until time.Time) bool {
	if since.IsZero() && until.IsZero() {
		return true
	}
	stamp, err := parseISOTime(entry.StartedAt)
	if err != nil {
		return false
	}
	if !since.IsZero() && stamp.Before(since) {
		return false
	}
	if !until.IsZero() && stamp.After(until) {
		return false
	}
	return true
}

// normalizeRunlogLimit 归一 limit：0 取默认，负数与超上限夹到边界。
func normalizeRunlogLimit(limit int) int {
	if limit <= 0 {
		return defaultRunlogLimit
	}
	if limit > maxRunlogLimit {
		return maxRunlogLimit
	}
	return limit
}
