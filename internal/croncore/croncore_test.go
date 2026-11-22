package croncore

import (
	"context"
	"errors"
	"testing"
)

func TestValidateSchedule(t *testing.T) {
	valid := []string{
		"0 3 * * *", "*/5 * * * *", "0 0 1 1 0", "0,15,30,45 * * * *",
		"10-20 3 * * 1-5", "0 3 * * 7", "0 3 1,15 * *",
	}
	for _, s := range valid {
		if err := ValidateSchedule(s); err != nil {
			t.Errorf("合法 schedule %q 被拒绝: %v", s, err)
		}
	}
	invalid := []string{
		"", "0 3 * *", "0 3 * * * *", "60 3 * * *", "0 24 * * *",
		"0 3 0 * *", "0 3 * 13 *", "0 3 * * 8", "a 3 * * *",
		"0 3 * * */0", "0 3 * 1-12-5 *", "0 3 * * 5-1",
	}
	for _, s := range invalid {
		if err := ValidateSchedule(s); err == nil {
			t.Errorf("非法 schedule %q 被接受", s)
		}
	}
}

func TestValidateName(t *testing.T) {
	if err := ValidateName("backup_daily-1"); err != nil {
		t.Errorf("合法 name 被拒绝: %v", err)
	}
	for _, bad := range []string{"", "has space", "a/b", ".."} {
		if err := ValidateName(bad); err == nil {
			t.Errorf("非法 name %q 被接受", bad)
		}
	}
}

// TestAddListRemoveHappyPath 验证方法级 happy path 与结构化结果。
func TestAddListRemoveHappyPath(t *testing.T) {
	s := NewStore()
	ctx := context.Background()
	if _, err := s.Add(ctx, AddParams{
		Name: "backup", Schedule: "0 3 * * *", Command: "/usr/bin/backup",
	}); err != nil {
		t.Fatalf("Add 失败: %v", err)
	}
	result, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List 失败: %v", err)
	}
	if result.Count != 1 || len(result.Jobs) != 1 || result.Jobs[0].Name != "backup" {
		t.Fatalf("List 结果不符: %+v", result)
	}
	if len(result.Logs) == 0 {
		t.Fatal("Add 未写入执行日志")
	}
	if _, err := s.Remove(ctx, "backup"); err != nil {
		t.Fatalf("Remove 失败: %v", err)
	}
	result, _ = s.List(ctx)
	if result.Count != 0 {
		t.Fatalf("Remove 后仍有任务: %d", result.Count)
	}
}

// TestAddDuplicate 验证重复添加返回 duplicate=true 而非报错。
func TestAddDuplicate(t *testing.T) {
	s := NewStore()
	ctx := context.Background()
	p := AddParams{Name: "dup", Schedule: "0 3 * * *", Command: "true"}
	_, _ = s.Add(ctx, p)
	r, err := s.Add(ctx, p)
	if err != nil {
		t.Fatalf("重复 Add 不应报错: %v", err)
	}
	if !r.Duplicate {
		t.Fatal("重复 Add 未标记 duplicate")
	}
}

// TestRemoveNotFound 验证删除不存在的任务返回 ErrNotFound。
func TestRemoveNotFound(t *testing.T) {
	s := NewStore()
	_, err := s.Remove(context.Background(), "ghost")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("期望 ErrNotFound，实际 %v", err)
	}
}

// TestNoScheduler 锁定设计边界：croncore 不产生执行记录，执行记录只能由
// 宿主通过 RecordExecution 上报，证明插件本身不触发任务（零调度）。
func TestNoScheduler(t *testing.T) {
	s := NewStore()
	_, _ = s.Add(context.Background(), AddParams{
		Name: "job", Schedule: "* * * * *", Command: "true",
	})
	r, _ := s.List(context.Background())
	if len(r.Executions) != 0 {
		t.Fatalf("插件不应自行产生执行记录，实际 %d 条", len(r.Executions))
	}
	if err := s.RecordExecution(context.Background(), Execution{
		ID: "1", JobName: "job", Status: "ok",
	}); err != nil {
		t.Fatalf("RecordExecution 失败: %v", err)
	}
	r, _ = s.List(context.Background())
	if len(r.Executions) != 1 {
		t.Fatal("宿主上报的执行记录未写入")
	}
}
