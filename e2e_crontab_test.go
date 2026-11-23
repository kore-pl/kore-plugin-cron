package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/pengtang1024/kore-plugin-cron/internal/croncore"
	"github.com/pengtang1024/kore-plugin-cron/internal/cronfake"
	"github.com/pengtang1024/kore/pkg/pluginapi"
)

// TestCrontabLifecycleE2E 通过协议分发层（与真实请求同一入口）跑完整生命周期：
// add 两条 → list → remove 第一条 → list → replace → list；crontab 全部落到假二进制。
func TestCrontabLifecycleE2E(t *testing.T) {
	fake, err := cronfake.Install(t.TempDir())
	if err != nil {
		t.Fatalf("安装假 crontab 失败: %v", err)
	}
	fake.Use(t)
	store := croncore.NewStore()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	first := mustOK(t, call(t, store, logger, "cron.add",
		`{"schedule":"0 3 * * *","command":"/usr/bin/backup --full","comment":"nightly backup"}`))
	t.Logf("E2E_JSON cron.add#1 %s", first)
	second := mustOK(t, call(t, store, logger, "cron.add",
		`{"schedule":"*/5 * * * *","command":"/bin/echo tick"}`))
	t.Logf("E2E_JSON cron.add#2 %s", second)

	list := decodeList(t, mustOK(t, call(t, store, logger, "cron.list", `{}`)))
	t.Logf("E2E_JSON cron.list#1 %s", mustOK(t, call(t, store, logger, "cron.list", `{}`)))
	if list.Count != 2 || len(list.Entries) != 2 ||
		list.Entries[0].Command != "/usr/bin/backup --full" ||
		list.Entries[1].Command != "/bin/echo tick" {
		t.Fatalf("add 后 list 不符: %+v", list.Entries)
	}

	removed := mustOK(t, call(t, store, logger, "cron.remove", `{"index":0}`))
	t.Logf("E2E_JSON cron.remove#0 %s", removed)
	list = decodeList(t, mustOK(t, call(t, store, logger, "cron.list", `{}`)))
	t.Logf("E2E_JSON cron.list#2 %s", mustOK(t, call(t, store, logger, "cron.list", `{}`)))
	if list.Count != 1 || list.Entries[0].Command != "/bin/echo tick" {
		t.Fatalf("remove 后 list 不符: %+v", list.Entries)
	}

	replaced := mustOK(t, call(t, store, logger, "cron.replace",
		`{"index":0,"schedule":"10 2 * * *","command":"/bin/echo replaced"}`))
	t.Logf("E2E_JSON cron.replace#0 %s", replaced)
	list = decodeList(t, mustOK(t, call(t, store, logger, "cron.list", `{}`)))
	t.Logf("E2E_JSON cron.list#3 %s", mustOK(t, call(t, store, logger, "cron.list", `{}`)))
	if list.Count != 1 || list.Entries[0].Schedule != "10 2 * * *" ||
		list.Entries[0].Command != "/bin/echo replaced" {
		t.Fatalf("replace 后 list 不符: %+v", list.Entries)
	}

	// 命令含换行必须被拒绝，且不能写入第二条任务。
	rejected := call(t, store, logger, "cron.add",
		`{"schedule":"0 4 * * *","command":"echo one\necho injected"}`)
	if rejected.Error == nil || rejected.Error.Code != pluginapi.E_INVALID {
		t.Fatalf("换行命令未被拒绝: %+v", rejected)
	}
	t.Logf("E2E_JSON cron.add(reject) {\"error\":%s}",
		mustJSON(t, rejected.Error))
	content, _ := fake.Content()
	if strings.Contains(content, "echo injected") {
		t.Fatalf("被拒命令仍写入 crontab: %q", content)
	}
	// 包装任务每条占两行（元数据注释 + 任务行），拒绝后仍只保留 1 条任务。
	if count := strings.Count(content, "__task-run"); count != 1 {
		t.Fatalf("拒绝后 crontab 应只保留 1 条任务: %q", content)
	}
}

// call 走与真实请求相同的 dispatch 入口。
func call(t *testing.T, store *croncore.Store, logger *slog.Logger, method, params string) *pluginapi.Response {
	t.Helper()
	request := &pluginapi.Request{
		Type: pluginapi.TypeRequest, ID: "e2e", Method: method, Params: json.RawMessage(params),
	}
	response, shutdown := dispatch(request, config{id: "cron"}, store, logger)
	if shutdown {
		t.Fatalf("%s 意外触发 shutdown", method)
	}
	return response
}

func mustOK(t *testing.T, response *pluginapi.Response) json.RawMessage {
	t.Helper()
	if response.Error != nil {
		t.Fatalf("请求失败: %s: %s", response.Error.Code, response.Error.Message)
	}
	return response.Result
}

func decodeList(t *testing.T, result json.RawMessage) croncore.ListResult {
	t.Helper()
	var parsed croncore.ListResult
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatalf("解析 list 结果失败: %v", err)
	}
	return parsed
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	return string(data)
}
