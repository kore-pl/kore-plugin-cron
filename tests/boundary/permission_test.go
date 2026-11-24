package boundary_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pengtang1024/kore-plugin-cron/internal/croncore"
)

// 权限不足（判据① 第 3 类）：真实 EACCES，不靠假 crontab 输出模拟。
//
// 两类真实场景：
//  1. 当前用户的 crontab 文件被 chmod 000 —— 读被内核拒绝；
//  2. 非 root 访问 root-only 路径（/root，mode 0700 且属主 root）——同样是 EACCES。
//
// 契约：List 把权限不足当环境限制（成功 + 空列表 + warning），
// 写类方法（Add）必须返回明确哨兵 ErrNoPermission，且不产生任何写入。
func TestPermissionDeniedCrontabFile(t *testing.T) {
	store, cronFile := newIsolatedStore(t)
	const seed = "0 1 * * * /bin/one\n"
	seedCrontab(t, cronFile, seed)
	if err := os.Chmod(cronFile, 0o000); err != nil {
		t.Fatalf("构造权限不足失败: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(cronFile, 0o600) })
	// 先用真实读探测权限位是否生效：root 或权限位不生效的平台会读到内容，如实跳过。
	if _, readErr := os.ReadFile(cronFile); readErr == nil {
		t.Skip("当前身份可读 chmod 000 文件（root 或平台不强制权限位），无法构造真实 EACCES")
	}

	_, err := store.Add(ctx(), croncore.AddParams{
		Schedule: "0 3 * * *", Command: "/bin/three",
	})
	if !errors.Is(err, croncore.ErrNoPermission) {
		t.Fatalf("chmod 000 后 Add 必须返回 ErrNoPermission，实际 %v", err)
	}
	if !strings.Contains(err.Error(), "权限") {
		t.Fatalf("权限错误信息应说明权限不足，实际 %q", err.Error())
	}

	// List 的口径是"环境限制不是服务故障"：成功 + 空列表 + warning。
	list, listErr := store.List(ctx())
	if listErr != nil {
		t.Fatalf("权限不足时 List 不应返回错误，实际 %v", listErr)
	}
	if list == nil || list.Count != 0 || len(list.Entries) != 0 || list.Warning == "" {
		t.Fatalf("权限不足时 List 应返回空列表 + warning，实际 %+v", list)
	}

	// 写类方法在进入备份/写盘前就失败：不应留下备份文件。
	if backups := dirEntries(t, os.Getenv("KORE_CRON_BACKUP_DIR")); len(backups) != 0 {
		t.Fatalf("权限失败不应产生备份文件，实际 %v", backups)
	}

	if err := os.Chmod(cronFile, 0o600); err != nil {
		t.Fatalf("恢复权限失败: %v", err)
	}
	if got := string(readCrontabBytes(t, cronFile)); got != seed {
		t.Fatalf("权限失败不得改动 crontab:\n期望 %q\n实际 %q", seed, got)
	}
}

// 非 root 访问 root-only 路径：读 crontab 就被内核拒绝（EACCES → ErrNoPermission）。
// /root 是 root:0700；本机若不是该布局（例如已 root 或 /root 可遍历），如实跳过，
// 不伪造权限场景。
func TestPermissionDeniedRootOnlyPath(t *testing.T) {
	info, err := os.Stat("/root")
	if err != nil {
		t.Skipf("/root 不存在，跳过 root-only 场景: %v", err)
	}
	if !info.IsDir() {
		t.Skipf("/root 不是目录，跳过 root-only 场景")
	}
	probe, err := os.Open("/root")
	if err == nil {
		_ = probe.Close()
		t.Skip("/root 对本用户可遍历，无法构造 root-only 场景")
	}
	if !os.IsPermission(err) {
		t.Skipf("/root 不可访问但不是权限错误（%v），跳过", err)
	}

	cronFile := filepath.Join("/root", "kore-cron-boundary-never-created")
	store := isolateEnv(t, cronFile)
	_, err = store.Add(ctx(), croncore.AddParams{
		Schedule: "0 3 * * *", Command: "/bin/true",
	})
	if !errors.Is(err, croncore.ErrNoPermission) {
		t.Fatalf("root-only 路径必须返回 ErrNoPermission，实际 %v", err)
	}
	if !strings.Contains(err.Error(), "权限") {
		t.Fatalf("错误信息应说明权限不足，实际 %q", err.Error())
	}
}
