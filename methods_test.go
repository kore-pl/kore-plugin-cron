package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/pengtang1024/kore-plugin-cron/internal/croncore"
	"github.com/pengtang1024/kore-plugin-cron/internal/cronfake"
	"github.com/pengtang1024/kore/pkg/pluginapi"
)

// TestRunRejectsArbitraryCommand 是 cron.run 的安全边界回归：
//   - 只给 command（没有 index/name）→ E_INVALID，绝不执行；
//   - 同时给 index 与伪造 command → 只执行 crontab 里第 index 条的真实命令。
func TestRunRejectsArbitraryCommand(t *testing.T) {
	fake, err := cronfake.Install(t.TempDir())
	if err != nil {
		t.Fatalf("安装假 crontab 失败: %v", err)
	}
	fake.Use(t)
	if err := fake.Seed("0 3 * * * echo hello\n"); err != nil {
		t.Fatalf("预置 crontab 失败: %v", err)
	}
	store := croncore.NewStore()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	rejected := call(t, store, logger, "cron.run", `{"command":"touch /tmp/kore-cron-pwned"}`)
	if rejected.Error == nil || rejected.Error.Code != pluginapi.E_INVALID {
		t.Fatalf("任意命令必须被拒绝: %+v", rejected)
	}
	if strings.Contains(rejected.Error.Message, "touch") {
		t.Fatalf("错误信息不应回显命令内容: %q", rejected.Error.Message)
	}

	result := mustOK(t, call(t, store, logger, "cron.run", `{"index":0,"command":"echo pwned"}`))
	var run croncore.RunResult
	if err := json.Unmarshal(result, &run); err != nil {
		t.Fatalf("解析 run 结果失败: %v", err)
	}
	if strings.TrimSpace(run.Output) != "hello" || run.Command != "echo hello" {
		t.Fatalf("执行目标必须是 crontab 里的命令，实际: %+v", run)
	}
}

// TestRunRequiresLocator：空参数与未知 name 都返回明确错误，不执行任何东西。
func TestRunRequiresLocator(t *testing.T) {
	fake, err := cronfake.Install(t.TempDir())
	if err != nil {
		t.Fatalf("安装假 crontab 失败: %v", err)
	}
	fake.Use(t)
	store := croncore.NewStore()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	response := call(t, store, logger, "cron.run", `{}`)
	if response.Error == nil || response.Error.Code != pluginapi.E_INVALID {
		t.Fatalf("空定位符应返回 E_INVALID: %+v", response)
	}
	response = call(t, store, logger, "cron.run", `{"name":"no-such"}`)
	if response.Error == nil || response.Error.Code != pluginapi.E_NOTFOUND {
		t.Fatalf("未知 name 应返回 E_NOTFOUND: %+v", response)
	}
}

// TestLogsDispatchEncodesResult：logs 通过协议分发返回结构化结果（含 note）。
func TestLogsDispatchEncodesResult(t *testing.T) {
	dir := t.TempDir()
	logPath := dir + "/syslog"
	if err := os.WriteFile(logPath, []byte("Nov 23 10:17:01 host CRON[5]: (root) CMD (echo hi)\n"), 0o600); err != nil {
		t.Fatalf("写日志失败: %v", err)
	}
	t.Setenv("KORE_CRON_DISABLE_JOURNAL", "1")
	t.Setenv("KORE_CRON_LOG_FILE", logPath)
	store := croncore.NewStore()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	result := mustOK(t, call(t, store, logger, "cron.logs", `{"limit":10}`))
	var logs croncore.LogsResult
	if err := json.Unmarshal(result, &logs); err != nil {
		t.Fatalf("解析 logs 失败: %v", err)
	}
	if !logs.Available || logs.Count != 1 || logs.Entries[0].Command != "echo hi" {
		t.Fatalf("logs 结果不符: %+v", logs)
	}
	if logs.Note == "" {
		t.Fatal("logs 应带 note 说明完整输出需重定向")
	}
}

// TestToggleDispatch：cron.enable / cron.disable 走协议分发，返回结构化结果，
// 并验证停用后 cron.run 以 E_CONFLICT 拒绝（错误信息不回显任务内容）。
func TestToggleDispatch(t *testing.T) {
	fake, err := cronfake.Install(t.TempDir())
	if err != nil {
		t.Fatalf("安装假 crontab 失败: %v", err)
	}
	fake.Use(t)
	if err := fake.Seed("# nightly\n0 3 * * * echo toggle-me\n"); err != nil {
		t.Fatalf("预置 crontab 失败: %v", err)
	}
	store := croncore.NewStore()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	var disabled croncore.ToggleResult
	if err := json.Unmarshal(mustOK(t, call(t, store, logger, "cron.disable", `{"index":0}`)), &disabled); err != nil {
		t.Fatalf("解析 disable 失败: %v", err)
	}
	if !disabled.Changed || disabled.Enabled || disabled.Entry.Enabled {
		t.Fatalf("disable 结果不符: %+v", disabled)
	}

	rejected := call(t, store, logger, "cron.run", `{"index":0}`)
	if rejected.Error == nil || rejected.Error.Code != pluginapi.E_CONFLICT {
		t.Fatalf("停用任务 cron.run 应返回 E_CONFLICT: %+v", rejected)
	}
	if strings.Contains(rejected.Error.Message, "toggle-me") {
		t.Fatalf("错误信息不应回显任务内容: %q", rejected.Error.Message)
	}

	var enabled croncore.ToggleResult
	if err := json.Unmarshal(mustOK(t, call(t, store, logger, "cron.enable", `{"index":0}`)), &enabled); err != nil {
		t.Fatalf("解析 enable 失败: %v", err)
	}
	if !enabled.Changed || !enabled.Enabled {
		t.Fatalf("enable 结果不符: %+v", enabled)
	}

	if missing := call(t, store, logger, "cron.enable", `{}`); missing.Error == nil || missing.Error.Code != pluginapi.E_INVALID {
		t.Fatalf("缺 index 应返回 E_INVALID: %+v", missing)
	}
	if outOfRange := call(t, store, logger, "cron.disable", `{"index":9}`); outOfRange.Error == nil || outOfRange.Error.Code != pluginapi.E_INVALID {
		t.Fatalf("越界应返回 E_INVALID: %+v", outOfRange)
	}
}

// TestPostUpgradeHookDeclaredAndImplemented 守护「声明即实现」：
// info.json 的 post_upgrade 必须指向真实方法，否则内核调用得到 E_NOTFOUND，
// 迁移会再次退化成静默不执行（这正是本次修复要消灭的失效模式）。
func TestPostUpgradeHookDeclaredAndImplemented(t *testing.T) {
	raw, err := os.ReadFile("info.json")
	if err != nil {
		t.Fatalf("读取 info.json 失败: %v", err)
	}
	var info struct {
		PostUpgrade        string            `json:"post_upgrade"`
		Methods            []string          `json:"methods"`
		MethodCapabilities map[string]string `json:"method_capabilities"`
	}
	if err := json.Unmarshal(raw, &info); err != nil {
		t.Fatalf("解析 info.json 失败: %v", err)
	}
	if info.PostUpgrade != "cron.migrate" {
		t.Fatalf("post_upgrade 应声明 cron.migrate，实际 %q", info.PostUpgrade)
	}
	if info.MethodCapabilities[info.PostUpgrade] != "system:admin" {
		t.Fatalf("post_upgrade 指向的方法必须显式声明能力，实际 %v", info.MethodCapabilities)
	}
	declared := false
	for _, method := range info.Methods {
		if method == info.PostUpgrade {
			declared = true
		}
	}
	if !declared {
		t.Fatalf("post_upgrade=%q 未出现在 methods 清单里: %v", info.PostUpgrade, info.Methods)
	}

	fake, err := cronfake.Install(t.TempDir())
	if err != nil {
		t.Fatalf("安装假 crontab 失败: %v", err)
	}
	fake.Use(t)
	if err := fake.Seed("0 3 * * * echo hello\n"); err != nil {
		t.Fatalf("预置 crontab 失败: %v", err)
	}
	store := croncore.NewStore()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	var migrated croncore.MigrateResult
	if err := json.Unmarshal(mustOK(t, call(t, store, logger, info.PostUpgrade, `{}`)), &migrated); err != nil {
		t.Fatalf("解析迁移结果失败: %v", err)
	}
	// 测试二进制不在 <pluginRoot>/<id>/<version>/bin/<id> 布局里：
	// 必须「跳过并说明原因」，不能报错、也不能假装迁移成功。
	if migrated.Note == "" {
		t.Fatalf("非安装布局必须返回跳过原因，实际 %+v", migrated)
	}
}
