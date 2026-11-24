package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pengtang1024/kore-plugin-cron/internal/croncore"
	"github.com/pengtang1024/kore-plugin-cron/internal/plugincfg"
	"github.com/pengtang1024/kore/pkg/pluginapi"
)

// testConfig 构造一份指向临时状态目录的契约测试配置，返回 store 路径。
func testConfig(t *testing.T) (*config, string) {
	t.Helper()
	stateDir := t.TempDir()
	return &config{
		id:     "cron",
		store:  plugincfg.NewStore(stateDir),
		values: defaultValues(),
		source: plugincfg.SourceDefault,
	}, filepath.Join(stateDir, "config.json")
}

// TestInfoJsonDeclaresConfigContract 守护 PLUGIN-CONFIG §1 + 方法级能力：
// methods 含两个方法且 config.get=system:read / config.set=system:admin。
func TestInfoJsonDeclaresConfigContract(t *testing.T) {
	raw, err := os.ReadFile("info.json")
	if err != nil {
		t.Fatalf("读取 info.json 失败: %v", err)
	}
	var info struct {
		Methods            []string          `json:"methods"`
		MethodCapabilities map[string]string `json:"method_capabilities"`
	}
	if err := json.Unmarshal(raw, &info); err != nil {
		t.Fatalf("解析 info.json 失败: %v", err)
	}
	declared := map[string]bool{}
	for _, method := range info.Methods {
		declared[method] = true
	}
	for method, capability := range map[string]string{
		"cron.config.get": "system:read",
		"cron.config.set": "system:admin",
	} {
		if !declared[method] {
			t.Errorf("methods 未声明 %s", method)
		}
		if got := info.MethodCapabilities[method]; got != capability {
			t.Errorf("%s 能力应为 %s，实际 %q", method, capability, got)
		}
	}
}

// TestConfigGetSchema 校验 PLUGIN-CONFIG §2：schema.title + fields 每项必填属性齐备、
// type 在枚举内、default 类型与 type 一致；4 个字段均来自本仓真实能力。
func TestConfigGetSchema(t *testing.T) {
	cfg, _ := testConfig(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := croncore.NewStore()

	var result plugincfg.GetResult
	if err := json.Unmarshal(mustOK(t, callWithConfig(t, cfg, store, logger, "cron.config.get", `{}`)), &result); err != nil {
		t.Fatalf("解析 config.get 结果失败: %v", err)
	}
	if result.Schema.Title == "" || len(result.Schema.Fields) == 0 {
		t.Fatalf("schema 必须有 title 与非空 fields: %+v", result.Schema)
	}
	if err := plugincfg.ValidateSchema(result.Schema); err != nil {
		t.Fatalf("schema 自检失败: %v", err)
	}
	wantKeys := map[string]string{
		"backup_dir": plugincfg.TypeString, "runlog_dir": plugincfg.TypeString,
		"log_file": plugincfg.TypeString, "disable_journal": plugincfg.TypeBool,
	}
	for _, field := range result.Schema.Fields {
		wantType, ok := wantKeys[field.Key]
		if !ok {
			t.Errorf("出现臆造字段 %q", field.Key)
			continue
		}
		if field.Type != wantType {
			t.Errorf("字段 %s type=%q，应为 %q", field.Key, field.Type, wantType)
		}
		if field.Label == "" || field.Help == "" {
			t.Errorf("字段 %s 缺 label/help", field.Key)
		}
	}
	if len(result.Schema.Fields) != len(wantKeys) {
		t.Errorf("字段数应为 %d，实际 %d", len(wantKeys), len(result.Schema.Fields))
	}
	if result.Source != plugincfg.SourceDefault {
		t.Errorf("无文件/环境变量时 source 应为 default，实际 %q", result.Source)
	}
}

// TestConfigSetPersistsAndApplies 守护 §3/§4/§5：校验 → 0600 原子写 → 更新内存
// → source=file、restart_required=false；并把值写回 croncore 读的环境变量，
// 下一次 cron 业务调用立即看到新目录（真热生效，不重启进程）。
func TestConfigSetPersistsAndApplies(t *testing.T) {
	cfg, configPath := testConfig(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := croncore.NewStore()

	dir := t.TempDir()
	backupDir := filepath.Join(dir, "backup")
	runlogDir := filepath.Join(dir, "runlog")
	logFile := filepath.Join(dir, "cron.log")
	params := `{"values":{"backup_dir":` + quote(backupDir) + `,"runlog_dir":` + quote(runlogDir) +
		`,"log_file":` + quote(logFile) + `,"disable_journal":true}}`
	var setResult plugincfg.SetResult
	if err := json.Unmarshal(mustOK(t, callWithConfig(t, cfg, store, logger, "cron.config.set", params)), &setResult); err != nil {
		t.Fatalf("解析 config.set 结果失败: %v", err)
	}
	if !setResult.Saved || !setResult.Applied || setResult.RestartRequired {
		t.Fatalf("config.set 结果应为 saved+applied+热生效: %+v", setResult)
	}
	if len(setResult.Warnings) != 0 {
		t.Fatalf("无告警期望空数组: %+v", setResult.Warnings)
	}

	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatalf("配置文件未落盘: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("config.json 权限应为 0600，实际 %o", info.Mode().Perm())
	}
	raw, err := os.ReadFile(configPath)
	if err != nil || !json.Valid(raw) {
		t.Fatalf("config.json 不是合法 JSON: %v", err)
	}

	// 下一次 get：source=file 且回传刚保存的值。
	var got plugincfg.GetResult
	if err := json.Unmarshal(mustOK(t, callWithConfig(t, cfg, store, logger, "config.get", `{}`)), &got); err != nil {
		t.Fatalf("解析 get 失败: %v", err)
	}
	if got.Source != plugincfg.SourceFile {
		t.Errorf("保存后 source 应为 file，实际 %q", got.Source)
	}
	if got.Values == nil || !strings.Contains(string(got.Values), backupDir) {
		t.Errorf("get 未回传保存值: %s", got.Values)
	}

	// 热生效判据：环境变量已被更新，croncore 的目录解析直接反映新值。
	if os.Getenv(envBackupDir) != backupDir || os.Getenv(envRunlogDir) != runlogDir || os.Getenv(envLogFile) != logFile {
		t.Errorf("croncore 读取的环境变量未更新: backup=%q runlog=%q log=%q",
			os.Getenv(envBackupDir), os.Getenv(envRunlogDir), os.Getenv(envLogFile))
	}
	if os.Getenv(envDisableJournal) != "1" {
		t.Errorf("disable_journal 未生效: %q", os.Getenv(envDisableJournal))
	}
}

// TestConfigSetRejectsInvalidKeepsOld 守护 §8：非法值返回 E_INVALID 且旧配置仍可用、不写半份文件。
func TestConfigSetRejectsInvalidKeepsOld(t *testing.T) {
	cfg, configPath := testConfig(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := croncore.NewStore()

	// 先存一份合法的。
	good := `{"values":{"backup_dir":"/var/lib/kore/cron-backup"}}`
	if resp := callWithConfig(t, cfg, store, logger, "cron.config.set", good); resp.Error != nil {
		t.Fatalf("合法保存失败: %+v", resp.Error)
	}
	before, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("读配置文件失败: %v", err)
	}

	bad := callWithConfig(t, cfg, store, logger, "cron.config.set", `{"values":{"backup_dir":"relative/path"}}`)
	if bad.Error == nil || bad.Error.Code != pluginapi.E_INVALID {
		t.Fatalf("非绝对路径应 E_INVALID: %+v", bad)
	}
	if !strings.Contains(bad.Error.Message, "backup_dir") || !strings.Contains(bad.Error.Message, "绝对路径") {
		t.Errorf("错误信息应指明哪项错+怎么改: %q", bad.Error.Message)
	}
	after, err := os.ReadFile(configPath)
	if err != nil || string(after) != string(before) {
		t.Errorf("非法 set 不应改盘: before=%s after=%s err=%v", before, after, err)
	}
	var got plugincfg.GetResult
	if err := json.Unmarshal(mustOK(t, callWithConfig(t, cfg, store, logger, "config.get", `{}`)), &got); err != nil {
		t.Fatalf("旧配置应仍可读: %v", err)
	}
	if got.Source != plugincfg.SourceFile || !strings.Contains(string(got.Values), "/var/lib/kore/cron-backup") {
		t.Errorf("非法 set 后旧配置应仍可用: %+v", got)
	}
	// 缺 values 也必须 E_INVALID。
	if resp := callWithConfig(t, cfg, store, logger, "cron.config.set", `{}`); resp.Error == nil || resp.Error.Code != pluginapi.E_INVALID {
		t.Errorf("缺 values 应 E_INVALID: %+v", resp)
	}
}

// TestConfigGetEnvSource 守护 §4：仅有环境变量时 source=env。
func TestConfigGetEnvSource(t *testing.T) {
	t.Setenv(envBackupDir, "/tmp/kore-env-backup")
	cfg, _ := testConfig(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := croncore.NewStore()

	// 模拟启动期解析：默认 → env。
	values, source, err := resolveValues(cfg.store)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	cfg.values, cfg.source = values, source
	if source != plugincfg.SourceEnv {
		t.Fatalf("source 应为 env，实际 %q", source)
	}
	var result plugincfg.GetResult
	if err := json.Unmarshal(mustOK(t, callWithConfig(t, cfg, store, logger, "cron.config.get", `{}`)), &result); err != nil {
		t.Fatalf("解析 get 失败: %v", err)
	}
	if result.Source != plugincfg.SourceEnv {
		t.Errorf("config.get source 应为 env，实际 %q", result.Source)
	}
}

// TestSensitiveContract 守护 §6：sensitive 字段 get 回 "***"、set 收到 "***" 表示不修改。
// cron 当前无可配凭据字段，用契约库的掩码能力做机制级覆盖，避免未来新增凭据字段时退化。
func TestSensitiveContract(t *testing.T) {
	schema := plugincfg.Schema{
		Title: "凭据测试",
		Fields: []plugincfg.Field{
			{Key: "password", Label: "口令", Type: plugincfg.TypeString, Default: json.RawMessage(`""`), Required: false, Help: "测试用", Sensitive: true},
		},
	}
	masked, err := plugincfg.MaskValues(schema, json.RawMessage(`{"password":"super-secret"}`))
	if err != nil {
		t.Fatalf("掩码失败: %v", err)
	}
	if strings.Contains(string(masked), "super-secret") || !strings.Contains(string(masked), plugincfg.SensitivePlaceholder) {
		t.Errorf("敏感字段不应回传明文: %s", masked)
	}
	if !plugincfg.IsSensitivePlaceholder(json.RawMessage(`"***"`)) {
		t.Error(`"***" 应识别为「不修改」占位`)
	}
	if plugincfg.IsSensitivePlaceholder(json.RawMessage(`"new-secret"`)) {
		t.Error("新明文不应被识别为占位")
	}
}

// callWithConfig 以给定 cfg 走 dispatch 入口（config.* 需要携带可变配置）。
func callWithConfig(t *testing.T, cfg *config, store *croncore.Store, logger *slog.Logger, method, params string) *pluginapi.Response {
	t.Helper()
	request := &pluginapi.Request{
		Type: pluginapi.TypeRequest, ID: "cfg-test", Method: method, Params: json.RawMessage(params),
	}
	response, shutdown := dispatch(request, cfg, store, logger)
	if shutdown {
		t.Fatalf("%s 意外触发 shutdown", method)
	}
	return response
}

// quote 返回 Go 字符串的 JSON 字面量（测试拼参用）。
func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
