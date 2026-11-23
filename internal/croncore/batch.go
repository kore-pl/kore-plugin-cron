package croncore

import (
	"context"
	"sort"
	"strings"
)

// 批量动作边界：一次最多 50 条，逐条执行、部分失败不回滚。
const maxBatchIDs = 50

// BatchParams 是 cron.batch 的入参：action ∈ enable / disable / remove / run，ids 为 list 的 0 起下标。
type BatchParams struct {
	Action string `json:"action"`
	IDs    []int  `json:"ids"`
}

// BatchItem 是单条结果；Error 非空即失败，其余字段按动作填充。
type BatchItem struct {
	ID         int    `json:"id"`
	OK         bool   `json:"ok"`
	Error      string `json:"error,omitempty"`
	Changed    bool   `json:"changed,omitempty"`
	TaskName   string `json:"task_name,omitempty"`
	ExitCode   *int   `json:"exit_code,omitempty"`
	OutputHead string `json:"output_head,omitempty"`
	TimedOut   bool   `json:"timed_out,omitempty"`
	BackupPath string `json:"backup_path,omitempty"`
}

// BatchResult 是 cron.batch 的结果：成功与失败分两组，逐条可判读。
type BatchResult struct {
	Action    string      `json:"action"`
	Total     int         `json:"total"`
	Succeeded []BatchItem `json:"succeeded"`
	Failed    []BatchItem `json:"failed"`
	Note      string      `json:"note,omitempty"`
}

// Batch 批量执行启停/删除/立即执行。部分失败不影响其它条目（不回滚已成功的写入）。
func (s *Store) Batch(ctx context.Context, params BatchParams) (*BatchResult, error) {
	action := strings.ToLower(strings.TrimSpace(params.Action))
	switch action {
	case "enable", "disable", "remove", "run":
	default:
		return nil, wrap(ErrInvalidParam, "action 必须是 enable / disable / remove / run")
	}
	ids := dedupeIDs(params.IDs)
	if len(ids) == 0 {
		return nil, wrap(ErrInvalidParam, "ids 必填且至少一条")
	}
	if len(ids) > maxBatchIDs {
		return nil, wrap(ErrInvalidParam, "ids 最多 "+itoa(maxBatchIDs)+" 条")
	}
	needWrite := action != "run"
	if err := s.preflight(ctx, needWrite); err != nil {
		return nil, err
	}
	// 删除会改变后续下标，必须从大到小；其余动作从小到大，结果顺序稳定。
	sort.Ints(ids)
	if action == "remove" {
		for i, j := 0, len(ids)-1; i < j; i, j = i+1, j-1 {
			ids[i], ids[j] = ids[j], ids[i]
		}
	}
	result := &BatchResult{Action: action, Total: len(ids), Succeeded: []BatchItem{}, Failed: []BatchItem{}}
	for _, id := range ids {
		item := s.batchOne(ctx, action, id)
		if item.OK {
			result.Succeeded = append(result.Succeeded, item)
		} else {
			result.Failed = append(result.Failed, item)
		}
	}
	if len(result.Failed) == 0 {
		result.Note = "全部成功"
	} else {
		result.Note = "部分失败：" + itoa(len(result.Succeeded)) + " 成功 / " + itoa(len(result.Failed)) + " 失败；已成功的条目不回滚"
	}
	return result, nil
}

// batchOne 执行单条动作并把结果收口为 BatchItem。
func (s *Store) batchOne(ctx context.Context, action string, id int) BatchItem {
	item := BatchItem{ID: id}
	switch action {
	case "enable", "disable":
		toggled, err := s.SetEnabled(ctx, id, action == "enable")
		if err != nil {
			item.Error = err.Error()
			return item
		}
		item.OK, item.Changed = true, toggled.Changed
		item.TaskName, item.BackupPath = toggled.Entry.TaskName, toggled.BackupPath
	case "remove":
		removed, err := s.Remove(ctx, id)
		if err != nil {
			item.Error = err.Error()
			return item
		}
		item.OK, item.TaskName, item.BackupPath = true, removed.Removed.TaskName, removed.BackupPath
	case "run":
		run, err := s.Run(ctx, RunParams{Index: &id})
		if err != nil {
			item.Error = err.Error()
			return item
		}
		item.OK, item.TaskName = true, run.TaskName
		code := run.ExitCode
		item.ExitCode, item.TimedOut = &code, run.TimedOut
		item.OutputHead = truncateRunes(run.Output, outputHeadBytes)
	}
	return item
}

// dedupeIDs 去重并保持输入顺序；负数与越界留给单条动作报错（逐条可判读）。
func dedupeIDs(raw []int) []int {
	seen := make(map[int]bool, len(raw))
	out := make([]int, 0, len(raw))
	for _, id := range raw {
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}
