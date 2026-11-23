package croncore

import (
	"encoding/json"
	"strings"
)

// metaPrefix 是 wrapper 任务的元数据注释前缀。
//
// 任务行会被包装成 `__task-run` 命令，界面需要看到的仍是用户定义的任务
// （类型 / 名称 / 命令）。元数据以紧邻任务行的单行注释保存，crontab 仍是
// 唯一事实来源：删掉注释行只会退化为"按命令形态识别"的普通任务，不会
// 让任务消失。
const metaPrefix = "# kore-cron:"

// metaVersion 是元数据格式版本；不认识的版本按无元数据处理（不猜）。
const metaVersion = 1

// taskMeta 是 wrapper 任务的逻辑定义。
type taskMeta struct {
	V       int    `json:"v"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Command string `json:"command"`
}

// encode 生成单行注释；内容里不会出现换行（command/name 已在写入前校验）。
func (m taskMeta) encode() string {
	data, err := json.Marshal(m)
	if err != nil {
		return ""
	}
	return metaPrefix + string(data)
}

// parseTaskMeta 解析一行注释；不是本插件的元数据或版本不符返回 ok=false。
func parseTaskMeta(line string) (taskMeta, bool) {
	trimmed := strings.TrimLeft(line, " \t")
	if !strings.HasPrefix(trimmed, metaPrefix) {
		return taskMeta{}, false
	}
	var meta taskMeta
	if err := json.Unmarshal([]byte(strings.TrimPrefix(trimmed, metaPrefix)), &meta); err != nil {
		return taskMeta{}, false
	}
	if meta.V != metaVersion || strings.TrimSpace(meta.Command) == "" {
		return taskMeta{}, false
	}
	if !validMetaType(meta.Type) {
		return taskMeta{}, false
	}
	return meta, true
}

// validMetaType 只接受本插件实际会落 crontab 的类型；plugin/backup 不落盘。
func validMetaType(typ string) bool {
	switch typ {
	case TypeShell, TypeHTTP:
		return true
	default:
		return false
	}
}
