package croncore

import (
	"encoding/json"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// httpTaskPrefix 是生成的 curl 命令固定前缀；Entry.Type 的识别与 cron.logs
// 的过滤都依赖它保持稳定。改动它等于迁移已有任务的类型识别，必须同步 CHANGELOG。
const httpTaskPrefix = "curl -sS -m 30 -o /dev/null "

// 任务参数边界：URL / body / header 都有长度上限，防止把 crontab 写成日志炸弹。
const (
	maxURLLen       = 2048
	maxBodyLen      = 4096
	maxHeaderCount  = 16
	maxHeaderKeyLen = 64
	maxHeaderValLen = 1024
	maxTaskNameLen  = 64
	// httpTaskTimeoutS 是生成命令里 curl 的硬超时（-m 30）。
	httpTaskTimeoutS = 30
	// schedulerTaskTimeoutS 是建议的内核 scheduler 任务超时（ADR-0019 默认 300s）。
	schedulerTaskTimeoutS = 300
	// schedulerTasksEndpoint 是内核定时任务端点（前端据此创建 plugin 类型任务）。
	schedulerTasksEndpoint = "/api/v1/scheduler/tasks"
)

// pluginIDPattern / pluginMethodPattern 与内核 scheduler 的同口径校验保持一致：
// 插件 id 小写字母开头、可含数字与连字符；方法名点分、每段小写字母开头。
var (
	pluginIDPattern     = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	pluginMethodPattern = regexp.MustCompile(`^[a-z][A-Za-z0-9_-]*(\.[a-z][A-Za-z0-9_-]*)*$`)
	headerKeyPattern    = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
)

// httpMethods 是允许的 HTTP 方法白名单：只放常用幂等/表单语义方法，
// 不放 CONNECT/TRACE 这类与"计划任务"无关、易被滥用的方法。
var httpMethods = map[string]bool{
	"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true, "HEAD": true,
}

// taskSpec 是校验后的任务定义：command 要写入 crontab 的行内容。
type taskSpec struct {
	typ     string
	command string
}

// buildTask 按 type 校验入参并生成要写入 crontab 的命令。
// plugin 类型不生成命令（不落 crontab），由 buildPluginTask 单独处理。
func buildTask(p AddParams) (taskSpec, error) {
	typ := normalizeType(p.Type)
	switch typ {
	case TypeShell:
		command, err := cleanCommand(p.Command)
		if err != nil {
			return taskSpec{}, err
		}
		return taskSpec{typ: TypeShell, command: command}, nil
	case TypeHTTP:
		command, err := buildHTTPCommand(p)
		if err != nil {
			return taskSpec{}, err
		}
		return taskSpec{typ: TypeHTTP, command: command}, nil
	default:
		return taskSpec{}, wrap(ErrInvalidParam, "type 必须是 shell / http / plugin")
	}
}

// normalizeType 把空类型归一为 shell（向后兼容老调用方），并统一小写。
func normalizeType(typ string) string {
	typ = strings.ToLower(strings.TrimSpace(typ))
	if typ == "" {
		return TypeShell
	}
	return typ
}

// buildHTTPCommand 把 url/method/headers/body 生成为固定 argv 的 curl 命令。
//
// 所有可变部分都用单引号包裹，内部单引号按 shell 的常见写法转义，因此即使值里带空格、
// 分号、$、反引号也不会被 /bin/sh 解释成命令——crontab 行本身是 shell 语法，
// 这是必须的防护。换行/回车/NUL 一律拒绝：它们会破坏 crontab 的行结构。
func buildHTTPCommand(p AddParams) (string, error) {
	rawURL := strings.TrimSpace(p.URL)
	if rawURL == "" {
		return "", wrap(ErrInvalidParam, "type=http 时 url 必填")
	}
	if err := checkNoControl("url", rawURL, maxURLLen); err != nil {
		return "", err
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", wrap(ErrInvalidParam, "url 解析失败: "+err.Error())
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", wrap(ErrInvalidParam, "url 只支持 http / https")
	}
	if parsed.Host == "" {
		return "", wrap(ErrInvalidParam, "url 缺少主机名")
	}
	method := strings.ToUpper(strings.TrimSpace(p.Method))
	if method == "" {
		method = "GET"
	}
	if !httpMethods[method] {
		return "", wrap(ErrInvalidParam, "method 只支持 GET / POST / PUT / PATCH / DELETE / HEAD")
	}
	command := httpTaskPrefix + "-X " + method
	headers, err := buildHeaderArgs(p.Headers)
	if err != nil {
		return "", err
	}
	command += headers
	if p.Body != "" {
		if err := checkNoControl("body", p.Body, maxBodyLen); err != nil {
			return "", err
		}
		command += " --data-raw " + shellQuote(p.Body)
	}
	command += " " + shellQuote(rawURL)
	if len(command) > maxGeneratedCommandLen {
		return "", wrap(ErrInvalidParam, "生成的命令超过 "+itoa(maxGeneratedCommandLen)+" 字节，请缩短 body 或 headers")
	}
	return command, nil
}

// maxGeneratedCommandLen 是 http 任务生成命令的长度上限：比 shell 命令的 512
// 字节更宽（要容纳 body），但仍远小于一行的合理长度。
const maxGeneratedCommandLen = 8192

// buildHeaderArgs 生成按 key 排序的 -H 参数，保证同一组 headers 的命令逐字一致
// （可复现、可做类型识别、测试可断言）。
func buildHeaderArgs(headers map[string]string) (string, error) {
	if len(headers) == 0 {
		return "", nil
	}
	if len(headers) > maxHeaderCount {
		return "", wrap(ErrInvalidParam, "headers 最多 "+itoa(maxHeaderCount)+" 个")
	}
	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var builder strings.Builder
	for _, key := range keys {
		if !headerKeyPattern.MatchString(key) || len(key) > maxHeaderKeyLen {
			return "", wrap(ErrInvalidParam, "header 名非法: "+key)
		}
		value := headers[key]
		if err := checkNoControl("header "+key, value, maxHeaderValLen); err != nil {
			return "", err
		}
		builder.WriteString(" -H ")
		builder.WriteString(shellQuote(key + ": " + value))
	}
	return builder.String(), nil
}

// checkNoControl 校验字符串不含换行/回车/NUL 与其它控制字符，并限制长度。
// 控制字符是 crontab 行注入与终端转义注入的共同入口，一律拒绝。
func checkNoControl(field, value string, maxLen int) error {
	if strings.ContainsAny(value, "\r\n\x00") {
		return wrap(ErrInvalidParam, field+" 不得包含换行、回车或 NUL")
	}
	for i := 0; i < len(value); i++ {
		if value[i] < 0x20 && value[i] != '\t' {
			return wrap(ErrInvalidParam, field+" 不得包含控制字符")
		}
	}
	if len(value) > maxLen {
		return wrap(ErrInvalidParam, field+" 超过 "+itoa(maxLen)+" 字节")
	}
	return nil
}

// shellQuote 用单引号包裹 shell 参数，并转义内部单引号。
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// buildPluginTask 校验 plugin 类型入参并生成"建议的内核 scheduler 任务"。
//
// 这里不读写 crontab：plugin 任务归内核 scheduler（ADR-0019），cron 插件只
// 生成描述，由前端 POST /api/v1/scheduler/tasks 创建。
func buildPluginTask(p AddParams, schedule string, comment string) (*SchedulerTaskSuggestion, error) {
	plugin := strings.TrimSpace(p.Plugin)
	if !pluginIDPattern.MatchString(plugin) {
		return nil, wrap(ErrInvalidParam, "type=plugin 时 plugin 必填且须匹配 ^[a-z][a-z0-9-]*$")
	}
	method := strings.TrimSpace(p.Method)
	if !pluginMethodPattern.MatchString(method) {
		return nil, wrap(ErrInvalidParam, "type=plugin 时 method 必填且须为点分方法名（如 echo.ping）")
	}
	params, err := normalizeTaskParams(p.Params)
	if err != nil {
		return nil, err
	}
	name := comment
	if name == "" {
		name = plugin + "." + method
	}
	if len(name) > maxTaskNameLen {
		return nil, wrap(ErrInvalidParam, "任务名（comment）超过 "+itoa(maxTaskNameLen)+" 字符")
	}
	return &SchedulerTaskSuggestion{
		Name: name, Plugin: plugin, Method: method, Params: params,
		Schedule: schedule, TimeoutS: schedulerTaskTimeoutS, Enabled: true,
		Endpoint: schedulerTasksEndpoint,
	}, nil
}

// normalizeTaskParams 把空值归一为 {}，非对象 JSON 一律拒绝（与内核同口径）。
func normalizeTaskParams(raw json.RawMessage) (json.RawMessage, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return json.RawMessage(`{}`), nil
	}
	if !json.Valid([]byte(trimmed)) || trimmed[0] != '{' {
		return nil, wrap(ErrInvalidParam, "type=plugin 时 params 必须是 JSON 对象")
	}
	return json.RawMessage(trimmed), nil
}

// inferType 按命令形态识别任务类型。识别是尽力而为：只有本插件生成的
// curl 命令带固定前缀，用户自己写的同形命令也会被识别为 http。
func inferType(command string) string {
	if strings.HasPrefix(command, httpTaskPrefix) {
		return TypeHTTP
	}
	return TypeShell
}
