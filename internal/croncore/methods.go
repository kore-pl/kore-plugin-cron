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
	entries := parseCrontab(content).result()
	return &ListResult{Entries: entries, Count: len(entries)}, nil
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

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	content, err := s.readCrontab(ctx)
	if err != nil {
		return nil, err
	}
	parsed := parseCrontab(content)
	parsed.appendEntry(schedule, spec.command, comment)
	backupPath, err := s.commit(ctx, content, joinLines(parsed.lines))
	if err != nil {
		return nil, err
	}
	entry := Entry{
		Index: len(parsed.entries), Type: spec.typ, Schedule: schedule, Command: spec.command,
		Comment: comment, Raw: schedule + " " + spec.command,
	}
	return &AddResult{Type: spec.typ, Entry: &entry, BackupPath: backupPath}, nil
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
