package croncore

import (
	"strings"
	"testing"
)

func TestValidateSchedule(t *testing.T) {
	valid := []string{
		"0 3 * * *", "*/5 * * * *", "0 0 1 1 0", "0,15,30,45 * * * *",
		"10-20 3 * * 1-5", "0 3 * * 7", "0 3 1,15 * *",
	}
	for _, schedule := range valid {
		if err := ValidateSchedule(schedule); err != nil {
			t.Errorf("合法 schedule %q 被拒绝: %v", schedule, err)
		}
	}
	invalid := []string{
		"", "0 3 * *", "0 3 * * * *", "60 3 * * *", "0 24 * * *",
		"0 3 0 * *", "0 3 * 13 *", "0 3 * * 8", "a 3 * * *",
		"0 3 * * */0", "0 3 * 1-12-5 *", "0 3 * * 5-1",
	}
	for _, schedule := range invalid {
		if err := ValidateSchedule(schedule); err == nil {
			t.Errorf("非法 schedule %q 被接受", schedule)
		}
	}
}

func TestValidateCommand(t *testing.T) {
	valid := []string{"/usr/bin/backup --flag", "echo hi", strings.Repeat("x", 512)}
	for _, command := range valid {
		if err := ValidateCommand(command); err != nil {
			t.Errorf("合法 command %q 被拒绝: %v", command, err)
		}
	}
	invalid := []string{
		"", "   ", "echo a\necho b", "echo a\r", "\necho a",
		strings.Repeat("x", 513),
	}
	for _, command := range invalid {
		if err := ValidateCommand(command); err == nil {
			t.Errorf("非法 command %q 被接受", command)
		}
	}
}

func TestValidateComment(t *testing.T) {
	if err := ValidateComment(""); err != nil {
		t.Errorf("空 comment 应允许: %v", err)
	}
	if err := ValidateComment("daily backup"); err != nil {
		t.Errorf("合法 comment 被拒绝: %v", err)
	}
	for _, comment := range []string{"a\nb", "a\rb", strings.Repeat("x", 513)} {
		if err := ValidateComment(comment); err == nil {
			t.Errorf("非法 comment %q 被接受", comment)
		}
	}
}

const sampleCrontab = `# top level comment
MAILTO=root

# daily backup
0 3 * * * /usr/bin/backup --flag
*/5 * * * * /bin/echo hi
@reboot /bin/boot
not a cron line
`

func TestParseCrontab(t *testing.T) {
	parsed := parseCrontab(sampleCrontab)
	entries := parsed.result()
	if len(entries) != 2 {
		t.Fatalf("应解析出 2 条任务，实际 %d: %+v", len(entries), entries)
	}
	first := entries[0]
	if first.Index != 0 || first.Schedule != "0 3 * * *" ||
		first.Command != "/usr/bin/backup --flag" || first.Comment != "daily backup" {
		t.Fatalf("第一条解析不符: %+v", first)
	}
	if first.Raw != "0 3 * * * /usr/bin/backup --flag" {
		t.Fatalf("raw 未保留原文: %q", first.Raw)
	}
	if entries[1].Comment != "" || entries[1].Schedule != "*/5 * * * *" {
		t.Fatalf("第二条解析不符: %+v", entries[1])
	}
}

func TestSplitEntryRejectsNonEntries(t *testing.T) {
	for _, line := range []string{
		"MAILTO=root", "@reboot /bin/boot", "0 3 * *", "0 3 * * *", "not a cron line",
	} {
		if _, _, ok := splitEntry(line); ok {
			t.Errorf("非任务行被当作任务: %q", line)
		}
	}
}

func TestJoinSplitLinesRoundTrip(t *testing.T) {
	content := "a\nb\n\nc\n"
	lines := splitLines(content)
	if got := joinLines(lines); got != content {
		t.Fatalf("往返不一致: %q != %q", got, content)
	}
	if joinLines(nil) != "" {
		t.Fatal("空行集合应输出空文本")
	}
}
