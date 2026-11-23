package croncore

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestBatchPartialFailure：逐条结果、部分失败不影响已成功条目。
func TestBatchPartialFailure(t *testing.T) {
	store, fake := newTestStore(t)
	_ = fake.Seed("0 1 * * * /bin/one\n0 2 * * * /bin/two\n")
	result, err := store.Batch(context.Background(), BatchParams{Action: "disable", IDs: []int{0, 9}})
	if err != nil {
		t.Fatalf("Batch 失败: %v", err)
	}
	if len(result.Succeeded) != 1 || len(result.Failed) != 1 || result.Succeeded[0].ID != 0 || result.Failed[0].ID != 9 {
		t.Fatalf("部分失败分组不符: %+v", result)
	}
	if !strings.Contains(result.Failed[0].Error, "越界") || result.Note == "" {
		t.Fatalf("失败条目应带原因: %+v", result.Failed[0])
	}
	content, _ := fake.Content()
	if !strings.Contains(content, DisabledPrefix+"0 1 * * * ") {
		t.Fatalf("成功条目的停用未落盘: %q", content)
	}
	if !strings.Contains(content, "0 2 * * * /bin/two") {
		t.Fatalf("未涉及的条目被改动: %q", content)
	}
}

// TestBatchIdempotentEnable：重复批量启用是幂等的（第二次 changed=false，不写盘）。
func TestBatchIdempotentEnable(t *testing.T) {
	store, fake := newTestStore(t)
	_ = fake.Seed(DisabledPrefix + "0 1 * * * /bin/one\n")
	first, err := store.Batch(context.Background(), BatchParams{Action: "enable", IDs: []int{0}})
	if err != nil || len(first.Succeeded) != 1 || !first.Succeeded[0].Changed {
		t.Fatalf("首次启用不符: %+v err=%v", first, err)
	}
	before, _ := fake.Content()
	second, err := store.Batch(context.Background(), BatchParams{Action: "enable", IDs: []int{0}})
	if err != nil || len(second.Succeeded) != 1 || second.Succeeded[0].Changed {
		t.Fatalf("幂等启用不符: %+v err=%v", second, err)
	}
	after, _ := fake.Content()
	if after != before {
		t.Fatalf("幂等批量启用不应改动 crontab: %q", after)
	}
}

// TestBatchRemoveShiftsIndexes：批量删除从大到小执行，不会误删。
func TestBatchRemoveShiftsIndexes(t *testing.T) {
	store, fake := newTestStore(t)
	_ = fake.Seed("0 1 * * * /bin/one\n0 2 * * * /bin/two\n0 3 * * * /bin/three\n")
	result, err := store.Batch(context.Background(), BatchParams{Action: "remove", IDs: []int{0, 1}})
	if err != nil || len(result.Succeeded) != 2 {
		t.Fatalf("批量删除失败: %+v err=%v", result, err)
	}
	content, _ := fake.Content()
	if strings.Contains(content, "/bin/one") || strings.Contains(content, "/bin/two") || !strings.Contains(content, "/bin/three") {
		t.Fatalf("批量删除结果不符: %q", content)
	}
}

// TestBatchRejectsBadAction：未知动作/空 ids 整体拒绝。
func TestBatchRejectsBadAction(t *testing.T) {
	store, _ := newTestStore(t)
	if _, err := store.Batch(context.Background(), BatchParams{Action: "explode", IDs: []int{0}}); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("未知动作应返回 ErrInvalidParam，实际 %v", err)
	}
	if _, err := store.Batch(context.Background(), BatchParams{Action: "enable"}); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("空 ids 应返回 ErrInvalidParam，实际 %v", err)
	}
}

// TestTemplatesReadOnly：模板只读，重复读取不写 crontab。
func TestTemplatesReadOnly(t *testing.T) {
	store, fake := newTestStore(t)
	_ = fake.Seed("0 1 * * * /bin/one\n")
	before, _ := fake.Content()
	first, err := store.Templates(context.Background())
	if err != nil || first.Count < 3 || first.Count > 5 {
		t.Fatalf("模板数量不符: %+v err=%v", first, err)
	}
	second, err := store.Templates(context.Background())
	if err != nil || second.Count != first.Count || second.Templates[0].ID != first.Templates[0].ID {
		t.Fatalf("模板读取不稳定: %+v err=%v", second, err)
	}
	first.Templates[0].Command = "mutated"
	third, _ := store.Templates(context.Background())
	if third.Templates[0].Command == "mutated" {
		t.Fatal("模板返回的是内部切片引用（调用方可篡改）")
	}
	after, _ := fake.Content()
	if after != before {
		t.Fatalf("只读模板改动了 crontab: %q", after)
	}
}
