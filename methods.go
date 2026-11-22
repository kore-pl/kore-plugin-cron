package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/pengtang1024/kore-plugin-cron/internal/croncore"
	"github.com/pengtang1024/kore/pkg/pluginapi"
)

// requestTimeout 是单个请求的处理预算：用 context.WithTimeout，defer cancel 释放。
const requestTimeout = 5 * time.Second

// removeParams / replaceParams 用指针区分“缺省 index”和“显式 index=0”。
type removeParams struct {
	Index *int `json:"index"`
}

type replaceParams struct {
	Index    *int   `json:"index"`
	Schedule string `json:"schedule"`
	Command  string `json:"command"`
}

// dispatch 把请求分发为响应帧；第二个返回值表示收到 kore.shutdown。
func dispatch(request *pluginapi.Request, cfg config, store *croncore.Store, logger *slog.Logger) (*pluginapi.Response, bool) {
	response := &pluginapi.Response{Type: pluginapi.TypeResponse, ID: request.ID, ReqID: request.ReqID}
	switch request.Method {
	case "kore.shutdown":
		response.Result = json.RawMessage(`{"ok":true}`)
		logger.Info("收到 kore.shutdown，优雅退出")
		return response, true
	case "kore.ping":
		response.Result = encode(pingResult())
		return response, false
	case "kore.reload":
		response.Result = json.RawMessage(`{"ok":true,"config_digest":""}`)
		return response, false
	}
	verb, ok := cronVerb(request.Method, cfg.id)
	if !ok {
		response.Error = methodError(pluginapi.E_NOTFOUND, "未知方法: "+request.Method)
		return response, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	if perr := handleCronVerb(ctx, verb, request.Params, store, response); perr != nil {
		response.Error = perr
		response.Result = nil
	}
	return response, false
}

// cronVerb 把 <id>.<verb> 或裸 <verb> 归一为动词名。
func cronVerb(method, id string) (string, bool) {
	verb := strings.TrimPrefix(method, id+".")
	switch verb {
	case "list", "add", "remove", "replace":
		return verb, true
	default:
		return "", false
	}
}

// handleCronVerb 执行 cron 方法，把 croncore 的业务错误映射为协议错误码。
func handleCronVerb(ctx context.Context, verb string, params json.RawMessage, store *croncore.Store, response *pluginapi.Response) *pluginapi.Error {
	switch verb {
	case "list":
		result, err := store.List(ctx)
		if err != nil {
			return mapCoreError(err)
		}
		response.Result = encode(result)
		return nil
	case "add":
		p, perr := parseAddParams(params)
		if perr != nil {
			return perr
		}
		result, err := store.Add(ctx, p)
		if err != nil {
			return mapCoreError(err)
		}
		response.Result = encode(result)
		return nil
	case "remove":
		index, perr := parseRemoveParams(params)
		if perr != nil {
			return perr
		}
		result, err := store.Remove(ctx, index)
		if err != nil {
			return mapCoreError(err)
		}
		response.Result = encode(result)
		return nil
	case "replace":
		p, perr := parseReplaceParams(params)
		if perr != nil {
			return perr
		}
		result, err := store.Replace(ctx, p)
		if err != nil {
			return mapCoreError(err)
		}
		response.Result = encode(result)
		return nil
	default:
		return methodError(pluginapi.E_NOTFOUND, "未知方法: cron."+verb)
	}
}

// parseAddParams 校验 cron.add 入参必须是 JSON 对象。
func parseAddParams(raw json.RawMessage) (croncore.AddParams, *pluginapi.Error) {
	var p croncore.AddParams
	if perr := decodeObject(raw, &p); perr != nil {
		return p, perr
	}
	return p, nil
}

// parseRemoveParams 解析 cron.remove 的必填 index。
func parseRemoveParams(raw json.RawMessage) (int, *pluginapi.Error) {
	var p removeParams
	if perr := decodeObject(raw, &p); perr != nil {
		return 0, perr
	}
	if p.Index == nil {
		return 0, methodError(pluginapi.E_INVALID, "index 必填")
	}
	return *p.Index, nil
}

// parseReplaceParams 解析 cron.replace 的必填 index / schedule / command。
func parseReplaceParams(raw json.RawMessage) (croncore.ReplaceParams, *pluginapi.Error) {
	var p replaceParams
	if perr := decodeObject(raw, &p); perr != nil {
		return croncore.ReplaceParams{}, perr
	}
	if p.Index == nil {
		return croncore.ReplaceParams{}, methodError(pluginapi.E_INVALID, "index 必填")
	}
	return croncore.ReplaceParams{Index: *p.Index, Schedule: p.Schedule, Command: p.Command}, nil
}

// decodeObject 确保 raw 是 JSON 对象并反序列化到 dst。
func decodeObject(raw json.RawMessage, dst any) *pluginapi.Error {
	if len(raw) != 0 && string(raw) != "null" {
		if raw[0] != '{' {
			return methodError(pluginapi.E_INVALID, "params 必须是 JSON 对象")
		}
		if err := json.Unmarshal(raw, dst); err != nil {
			return methodError(pluginapi.E_INVALID, "params 解析失败: "+err.Error())
		}
	}
	return nil
}

// mapCoreError 把 croncore 哨兵错误映射为协议错误码。
func mapCoreError(err error) *pluginapi.Error {
	switch {
	case errors.Is(err, croncore.ErrInvalidParam):
		return methodError(pluginapi.E_INVALID, err.Error())
	case errors.Is(err, croncore.ErrNotFound):
		return methodError(pluginapi.E_NOTFOUND, err.Error())
	case errors.Is(err, croncore.ErrTimeout), errors.Is(err, croncore.ErrInterrupted):
		return methodError(pluginapi.E_UPSTREAM, err.Error())
	default:
		return methodError(pluginapi.E_INTERNAL, err.Error())
	}
}

// methodError 构造协议错误体。
func methodError(code string, msg string) *pluginapi.Error {
	return &pluginapi.Error{Code: code, Message: msg}
}

// encode 序列化结构化结果为 RawMessage。
func encode(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return data
}
