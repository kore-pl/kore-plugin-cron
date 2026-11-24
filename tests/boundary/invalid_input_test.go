package boundary_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/pengtang1024/kore-plugin-cron/internal/croncore"
)

// 异常输入（判据① 第 1 类）：crontab 按行解析，任何可换行/可越界入参都必须
// 在写盘前被拒绝，且错误码明确为 ErrInvalidParam。
//
// 复用仓库已有的四类异常输入覆盖（内部 croncore 用例已经验证同一路径），
// 这里从"隔离的 crontab 文件"出发复核：拒绝之外还要证明文件没有被创建。
func TestInvalidInputRejected(t *testing.T) {
	cases := []struct {
		name   string
		params croncore.AddParams
	}{
		{
			name:   "非法 cron 表达式_段数不足",
			params: croncore.AddParams{Schedule: "0 3 * *", Command: "/bin/true"},
		},
		{
			name:   "非法 cron 表达式_分钟越界",
			params: croncore.AddParams{Schedule: "60 3 * * *", Command: "/bin/true"},
		},
		{
			name: "任务名非法_备注注入换行",
			params: croncore.AddParams{
				Schedule: "0 3 * * *", Command: "/bin/true",
				Comment: "daily\n0 0 * * * /bin/evil",
			},
		},
		{
			name:   "命令为空",
			params: croncore.AddParams{Schedule: "0 3 * * *", Command: "   "},
		},
		{
			name:   "命令非法_注入换行",
			params: croncore.AddParams{Schedule: "0 3 * * *", Command: "echo a\necho b"},
		},
		{
			name: "命令超长",
			params: croncore.AddParams{
				Schedule: "0 3 * * *", Command: strings.Repeat("x", 513),
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, cronFile := newIsolatedStore(t)
			_, err := store.Add(ctx(), tc.params)
			if !errors.Is(err, croncore.ErrInvalidParam) {
				t.Fatalf("异常输入必须返回 ErrInvalidParam，实际 %v（params=%+v）", err, tc.params)
			}
			mustNotExist(t, cronFile)
		})
	}
}

// TestInvalidIndexRejected：越界 index 属于异常输入，且不得改动已有 crontab。
func TestInvalidIndexRejected(t *testing.T) {
	store, cronFile := newIsolatedStore(t)
	const seed = "0 1 * * * /bin/one\n"
	seedCrontab(t, cronFile, seed)

	if _, err := store.Replace(ctx(), croncore.ReplaceParams{
		Index: 5, Schedule: "0 2 * * *", Command: "/bin/two",
	}); !errors.Is(err, croncore.ErrInvalidParam) {
		t.Fatalf("越界 replace 必须返回 ErrInvalidParam，实际 %v", err)
	}
	if _, err := store.Remove(ctx(), 5); !errors.Is(err, croncore.ErrInvalidParam) {
		t.Fatalf("越界 remove 必须返回 ErrInvalidParam，实际 %v", err)
	}
	if got := string(readCrontabBytes(t, cronFile)); got != seed {
		t.Fatalf("异常 index 不得改动 crontab:\n期望 %q\n实际 %q", seed, got)
	}
}
