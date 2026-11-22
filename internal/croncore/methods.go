package croncore

import (
	"context"
	"encoding/json"
)

// List 返回任务定义、执行记录与日志的结构化快照。
func (s *Store) List(ctx context.Context) (*ListResult, error) {
	if err := s.preflight(ctx, false); err != nil {
		return nil, err
	}
	jobs := append([]Job(nil), s.jobs...)
	execs := append([]Execution(nil), s.executions...)
	logs := append([]LogEntry(nil), s.logs...)
	return &ListResult{Jobs: jobs, Executions: execs, Logs: logs, Count: len(jobs)}, nil
}

// Add 新增任务定义：校验 name / schedule / command，纯定义不调度。
func (s *Store) Add(ctx context.Context, p AddParams) (*AddResult, error) {
	if err := s.preflight(ctx, true); err != nil {
		return nil, err
	}
	if err := ValidateName(p.Name); err != nil {
		return nil, err
	}
	if err := ValidateSchedule(p.Schedule); err != nil {
		return nil, err
	}
	if p.Command == "" || len(p.Command) > maxCmdLen {
		return nil, wrap(ErrInvalidParam, "command 必须为 1-4096 字符")
	}
	enabled := true
	if p.Enabled != nil {
		enabled = *p.Enabled
	}
	for _, j := range s.jobs {
		if j.Name == p.Name {
			return &AddResult{Name: p.Name, Schedule: p.Schedule,
				Command: p.Command, Enabled: enabled, Duplicate: true}, nil
		}
	}
	job := Job{Name: p.Name, Schedule: p.Schedule, Command: p.Command, Enabled: enabled}
	if err := s.persist(job); err != nil {
		return nil, err
	}
	s.jobs = append(s.jobs, job)
	s.appendLog(p.Name, "INFO", "任务定义已添加（不调度）")
	return &AddResult{Name: p.Name, Schedule: p.Schedule, Command: p.Command, Enabled: enabled}, nil
}

// Remove 删除任务定义；不存在返回 ErrNotFound。
func (s *Store) Remove(ctx context.Context, name string) (*RemoveResult, error) {
	if err := s.preflight(ctx, true); err != nil {
		return nil, err
	}
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	for i, j := range s.jobs {
		if j.Name == name {
			if err := s.persistRemove(name); err != nil {
				return nil, err
			}
			s.jobs = append(s.jobs[:i], s.jobs[i+1:]...)
			s.appendLog(name, "INFO", "任务定义已删除")
			return &RemoveResult{Name: name, Removed: true}, nil
		}
	}
	return nil, wrap(ErrNotFound, name)
}

// RecordExecution 由宿主上报执行结果写入记录与日志，本方法不产生执行动作。
func (s *Store) RecordExecution(ctx context.Context, e Execution) error {
	if err := s.preflight(ctx, true); err != nil {
		return err
	}
	s.executions = append(s.executions, e)
	s.appendLog(e.JobName, "INFO", "执行记录: status="+e.Status)
	return nil
}

// persist 把任务定义序列化写入 sink；sink 为 nil 时跳过。
func (s *Store) persist(j Job) error {
	if s.sink == nil {
		return nil
	}
	data, err := json.Marshal(j)
	if err != nil {
		return wrap(ErrIO, err.Error())
	}
	if _, err := s.sink.Write(append(data, '\n')); err != nil {
		return wrap(ErrIO, err.Error())
	}
	return nil
}

// persistRemove 写一条删除标记到 sink。
func (s *Store) persistRemove(name string) error {
	if s.sink == nil {
		return nil
	}
	data, _ := json.Marshal(RemoveResult{Name: name, Removed: true})
	if _, err := s.sink.Write(append(data, '\n')); err != nil {
		return wrap(ErrIO, err.Error())
	}
	return nil
}

// appendLog 追加一条执行日志。
func (s *Store) appendLog(job, level, msg string) {
	s.logs = append(s.logs, LogEntry{
		Timestamp: nowRFC3339(), JobName: job, Level: level, Message: msg,
	})
}
