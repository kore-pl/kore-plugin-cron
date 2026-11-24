package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pengtang1024/kore-plugin-cron/internal/plugincfg"
	"github.com/pengtang1024/kore/pkg/pluginapi"
)

// embeddedInfo 是随二进制发布的内嵌清单 manifest.json（只含握手字段，不含 size_bytes）。
//
//go:embed manifest.json
var embeddedInfo []byte

// manifest 是 info.json 中插件运行期需要的字段。
type manifest struct {
	ID           string   `json:"id"`
	Version      string   `json:"version"`
	Protocol     string   `json:"protocol"`
	RequiresKore string   `json:"requires_kore"`
	Capabilities []string `json:"capabilities"`
}

// 配置契约的可配项键名，与 croncore 真实读取的环境变量一一对应。
const (
	keyBackupDir      = "backup_dir"
	keyRunlogDir      = "runlog_dir"
	keyLogFile        = "log_file"
	keyDisableJournal = "disable_journal"

	envBackupDir      = "KORE_CRON_BACKUP_DIR"
	envRunlogDir      = "KORE_CRON_RUNLOG_DIR"
	envLogFile        = "KORE_CRON_LOG_FILE"
	envDisableJournal = "KORE_CRON_DISABLE_JOURNAL"
)

// fileValues 是 config.json / 环境变量/默认值解析出的全部可配项。
// 每项都是显式标量，不用 object / any（G-24）。
type fileValues struct {
	BackupDir      string `json:"backup_dir"`
	RunlogDir      string `json:"runlog_dir"`
	LogFile        string `json:"log_file"`
	DisableJournal bool   `json:"disable_journal"`
}

// config 是运行期配置：环境变量 + 内嵌清单 + 插件配置契约。
//
// values / source 只在 serve 会话内经 config.set 变更，不引入 goroutine，
// 无需加锁：插件会话循环单 goroutine 串行读请求、串行 dispatch。
type config struct {
	socket       string
	id           string
	token        string
	version      string
	requiresKore string
	capabilities []string
	startedAt    time.Time
	store        *plugincfg.Store
	values       fileValues
	source       string
}

// defaultValues 是代码默认值：四项均为空/关，等价于「插件不干预 croncore 的既有默认解析」。
// 不臆造日志保留天数 / 默认用户 / 时区 / 并发上限——本仓没有这些能力。
func defaultValues() fileValues {
	return fileValues{}
}

// configSchema 是 config.get 返回的表单描述（PLUGIN-CONFIG §2）。
func configSchema() plugincfg.Schema {
	return plugincfg.Schema{
		Title: "计划任务插件设置",
		Fields: []plugincfg.Field{
			{
				Key: keyBackupDir, Label: "任务备份目录", Type: plugincfg.TypeString,
				Default: schemaJSON(""), Required: false,
				Help:        "crontab 写前备份的落盘目录，须为绝对路径；留空则用 <状态目录>/cron-backup。",
				Placeholder: "/var/lib/kore/cron-backup",
			},
			{
				Key: keyRunlogDir, Label: "执行日志目录", Type: plugincfg.TypeString,
				Default: schemaJSON(""), Required: false,
				Help:        "逐任务执行日志 runlog.jsonl 的目录，须为绝对路径；留空则用 <状态目录>/runlog。",
				Placeholder: "/var/lib/kore/runlog",
			},
			{
				Key: keyLogFile, Label: "系统 cron 日志文件", Type: plugincfg.TypeString,
				Default: schemaJSON(""), Required: false,
				Help:        "cron.log 读取的日志文件，须为绝对路径；留空则依次尝试 journalctl 与 /var/log/cron.log。",
				Placeholder: "/var/log/cron.log",
			},
			{
				Key: keyDisableJournal, Label: "禁用 journalctl 读取", Type: plugincfg.TypeBool,
				Default: schemaJSON(false), Required: false,
				Help: "开启后 cron.log 不再调用 journalctl -u cron，只读日志文件；journalctl 不可用或很慢时建议打开。",
			},
		},
	}
}

// loadConfig 读取协议环境变量与配置契约，解析内嵌 manifest.json。
func loadConfig() (config, error) {
	var m manifest
	if err := json.Unmarshal(embeddedInfo, &m); err != nil {
		return config{}, fmt.Errorf("解析内嵌 manifest.json: %w", err)
	}
	if m.Protocol != pluginapi.Protocol {
		return config{}, fmt.Errorf("info.json.protocol=%q 与 SDK 协议 %q 不一致", m.Protocol, pluginapi.Protocol)
	}
	if err := plugincfg.ValidateSchema(configSchema()); err != nil {
		return config{}, fmt.Errorf("config schema 非法: %w", err)
	}
	store := plugincfg.NewStore(os.Getenv("KORE_STATE_DIR"))
	values, source, err := resolveValues(store)
	if err != nil {
		return config{}, err
	}
	cfg := config{
		socket:       os.Getenv("KORE_PLUGIN_SOCK"),
		id:           os.Getenv("KORE_PLUGIN_ID"),
		token:        os.Getenv("KORE_PLUGIN_TOKEN"),
		version:      m.Version,
		requiresKore: m.RequiresKore,
		capabilities: m.Capabilities,
		startedAt:    time.Now(),
		store:        store,
		values:       values,
		source:       source,
	}
	if cfg.id == "" {
		cfg.id = m.ID
	}
	if cfg.socket == "" {
		return config{}, fmt.Errorf("缺少环境变量 KORE_PLUGIN_SOCK")
	}
	// 启动加载后立即把生效值写回环境，让 croncore 的既有逐次 os.Getenv 读取
	// 与配置契约保持同一真源（file > env > default）。
	cfg.applyToEnv()
	return cfg, nil
}

// resolveValues 实现优先级 config.json > 环境变量 > 代码默认值（PLUGIN-CONFIG §4）。
func resolveValues(store *plugincfg.Store) (fileValues, string, error) {
	if store.Exists() {
		raw, _, err := store.Read()
		if err != nil {
			return fileValues{}, "", err
		}
		var values fileValues
		if err := json.Unmarshal(raw, &values); err != nil {
			return fileValues{}, "", fmt.Errorf("解析插件配置 %s: %w", store.Path(), err)
		}
		normalized := fillDefaults(values)
		if msg := validateValues(normalized); msg != "" {
			return fileValues{}, "", fmt.Errorf("插件配置 %s 非法: %s", store.Path(), msg)
		}
		return normalized, plugincfg.SourceFile, nil
	}

	values := defaultValues()
	source := plugincfg.SourceDefault
	if raw := strings.TrimSpace(os.Getenv(envBackupDir)); raw != "" {
		values.BackupDir = raw
		source = plugincfg.SourceEnv
	}
	if raw := strings.TrimSpace(os.Getenv(envRunlogDir)); raw != "" {
		values.RunlogDir = raw
		source = plugincfg.SourceEnv
	}
	if raw := strings.TrimSpace(os.Getenv(envLogFile)); raw != "" {
		values.LogFile = raw
		source = plugincfg.SourceEnv
	}
	if raw := strings.TrimSpace(os.Getenv(envDisableJournal)); raw != "" {
		values.DisableJournal = raw == "1" || strings.EqualFold(raw, "true")
		source = plugincfg.SourceEnv
	}
	if msg := validateValues(values); msg != "" {
		return fileValues{}, "", fmt.Errorf("环境变量配置非法: %s", msg)
	}
	return values, source, nil
}

// fillDefaults 把零值字段补成代码默认值（config.json 允许只写关心的字段）。
func fillDefaults(values fileValues) fileValues {
	defaults := defaultValues()
	if strings.TrimSpace(values.BackupDir) == "" {
		values.BackupDir = defaults.BackupDir
	}
	if strings.TrimSpace(values.RunlogDir) == "" {
		values.RunlogDir = defaults.RunlogDir
	}
	if strings.TrimSpace(values.LogFile) == "" {
		values.LogFile = defaults.LogFile
	}
	return values
}

// validateValues 校验配置值；启动加载与 config.set 共用同一套规则。
// 路径项必须为空或绝对路径（croncore 的相关解析都假定绝对路径），
// 返回空串表示通过。
func validateValues(values fileValues) string {
	paths := []struct {
		key   string
		value string
	}{
		{keyBackupDir, values.BackupDir},
		{keyRunlogDir, values.RunlogDir},
		{keyLogFile, values.LogFile},
	}
	for _, item := range paths {
		if item.value == "" {
			continue
		}
		if !filepath.IsAbs(item.value) {
			return fmt.Sprintf("%s 必须是绝对路径（当前 %q）", item.key, item.value)
		}
	}
	return ""
}

// applyToEnv 把生效值写回 croncore 读取的环境变量，使 config.set 后下一次业务调用即用新值。
// 四项对应 croncore 在每次方法调用时读取的开关，因此 restart_required=false。
func (c *config) applyToEnv() {
	setOrUnset(envBackupDir, c.values.BackupDir)
	setOrUnset(envRunlogDir, c.values.RunlogDir)
	setOrUnset(envLogFile, c.values.LogFile)
	if c.values.DisableJournal {
		_ = os.Setenv(envDisableJournal, "1")
	} else {
		_ = os.Unsetenv(envDisableJournal)
	}
}

// setOrUnset 空值等价于「不设置」，让 croncore 回到自己的默认解析。
func setOrUnset(name, value string) {
	if value == "" {
		_ = os.Unsetenv(name)
		return
	}
	_ = os.Setenv(name, value)
}

// mustJSON 把标量编成 json.RawMessage（schema 的 default 字段）。
func schemaJSON(value any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage(`null`)
	}
	return encoded
}

// configGet 组装 config.get 结果：schema + 当前值 + 来源（PLUGIN-CONFIG §1）。
func configGet(c *config) (json.RawMessage, *pluginapi.Error) {
	values, err := json.Marshal(c.values)
	if err != nil {
		return nil, &pluginapi.Error{Code: pluginapi.E_INTERNAL, Message: "序列化当前配置失败: " + err.Error()}
	}
	// 本仓四项均为非凭据字段，无 sensitive 项；仍走掩码函数保持契约统一，
	// 未来若新增凭据字段，config.get 自动不回传原值。
	masked, err := plugincfg.MaskValues(configSchema(), values)
	if err != nil {
		return nil, &pluginapi.Error{Code: pluginapi.E_INTERNAL, Message: "掩码配置值失败: " + err.Error()}
	}
	encoded, err := json.Marshal(plugincfg.GetResult{Schema: configSchema(), Values: masked, Source: c.source})
	if err != nil {
		return nil, &pluginapi.Error{Code: pluginapi.E_INTERNAL, Message: "序列化 config.get 结果失败: " + err.Error()}
	}
	return encoded, nil
}

// configSet 按「校验 → 原子写 → 更新内存 → 返回 applied」顺序保存配置（PLUGIN-CONFIG §3）。
func configSet(raw json.RawMessage, c *config) (json.RawMessage, *pluginapi.Error) {
	var params plugincfg.SetParams
	if len(raw) == 0 || string(raw) == "null" {
		return nil, &pluginapi.Error{Code: pluginapi.E_INVALID, Message: "缺少必填字段 values"}
	}
	if raw[0] != '{' {
		return nil, &pluginapi.Error{Code: pluginapi.E_INVALID, Message: "params 必须是 JSON 对象"}
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, &pluginapi.Error{Code: pluginapi.E_INVALID, Message: "params 解析失败: " + err.Error()}
	}
	if len(params.Values) == 0 || string(params.Values) == "null" {
		return nil, &pluginapi.Error{Code: pluginapi.E_INVALID, Message: "缺少必填字段 values"}
	}
	// 以当前值为基线做局部更新：未提供的字段保持原值。
	candidate := c.values
	if err := json.Unmarshal(params.Values, &candidate); err != nil {
		return nil, &pluginapi.Error{Code: pluginapi.E_INVALID, Message: "values 解析失败: " + err.Error()}
	}
	candidate = fillDefaults(candidate)
	if msg := validateValues(candidate); msg != "" {
		return nil, &pluginapi.Error{Code: pluginapi.E_INVALID, Message: msg}
	}
	if c.store == nil || c.store.Path() == "" {
		return nil, &pluginapi.Error{
			Code:    pluginapi.E_INTERNAL,
			Message: "KORE_STATE_DIR 未设置，无法保存插件配置",
			Detail:  json.RawMessage(`{"hint":"插件由内核启动时会注入 KORE_STATE_DIR；本地单独调试请先设置该环境变量。"}`),
		}
	}
	payload, err := json.Marshal(candidate)
	if err != nil {
		return nil, &pluginapi.Error{Code: pluginapi.E_INTERNAL, Message: "序列化配置失败: " + err.Error()}
	}
	if err := c.store.Write(payload); err != nil {
		return nil, &pluginapi.Error{
			Code:    pluginapi.E_INTERNAL,
			Message: "保存插件配置失败: " + err.Error(),
			Detail:  json.RawMessage(`{"hint":"检查 KORE_STATE_DIR 是否可写（目录需 0700 且属主为运行用户）。"}`),
		}
	}
	c.values = candidate
	c.source = plugincfg.SourceFile
	c.applyToEnv() // 热生效：croncore 每次方法调用都读这些环境变量。
	encoded, err := json.Marshal(plugincfg.SetResult{Saved: true, Applied: true, RestartRequired: false, Warnings: []string{}})
	if err != nil {
		return nil, &pluginapi.Error{Code: pluginapi.E_INTERNAL, Message: "序列化 config.set 结果失败: " + err.Error()}
	}
	return encoded, nil
}
