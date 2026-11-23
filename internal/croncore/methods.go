package croncore

import (
	"context"
	"errors"
)

// List 执行 `crontab -l` 并解析当前用户的任务条目；无 crontab 返回空数组。
//
// 权限不足属于环境限制：返回成功 + 空列表 + warning，不映射成服务端故障。
func (s *Store) List(ctx context.Context) (*ListResult, error) {
	if err := s.preflight(ctx, false); err != nil {
		return nil, err
	}
	content, err := s.readCrontab(ctx)
	if errors.Is(err, ErrNoPermission) {
		return &ListResult{
			Entries: []Entry{}, Count: 0,
			Warning: "当前用户无 crontab 权限（crontab -l 报权限错误）",
		}, nil
	}
	if err != nil {
		return nil, err
	}
	layout, layoutOK := binaryLayoutFrom(executablePath())
	entries := parseCrontab(content).resultWith(layout, layoutOK)
	result := &ListResult{Entries: entries, Count: len(entries)}
	if layoutOK {
		result.StablePath = layout.stable
		if fileExists(layout.stable) {
			result.StablePathState = "ready"
		} else {
			result.StablePathState = "missing"
			result.Warning = appendWarning(result.Warning,
				"稳定二进制路径 "+layout.stable+" 不存在（内核未建立 current 链接）：新任务会写入当前版本路径，升级后依赖启动迁移")
		}
	}
	for _, entry := range entries {
		if entry.BinaryStatus == "stale" || entry.BinaryStatus == "missing" {
			result.StaleCount++
		}
	}
	if result.StaleCount > 0 {
		result.Warning = appendWarning(result.Warning,
			itoa(result.StaleCount)+" 条任务指向的二进制不可用或为旧版本（见 entry.binary_status / binary_note）")
	}
	return result, nil
}

// appendWarning 把多条提示拼成一行（用中文分号分隔），避免后一条覆盖前一条。
func appendWarning(current, extra string) string {
	if current == "" {
		return extra
	}
	return current + "；" + extra
}

// Add 校验入参后读现有 crontab、追加条目并整份写回；写前先备份。
//
// type=plugin 不走 crontab：只生成建议的内核 scheduler 任务描述
// （ADR-0019 的边界：cron 插件管系统 crontab，plugin 任务归内核 scheduler）。
func (s *Store) Add(ctx context.Context, params AddParams) (*AddResult, error) {
	typ := normalizeType(params.Type)
	// plugin 类型不落 crontab，因此磁盘满不影响它（只受上下文与"进程被终止"类故障影响）。
	if err := s.preflight(ctx, typ != TypePlugin); err != nil {
		return nil, err
	}
	schedule, err := cleanSchedule(params.Schedule)
	if err != nil {
		return nil, err
	}
	comment, err := cleanComment(params.Comment)
	if err != nil {
		return nil, err
	}
	if typ == TypePlugin {
		suggestion, buildErr := buildPluginTask(params, schedule, comment)
		if buildErr != nil {
			return nil, buildErr
		}
		return &AddResult{
			Type: TypePlugin, SchedulerTask: suggestion,
			Note: "plugin 类型任务由内核 scheduler 执行（ADR-0019），cron 插件没有写入 crontab；请由前端 POST " +
				schedulerTasksEndpoint + " 创建该任务。",
		}, nil
	}
	spec, err := buildTask(params)
	if err != nil {
		return nil, err
	}
	// 包装命令让系统 cron 的每次执行都写 runlog；路径来自插件自身与状态目录，
	// 用户无法控制。命令过长等情况下退化为原始命令（仍可执行，但无逐任务日志）。
	bin := wrapperBinaryPath()
	taskLine, metaLine, taskName, wrapped := buildWrappedTask(bin.path, schedule, spec, comment)
	note := ""
	if !wrapped {
		taskLine = spec.command
		note = "任务已写入 crontab，但当前未启用逐任务执行日志（命令过长或取不到插件自身路径）；cron.run 的立即执行仍会记录"
	} else if bin.layoutOK && !bin.stable {
		note = migrateNote(bin.layout.stable)
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	content, err := s.readCrontab(ctx)
	if err != nil {
		return nil, err
	}
	parsed := parseCrontab(content)
	parsed.appendEntry(schedule, taskLine, comment, metaLine)
	backupPath, err := s.commit(ctx, content, joinLines(parsed.lines))
	if err != nil {
		return nil, err
	}
	entry := Entry{
		Index: len(parsed.entries), Type: spec.typ, Schedule: schedule, Command: spec.command,
		Comment: comment, Raw: schedule + " " + taskLine, Enabled: true,
		TaskName: taskName, Wrapped: wrapped,
	}
	if wrappedBin, _, ok := splitWrapperCommand(taskLine); ok {
		entry.Binary = wrappedBin
		entry.BinaryStatus, entry.BinaryNote = classifyBinary(bin.layout, bin.layoutOK, wrappedBin)
	}
	return &AddResult{
		Type: spec.typ, Entry: &entry, BackupPath: backupPath,
		ScheduledLog: wrapped, Note: note,
	}, nil
}

// Remove 删除第 index 条（0 起）及其紧邻注释；越界返回 ErrInvalidParam。
func (s *Store) Remove(ctx context.Context, index int) (*RemoveResult, error) {
	if err := s.preflight(ctx, true); err != nil {
		return nil, err
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	content, err := s.readCrontab(ctx)
	if err != nil {
		return nil, err
	}
	parsed := parseCrontab(content)
	removed, err := parsed.removeEntry(index)
	if err != nil {
		return nil, err
	}
	backupPath, err := s.commit(ctx, content, joinLines(parsed.lines))
	if err != nil {
		return nil, err
	}
	return &RemoveResult{Removed: removed, BackupPath: backupPath}, nil
}

// Replace 替换第 index 条（0 起）的 schedule 与 command，保留其注释。
func (s *Store) Replace(ctx context.Context, params ReplaceParams) (*ReplaceResult, error) {
	if err := s.preflight(ctx, true); err != nil {
		return nil, err
	}
	schedule, err := cleanSchedule(params.Schedule)
	if err != nil {
		return nil, err
	}
	command, err := cleanCommand(params.Command)
	if err != nil {
		return nil, err
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	content, err := s.readCrontab(ctx)
	if err != nil {
		return nil, err
	}
	parsed := parseCrontab(content)
	entry, err := parsed.replaceEntry(params.Index, schedule, command)
	if err != nil {
		return nil, err
	}
	backupPath, err := s.commit(ctx, content, joinLines(parsed.lines))
	if err != nil {
		return nil, err
	}
	return &ReplaceResult{Entry: entry, BackupPath: backupPath}, nil
}

// SetEnabled 停用/启用第 index 条任务（cron.disable / cron.enable 的共同实现）。
//
// 不删除任务：停用只在任务行前加 "#KORE-DISABLED# " 注释标记，关闭系统 cron
// 的执行路径；启用去掉标记即完整还原。状态本来就一致时 changed=false 且不写盘。
// 需要写盘时走与 add/remove/replace 相同的"先备份、失败回滚"路径。
func (s *Store) SetEnabled(ctx context.Context, index int, enabled bool) (*ToggleResult, error) {
	if err := s.preflight(ctx, true); err != nil {
		return nil, err
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	content, err := s.readCrontab(ctx)
	if err != nil {
		return nil, err
	}
	parsed := parseCrontab(content)
	entry, changed, err := parsed.setEnabled(index, enabled)
	if err != nil {
		return nil, err
	}
	if !changed {
		return &ToggleResult{Entry: entry, Enabled: enabled, Changed: false}, nil
	}
	backupPath, err := s.commit(ctx, content, joinLines(parsed.lines))
	if err != nil {
		return nil, err
	}
	return &ToggleResult{Entry: entry, Enabled: enabled, Changed: true, BackupPath: backupPath}, nil
}
