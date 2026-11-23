package croncore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fixedNow 是日志解析的固定"现在"，避免测试随日期漂移。
func fixedNow() time.Time {
	return time.Date(2026, 11, 23, 12, 0, 0, 0, time.Local)
}

// storeWithLogs 构造带注入日志依赖的 Store。
func storeWithLogs(deps logDeps) *Store {
	store := NewStore()
	store.logs = deps
	return store
}

// TestParseClassicSyslogLines：经典 syslog 行解析出 time/user/command/pid，
// pam 会话行与 info 行跳过。
func TestParseClassicSyslogLines(t *testing.T) {
	content := strings.Join([]string{
		"Nov 23 10:17:01 host CRON[212974]: pam_unix(cron:session): session opened for user root(uid=0) by root(uid=0)",
		"Nov 23 10:17:01 host CRON[212976]: (root) CMD (cd / && run-parts --report /etc/cron.hourly)",
		"Nov 23 10:17:01 host CRON[212974]: pam_unix(cron:session): session closed for user root",
		"Nov 23 10:20:01 host CRON[213000]: (CRON) info (No MTA installed, discarding output)",
		"Nov 23 10:30:01 host CRON[213111]: (pt) CMD (/usr/bin/backup --full)",
	}, "\n")
	entries := parseCronLogLines(content, fixedNow())
	if len(entries) != 2 {
		t.Fatalf("应解析出 2 条启动行，实际 %d: %+v", len(entries), entries)
	}
	if entries[0].User != "root" || entries[0].PID != 212976 ||
		entries[0].Command != "cd / && run-parts --report /etc/cron.hourly" {
		t.Fatalf("首条解析不符: %+v", entries[0])
	}
	if entries[0].Time != "2026-11-23T10:17:01+08:00" && !strings.HasPrefix(entries[0].Time, "2026-11-23T10:17:01") {
		t.Fatalf("时间解析不符: %q", entries[0].Time)
	}
	if entries[1].User != "pt" || entries[1].Command != "/usr/bin/backup --full" {
		t.Fatalf("次条解析不符: %+v", entries[1])
	}
}

// TestParseISOLines：journalctl short-iso 行（带年份与 +0800 偏移）。
func TestParseISOLines(t *testing.T) {
	line := "2026-11-23T10:17:01+0800 host CRON[42]: (www-data) CMD (/usr/bin/php job.php)"
	entries := parseCronLogLines(line, fixedNow())
	if len(entries) != 1 {
		t.Fatalf("应解析出 1 条，实际 %d", len(entries))
	}
	if entries[0].User != "www-data" || entries[0].PID != 42 ||
		entries[0].Command != "/usr/bin/php job.php" ||
		entries[0].Time != "2026-11-23T10:17:01+08:00" {
		t.Fatalf("ISO 行解析不符: %+v", entries[0])
	}
}

// TestParseYearRollover：syslog 无年份，1 月读到 12 月的行应归到去年。
func TestParseYearRollover(t *testing.T) {
	now := time.Date(2026, 1, 2, 0, 0, 0, 0, time.Local)
	entries := parseCronLogLines("Dec 31 23:59:01 host CRON[7]: (root) CMD (echo old)", now)
	if len(entries) != 1 || !strings.HasPrefix(entries[0].Time, "2025-12-31T23:59:01") {
		t.Fatalf("跨年归年不符: %+v", entries)
	}
}

// TestLogsFromJournal：journalctl 有 CRON 行时作为 source，最新在前。
func TestLogsFromJournal(t *testing.T) {
	t.Setenv("KORE_CRON_DISABLE_JOURNAL", "")
	t.Setenv("KORE_CRON_LOG_FILE", filepath.Join(t.TempDir(), "absent.log"))
	deps := logDeps{
		lookPath: func(string) (string, error) { return "/usr/bin/journalctl", nil },
		runJournal: func(context.Context) ([]byte, error) {
			return []byte("2026-11-23T10:17:01+0800 host CRON[1]: (root) CMD (echo one)\n" +
				"2026-11-23T10:20:01+0800 host CRON[2]: (root) CMD (echo two)\n"), nil
		},
		readTail: func(string, int64) ([]byte, error) { return nil, errors.New("should not read") },
		now:      fixedNow,
	}
	result, err := storeWithLogs(deps).Logs(context.Background(), LogsParams{})
	if err != nil {
		t.Fatalf("Logs 失败: %v", err)
	}
	if !result.Available || result.Source != "journalctl -u cron" || result.Count != 2 {
		t.Fatalf("journal 源结果不符: %+v", result)
	}
	if result.Entries[0].Command != "echo two" {
		t.Fatalf("应最新在前: %+v", result.Entries)
	}
	if !strings.Contains(result.Note, "重定向到日志文件") {
		t.Fatalf("note 应给出完整输出的补救方式: %q", result.Note)
	}
}

// TestLogsFallsBackToSyslogFile：journalctl 不可用时读 syslog 文件。
func TestLogsFallsBackToSyslogFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "syslog")
	content := "Nov 23 10:17:01 host CRON[11]: (root) CMD (echo from-file)\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("写 syslog 失败: %v", err)
	}
	t.Setenv("KORE_CRON_DISABLE_JOURNAL", "1")
	t.Setenv("KORE_CRON_LOG_FILE", path)
	deps := defaultLogDeps()
	deps.now = fixedNow
	result, err := storeWithLogs(deps).Logs(context.Background(), LogsParams{Limit: 5})
	if err != nil {
		t.Fatalf("Logs 失败: %v", err)
	}
	if !result.Available || result.Source != path || result.Count != 1 {
		t.Fatalf("文件源结果不符: %+v", result)
	}
	if result.Entries[0].Command != "echo from-file" {
		t.Fatalf("命令解析不符: %+v", result.Entries[0])
	}
}

// TestLogsUnavailableIsHonest：没有任何日志源时 available=false + 原因，
// 不把"读不到日志"伪装成"没有执行"。
func TestLogsUnavailableIsHonest(t *testing.T) {
	t.Setenv("KORE_CRON_DISABLE_JOURNAL", "1")
	t.Setenv("KORE_CRON_LOG_FILE", filepath.Join(t.TempDir(), "absent.log"))
	deps := logDeps{
		readTail: func(string, int64) ([]byte, error) { return nil, errors.New("no such file") },
		now:      fixedNow,
	}
	result, err := storeWithLogs(deps).Logs(context.Background(), LogsParams{})
	if err != nil {
		t.Fatalf("Logs 失败: %v", err)
	}
	if result.Available || result.Source != "none" || len(result.Entries) != 0 {
		t.Fatalf("不可用时应诚实返回空: %+v", result)
	}
	if !strings.Contains(result.Note, "如需完整执行输出") || !strings.Contains(result.Note, "检测详情") {
		t.Fatalf("note 缺少原因与补救方式: %q", result.Note)
	}
}

// TestLogsFilterByName：name 先匹配 crontab 备注，再按命令过滤日志行。
func TestLogsFilterByName(t *testing.T) {
	cronPath := tempCronFile(t)
	if err := os.WriteFile(cronPath, []byte("# 夜间备份\n0 3 * * * /usr/bin/backup --full\n"), 0o600); err != nil {
		t.Fatalf("写 crontab 失败: %v", err)
	}
	t.Setenv("KORE_CRON_DISABLE_JOURNAL", "")
	deps := logDeps{
		lookPath: func(string) (string, error) { return "/usr/bin/journalctl", nil },
		runJournal: func(context.Context) ([]byte, error) {
			return []byte("2026-11-23T03:00:01+0800 host CRON[1]: (pt) CMD (/usr/bin/backup --full)\n" +
				"2026-11-23T04:00:01+0800 host CRON[2]: (pt) CMD (/usr/bin/other)\n"), nil
		},
		readTail: func(string, int64) ([]byte, error) { return nil, errors.New("unused") },
		now:      fixedNow,
	}
	result, err := storeWithLogs(deps).Logs(context.Background(), LogsParams{Name: "夜间备份"})
	if err != nil {
		t.Fatalf("Logs 失败: %v", err)
	}
	if result.Count != 1 || result.Entries[0].Command != "/usr/bin/backup --full" {
		t.Fatalf("按备注过滤失败: %+v", result)
	}
}

// TestLogsLimitNewestFirst：limit 截断后仍保持最新在前。
func TestLogsLimitNewestFirst(t *testing.T) {
	t.Setenv("KORE_CRON_DISABLE_JOURNAL", "")
	deps := logDeps{
		lookPath: func(string) (string, error) { return "/usr/bin/journalctl", nil },
		runJournal: func(context.Context) ([]byte, error) {
			return []byte("2026-11-23T01:00:00+0800 h CRON[1]: (root) CMD (one)\n" +
				"2026-11-23T02:00:00+0800 h CRON[2]: (root) CMD (two)\n" +
				"2026-11-23T03:00:00+0800 h CRON[3]: (root) CMD (three)\n"), nil
		},
		readTail: func(string, int64) ([]byte, error) { return nil, errors.New("unused") },
		now:      fixedNow,
	}
	result, err := storeWithLogs(deps).Logs(context.Background(), LogsParams{Limit: 2})
	if err != nil {
		t.Fatalf("Logs 失败: %v", err)
	}
	if result.Count != 2 || result.Entries[0].Command != "three" || result.Entries[1].Command != "two" {
		t.Fatalf("limit/排序不符: %+v", result.Entries)
	}
}

// TestNormalizeLogLimit：0 取默认，负数取默认，超上限夹到 200。
func TestNormalizeLogLimit(t *testing.T) {
	if got := normalizeLogLimit(0); got != defaultLogLimit {
		t.Fatalf("0 应取默认 %d，实际 %d", defaultLogLimit, got)
	}
	if got := normalizeLogLimit(-5); got != defaultLogLimit {
		t.Fatalf("负数应取默认，实际 %d", got)
	}
	if got := normalizeLogLimit(9999); got != maxLogLimit {
		t.Fatalf("超上限应夹到 %d，实际 %d", maxLogLimit, got)
	}
}
