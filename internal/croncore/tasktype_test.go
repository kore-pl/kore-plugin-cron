package croncore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tempCronFile 让当前用例走 KORE_CRON_FILE 文件后端，返回文件路径。
func tempCronFile(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "crontab.txt")
	t.Setenv("KORE_CRON_FILE", path)
	t.Setenv("KORE_CRON_BACKUP_DIR", filepath.Join(dir, "backup"))
	return path
}

// TestAddShellDefaultType：老调用方只给 schedule+command，type 缺省为 shell。
func TestAddShellDefaultType(t *testing.T) {
	path := tempCronFile(t)
	store := NewStore()
	result, err := store.Add(context.Background(), AddParams{
		Schedule: "0 3 * * *", Command: "echo hello", Comment: "nightly",
	})
	if err != nil {
		t.Fatalf("Add 失败: %v", err)
	}
	if result.Type != TypeShell || result.Entry == nil || result.Entry.Type != TypeShell {
		t.Fatalf("shell 类型不符: %+v", result)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读 crontab 文件失败: %v", err)
	}
	if !strings.Contains(string(content), "# nightly\n# kore-cron:") ||
		!strings.Contains(string(content), " __task-run ") ||
		!strings.Contains(string(content), "'echo hello'") {
		t.Fatalf("crontab 内容不符（应有注释+元数据+包装命令）: %q", content)
	}
}

// TestAddHTTPGeneratesFixedArgv：http 任务生成为固定 argv 的 curl 命令。
func TestAddHTTPGeneratesFixedArgv(t *testing.T) {
	path := tempCronFile(t)
	store := NewStore()
	result, err := store.Add(context.Background(), AddParams{
		Type: TypeHTTP, Schedule: "*/5 * * * *", Comment: "health check",
		URL:    "https://example.com/health?x=1",
		Method: "post",
		Headers: map[string]string{
			"X-B": "2", "X-A": "1",
		},
		Body: `{"ok":true}`,
	})
	if err != nil {
		t.Fatalf("Add http 失败: %v", err)
	}
	want := `curl -sS -m 30 -o /dev/null -X POST -H 'X-A: 1' -H 'X-B: 2' --data-raw '{"ok":true}' 'https://example.com/health?x=1'`
	if result.Entry == nil || result.Entry.Command != want {
		t.Fatalf("生成命令不符:\n got=%q\nwant=%q", result.Entry.Command, want)
	}
	if result.Entry.Type != TypeHTTP {
		t.Fatalf("类型应为 http: %+v", result.Entry)
	}
	content, _ := os.ReadFile(path)
	if len(strings.Split(strings.TrimSpace(string(content)), "\n")) != 3 {
		t.Fatalf("http 任务应写 注释+元数据+任务 三行: %q", content)
	}
}

// TestAddHTTPDefaultsGET：不传 method 默认 GET、无 body 时不出 --data-raw。
func TestAddHTTPDefaultsGET(t *testing.T) {
	tempCronFile(t)
	result, err := NewStore().Add(context.Background(), AddParams{
		Type: TypeHTTP, Schedule: "0 * * * *", URL: "http://127.0.0.1:8080/ping",
	})
	if err != nil {
		t.Fatalf("Add 失败: %v", err)
	}
	want := `curl -sS -m 30 -o /dev/null -X GET 'http://127.0.0.1:8080/ping'`
	if result.Entry.Command != want {
		t.Fatalf("默认 GET 命令不符: %q", result.Entry.Command)
	}
}

// TestAddHTTPQuotesShellMetacharacters：URL/body 里的 shell 元字符被单引号隔离，
// 生成的命令仍是**一行**，不会变成多条 crontab 任务。
func TestAddHTTPQuotesShellMetacharacters(t *testing.T) {
	path := tempCronFile(t)
	result, err := NewStore().Add(context.Background(), AddParams{
		Type: TypeHTTP, Schedule: "0 1 * * *",
		URL:  "https://example.com/a?q='; touch /tmp/pwned; '",
		Body: `price=$HOME; id`,
	})
	if err != nil {
		t.Fatalf("Add 失败: %v", err)
	}
	if !strings.Contains(result.Entry.Command, `'\''`) {
		t.Fatalf("单引号未转义: %q", result.Entry.Command)
	}
	content, _ := os.ReadFile(path)
	if len(strings.Split(strings.TrimSpace(string(content)), "\n")) != 2 {
		t.Fatalf("命令被拆成多行（注入成功）: %q", content)
	}
}

// TestAddHTTPRejectsUnsafeInput：非法 scheme / 换行 / 方法 / NUL 都必须拒绝。
func TestAddHTTPRejectsUnsafeInput(t *testing.T) {
	tempCronFile(t)
	cases := []AddParams{
		{Type: TypeHTTP, Schedule: "0 1 * * *", URL: "ftp://example.com/x"},
		{Type: TypeHTTP, Schedule: "0 1 * * *", URL: "https://example.com/x", Body: "a\nb"},
		{Type: TypeHTTP, Schedule: "0 1 * * *", URL: "https://example.com/x", Body: "a\x00b"},
		{Type: TypeHTTP, Schedule: "0 1 * * *", URL: "https://example.com/x", Method: "TRACE"},
		{Type: TypeHTTP, Schedule: "0 1 * * *", URL: "https://example.com/x", Headers: map[string]string{"X A": "1"}},
		{Type: TypeHTTP, Schedule: "0 1 * * *", URL: "https://example.com/x", Headers: map[string]string{"X-A": "1\r\nX-B: 2"}},
		{Type: TypeHTTP, Schedule: "0 1 * * *"},
	}
	for i, params := range cases {
		if _, err := NewStore().Add(context.Background(), params); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("case %d 应返回 ErrInvalidParam，实际 %v", i, err)
		}
	}
}

// TestAddPluginReturnsSuggestionOnly：plugin 类型不写 crontab，只返回建议描述。
func TestAddPluginReturnsSuggestionOnly(t *testing.T) {
	path := tempCronFile(t)
	result, err := NewStore().Add(context.Background(), AddParams{
		Type: TypePlugin, Schedule: "0 3 * * *", Comment: "夜间备份",
		Plugin: "backup", Method: "backup.create", Params: []byte(`{"scope":"files"}`),
	})
	if err != nil {
		t.Fatalf("Add plugin 失败: %v", err)
	}
	if result.Type != TypePlugin || result.Entry != nil || result.BackupPath != "" {
		t.Fatalf("plugin 类型不应写 crontab: %+v", result)
	}
	if result.SchedulerTask == nil {
		t.Fatal("缺少 scheduler_task 建议")
	}
	task := result.SchedulerTask
	if task.Plugin != "backup" || task.Method != "backup.create" ||
		task.Schedule != "0 3 * * *" || task.Name != "夜间备份" ||
		task.Endpoint != schedulerTasksEndpoint || !task.Enabled || task.TimeoutS != schedulerTaskTimeoutS {
		t.Fatalf("建议任务描述不符: %+v", task)
	}
	if string(task.Params) != `{"scope":"files"}` {
		t.Fatalf("params 透传不符: %s", task.Params)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("plugin 类型不应创建 crontab 文件，实际 err=%v", statErr)
	}
	if !strings.Contains(result.Note, schedulerTasksEndpoint) {
		t.Fatalf("note 未指向内核端点: %q", result.Note)
	}
}

// TestAddPluginRejectsBadShape：插件 id / 方法 / params 形状非法一律拒绝。
func TestAddPluginRejectsBadShape(t *testing.T) {
	tempCronFile(t)
	cases := []AddParams{
		{Type: TypePlugin, Schedule: "0 3 * * *", Plugin: "Backup", Method: "backup.create"},
		{Type: TypePlugin, Schedule: "0 3 * * *", Plugin: "backup", Method: "Backup.create"},
		{Type: TypePlugin, Schedule: "0 3 * * *", Plugin: "backup", Method: "backup.create", Params: []byte(`[1]`)},
		{Type: "unknown", Schedule: "0 3 * * *", Command: "echo hi"},
	}
	for i, params := range cases {
		if _, err := NewStore().Add(context.Background(), params); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("case %d 应返回 ErrInvalidParam，实际 %v", i, err)
		}
	}
}

// TestListReportsEntryType：list 按命令形态回填 type。
func TestListReportsEntryType(t *testing.T) {
	path := tempCronFile(t)
	store := NewStore()
	if _, err := store.Add(context.Background(), AddParams{
		Schedule: "0 3 * * *", Command: "echo hello",
	}); err != nil {
		t.Fatalf("Add shell 失败: %v", err)
	}
	if _, err := store.Add(context.Background(), AddParams{
		Type: TypeHTTP, Schedule: "0 4 * * *", URL: "https://example.com/",
	}); err != nil {
		t.Fatalf("Add http 失败: %v", err)
	}
	// 用户手写 curl（不带本插件前缀）不应被误判为 http。
	if err := os.WriteFile(path, append(mustReadFile(t, path), []byte("0 5 * * * curl -s https://example.com/\n")...), 0o600); err != nil {
		t.Fatalf("追加手写行失败: %v", err)
	}
	list, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("List 失败: %v", err)
	}
	if len(list.Entries) != 3 {
		t.Fatalf("条目数不符: %+v", list.Entries)
	}
	if list.Entries[0].Type != TypeShell || list.Entries[1].Type != TypeHTTP || list.Entries[2].Type != TypeShell {
		t.Fatalf("类型识别不符: %+v", list.Entries)
	}
}

// TestHTTPCommandDeterministic：同一 headers 的不同 map 迭代顺序生成同一命令。
func TestHTTPCommandDeterministic(t *testing.T) {
	headers := map[string]string{"A": "1", "B": "2", "C": "3", "D": "4"}
	first, err := buildHTTPCommand(AddParams{
		Type: TypeHTTP, URL: "https://example.com/", Method: "POST", Headers: headers,
	})
	if err != nil {
		t.Fatalf("生成命令失败: %v", err)
	}
	for i := 0; i < 20; i++ {
		again, err := buildHTTPCommand(AddParams{
			Type: TypeHTTP, URL: "https://example.com/", Method: "POST", Headers: headers,
		})
		if err != nil || again != first {
			t.Fatalf("第 %d 次生成不一致: %q vs %q (err=%v)", i, again, first, err)
		}
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读文件失败: %v", err)
	}
	return data
}
