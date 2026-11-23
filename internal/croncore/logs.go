package croncore

import (
	"context"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// 日志读取参数与边界。
const (
	// maxLogBytes 是单次读取的日志尾部字节数：syslog 可能上百 MB，
	// 只读尾部既够用又不把内存打爆。
	maxLogBytes = 4 << 20
	// journalWindow 是 journalctl 的取数窗口（cron 执行行通常足够回溯 7 天）。
	journalWindow = "7 days ago"
	// defaultLogLimit / maxLogLimit 是 cron.logs 返回条数上限。
	defaultLogLimit = 50
	maxLogLimit     = 200
	// journalTimeout 是单次 journalctl 调用的硬预算。
	journalTimeout = 8 * time.Second
)

// 系统 cron 的启动行有两种常见形态：
//   - syslog 经典格式：Nov 23 10:17:01 host CRON[123]: (root) CMD (command)
//   - journalctl short-iso：2026-11-23T10:17:01+0800 host CRON[123]: (root) CMD (command)
//
// 只解析带 "(user) CMD (...)" 的启动行；pam_unix 会话行、"(CRON) info" 行跳过。
var (
	classicCronLine = regexp.MustCompile(`^([A-Z][a-z]{2})\s+(\d{1,2})\s+(\d{2}):(\d{2}):(\d{2})\s+\S+\s+(?:CRON|crond|CROND)\[(\d+)\]:\s+\(([^)]+)\)\s+CMD\s+\((.*)\)\s*$`)
	isoCronLine     = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:?\d{2}))\s+\S+\s+(?:CRON|crond|CROND)\[(\d+)\]:\s+\(([^)]+)\)\s+CMD\s+\((.*)\)\s*$`)
)

// monthNames 把 syslog 的英文月份缩写映射为 time.Month。
var monthNames = map[string]time.Month{
	"Jan": time.January, "Feb": time.February, "Mar": time.March, "Apr": time.April,
	"May": time.May, "Jun": time.June, "Jul": time.July, "Aug": time.August,
	"Sep": time.September, "Oct": time.October, "Nov": time.November, "Dec": time.December,
}

// logDeps 是日志读取的可注入依赖：单元测试据此构造日志内容，
// 不依赖本机是否真的装了 cron / journald。
type logDeps struct {
	lookPath   func(string) (string, error)
	runJournal func(ctx context.Context) ([]byte, error)
	readTail   func(path string, maxBytes int64) ([]byte, error)
	now        func() time.Time
}

// defaultLogDeps 是生产实现：真实 journalctl 与真实文件尾部读取。
func defaultLogDeps() logDeps {
	return logDeps{
		lookPath:   exec.LookPath,
		runJournal: runJournalctl,
		readTail:   readFileTail,
		now:        time.Now,
	}
}

// runJournalctl 执行固定 argv 的 journalctl：不接受任何外部拼接参数，
// 因此不存在命令注入面；-q 去掉自身提示，-o short-iso 带年份便于解析。
func runJournalctl(ctx context.Context) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, journalTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "journalctl", "-u", "cron", "-o", "short-iso",
		"--since", journalWindow, "--no-pager", "-q")
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return output, nil
}

// readFileTail 读取文件尾部 maxBytes 字节；文件不存在或不可读返回错误。
func readFileTail(path string, maxBytes int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	offset := info.Size() - maxBytes
	if offset < 0 {
		offset = 0
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}
	return io.ReadAll(file)
}

// Logs 读取系统 cron 的执行记录。
//
// 顺序：journalctl -u cron → KORE_CRON_LOG_FILE → /var/log/cron.log →
// /var/log/syslog；第一个能解析出 CRON 启动行的源即为 source。
// 全部不可用时返回 available=false + 原因，不把"没有日志源"伪装成"没有执行"。
func (s *Store) Logs(ctx context.Context, params LogsParams) (*LogsResult, error) {
	if err := s.preflight(ctx, false); err != nil {
		return nil, err
	}
	limit := normalizeLogLimit(params.Limit)
	if s.logs.now == nil {
		s.logs = defaultLogDeps()
	}
	reasons := make([]string, 0, 4)
	if !journalDisabled() {
		if _, err := s.logs.lookPath("journalctl"); err == nil {
			output, runErr := s.logs.runJournal(ctx)
			if runErr != nil {
				reasons = append(reasons, "journalctl -u cron 不可用: "+runErr.Error())
			} else if entries := parseCronLogLines(string(output), s.logs.now()); len(entries) > 0 {
				return s.buildLogsResult("journalctl -u cron", entries, params, ctx, limit), nil
			} else {
				reasons = append(reasons, "journalctl -u cron 最近 7 天没有 CRON 行")
			}
		} else {
			reasons = append(reasons, "未找到 journalctl")
		}
	}
	for _, path := range logFileCandidates() {
		data, err := s.logs.readTail(path, maxLogBytes)
		if err != nil {
			reasons = append(reasons, path+" 不可读: "+err.Error())
			continue
		}
		entries := parseCronLogLines(string(data), s.logs.now())
		if len(entries) > 0 {
			return s.buildLogsResult(path, entries, params, ctx, limit), nil
		}
		reasons = append(reasons, path+" 中未见 CRON 行")
	}
	return &LogsResult{
		Available: false, Source: "none", Entries: []LogEntry{}, Count: 0,
		Note: unavailableLogNote(reasons),
	}, nil
}

// buildLogsResult 过滤、截断并组装成功结果（最新在前）。
func (s *Store) buildLogsResult(source string, entries []LogEntry, params LogsParams, ctx context.Context, limit int) *LogsResult {
	filtered := filterLogsByName(entries, params.Name, s.crontabEntries(ctx))
	if len(filtered) > limit {
		filtered = filtered[len(filtered)-limit:]
	}
	// 最新的在前：界面先看到最近一次执行。
	reversed := make([]LogEntry, 0, len(filtered))
	for i := len(filtered) - 1; i >= 0; i-- {
		reversed = append(reversed, filtered[i])
	}
	return &LogsResult{
		Available: true, Source: source, Entries: reversed, Count: len(reversed),
		Note: "系统 cron 只记录任务启动行（CMD）；如需完整执行输出，请把任务命令重定向到日志文件（例如 >> /var/log/myjob.log 2>&1）",
	}
}

// crontabEntries 读当前 crontab 条目；失败时返回空（日志本身仍可读）。
func (s *Store) crontabEntries(ctx context.Context) []Entry {
	content, err := s.readCrontab(ctx)
	if err != nil {
		return nil
	}
	return parseCrontab(content).result()
}

// filterLogsByName 按任务名过滤：先匹配 crontab 的备注/命令，再按命令回落到
// 日志行文本。匹配是尽力而为（日志命令与 crontab 命令通常逐字一致）。
func filterLogsByName(entries []LogEntry, name string, tasks []Entry) []LogEntry {
	name = strings.TrimSpace(name)
	if name == "" {
		return entries
	}
	lower := strings.ToLower(name)
	matched := make([]string, 0, len(tasks))
	for _, task := range tasks {
		if strings.Contains(strings.ToLower(task.Comment), lower) ||
			strings.Contains(strings.ToLower(task.Command), lower) {
			matched = append(matched, task.Command)
		}
	}
	filtered := make([]LogEntry, 0, len(entries))
	for _, entry := range entries {
		if len(matched) > 0 {
			for _, command := range matched {
				if strings.Contains(entry.Command, command) || strings.Contains(command, entry.Command) {
					filtered = append(filtered, entry)
					break
				}
			}
			continue
		}
		if strings.Contains(strings.ToLower(entry.Command), lower) {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

// normalizeLogLimit 归一 limit：0 取默认，负数与超上限都夹到边界。
func normalizeLogLimit(limit int) int {
	if limit <= 0 {
		return defaultLogLimit
	}
	if limit > maxLogLimit {
		return maxLogLimit
	}
	return limit
}

// journalDisabled 读取测试钩子 KORE_CRON_DISABLE_JOURNAL。
func journalDisabled() bool {
	return strings.TrimSpace(os.Getenv("KORE_CRON_DISABLE_JOURNAL")) != ""
}

// logFileCandidates 返回按优先级排列的日志文件候选。
func logFileCandidates() []string {
	candidates := make([]string, 0, 3)
	if explicit := strings.TrimSpace(os.Getenv("KORE_CRON_LOG_FILE")); explicit != "" {
		candidates = append(candidates, explicit)
	}
	return append(candidates, "/var/log/cron.log", "/var/log/syslog")
}

// unavailableLogNote 组装"没有日志源"的诚实说明与补救方式。
func unavailableLogNote(reasons []string) string {
	note := "系统 crontab 默认不记录执行日志，本机未检测到可用的 cron 日志源（journalctl -u cron / /var/log/cron.log / /var/log/syslog）。如需完整执行输出，请把任务命令重定向到日志文件（例如 >> /var/log/myjob.log 2>&1）。"
	if len(reasons) > 0 {
		shown := reasons
		if len(shown) > 3 {
			shown = shown[:3]
		}
		note += " 检测详情: " + strings.Join(shown, "；")
	}
	return note
}

// parseCronLogLines 解析日志文本中的 CRON 启动行；无法识别的行直接跳过。
func parseCronLogLines(content string, now time.Time) []LogEntry {
	entries := make([]LogEntry, 0, 16)
	for _, line := range strings.Split(content, "\n") {
		if !strings.Contains(line, "CRON") && !strings.Contains(line, "crond") {
			continue
		}
		if entry, ok := parseLogLine(line, now); ok {
			entries = append(entries, entry)
		}
	}
	return entries
}

// parseLogLine 解析单行；ok=false 表示不是 cron 启动行。
func parseLogLine(line string, now time.Time) (LogEntry, bool) {
	if match := isoCronLine.FindStringSubmatch(line); match != nil {
		stamp, err := parseISOTime(match[1])
		if err != nil {
			return LogEntry{}, false
		}
		return LogEntry{
			Time: stamp.Format(time.RFC3339), User: match[3],
			Command: match[4], PID: atoiOrZero(match[2]),
		}, true
	}
	match := classicCronLine.FindStringSubmatch(line)
	if match == nil {
		return LogEntry{}, false
	}
	month, ok := monthNames[match[1]]
	if !ok {
		return LogEntry{}, false
	}
	year := now.Year()
	stamp := time.Date(year, month,
		atoiOrZero(match[2]), atoiOrZero(match[3]), atoiOrZero(match[4]), atoiOrZero(match[5]),
		0, now.Location())
	// syslog 时间不带年份：解析结果比"现在"晚一天以上，说明是去年的日志（跨年）。
	if stamp.After(now.Add(24 * time.Hour)) {
		stamp = time.Date(year-1, month,
			atoiOrZero(match[2]), atoiOrZero(match[3]), atoiOrZero(match[4]), atoiOrZero(match[5]),
			0, now.Location())
	}
	return LogEntry{
		Time: stamp.Format(time.RFC3339), User: match[7],
		Command: match[8], PID: atoiOrZero(match[6]),
	}, true
}

// parseISOTime 解析 journalctl 的 ISO 时间（支持带/不带冒号的时区偏移）。
func parseISOTime(raw string) (time.Time, error) {
	layouts := []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05-0700", "2006-01-02T15:04:05.999999999-0700"}
	var lastErr error
	for _, layout := range layouts {
		stamp, err := time.Parse(layout, raw)
		if err == nil {
			return stamp, nil
		}
		lastErr = err
	}
	return time.Time{}, lastErr
}

// atoiOrZero 把纯数字串转 int；非法值返回 0（日志解析容错，不因此丢整行）。
func atoiOrZero(raw string) int {
	value, err := atoi(raw)
	if err != nil {
		return 0
	}
	return value
}
