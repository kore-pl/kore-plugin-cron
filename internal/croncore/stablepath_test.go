package croncore

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFakeInstall 构造安装布局：<root>/cron/<version>/bin/cron，并可建立 current 链接。
func writeFakeInstall(t *testing.T, versions []string, current string) string {
	t.Helper()
	root := t.TempDir()
	for _, version := range versions {
		binDir := filepath.Join(root, "cron", version, "bin")
		if err := os.MkdirAll(binDir, 0o755); err != nil {
			t.Fatalf("建立版本目录失败: %v", err)
		}
		if err := os.WriteFile(filepath.Join(binDir, "cron"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatalf("写假二进制失败: %v", err)
		}
	}
	if current != "" {
		if err := os.Symlink(current, filepath.Join(root, "cron", currentLinkName)); err != nil {
			t.Fatalf("建立 current 链接失败: %v", err)
		}
	}
	return root
}

// writeCronFile 写入 KORE_CRON_FILE 并返回文件路径与内容。
func writeCronFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "crontab")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("写 crontab 文件失败: %v", err)
	}
	t.Setenv("KORE_CRON_FILE", path)
	t.Setenv("KORE_STATE_DIR", t.TempDir())
	return path
}

// TestBinaryLayoutFrom 验证只识别安装布局，不误伤开发/测试二进制。
func TestBinaryLayoutFrom(t *testing.T) {
	layout, ok := binaryLayoutFrom("/opt/kore/plugins/cron/0.6.0/bin/cron")
	if !ok {
		t.Fatal("安装布局应被识别")
	}
	if layout.pluginDir != "/opt/kore/plugins/cron" || layout.version != "0.6.0" {
		t.Fatalf("布局解析不符: %+v", layout)
	}
	if layout.stable != "/opt/kore/plugins/cron/current/bin/cron" {
		t.Fatalf("稳定路径不符: %s", layout.stable)
	}
	for _, exe := range []string{"", "/tmp/go-build123/b001/cron", "/usr/local/bin/cron", "/opt/kore/plugins/cron/bin/cron"} {
		if _, recognized := binaryLayoutFrom(exe); recognized {
			t.Fatalf("%q 不应被识别为安装布局", exe)
		}
	}
}

// TestWrapperBinaryPathPrefersStable 验证有 current 链接时写稳定路径，没有时退化为版本路径。
func TestWrapperBinaryPathPrefersStable(t *testing.T) {
	root := writeFakeInstall(t, []string{"0.4.1", "0.6.0"}, "")
	exe := filepath.Join(root, "cron", "0.6.0", "bin", "cron")

	got := wrapperBinaryPathFrom(exe)
	if got.stable || got.path != exe {
		t.Fatalf("无 current 链接应退化为版本路径: %+v", got)
	}

	if err := os.Symlink("0.6.0", filepath.Join(root, "cron", currentLinkName)); err != nil {
		t.Fatalf("建立 current 链接失败: %v", err)
	}
	got = wrapperBinaryPathFrom(exe)
	want := filepath.Join(root, "cron", currentLinkName, "bin", "cron")
	if !got.stable || got.path != want {
		t.Fatalf("有 current 链接应使用稳定路径: got=%+v want=%s", got, want)
	}
}

// TestMigrateStaleBinariesRewritesOnlyOwnLines 是核心验收：升级后 crontab 指向新版本，
// 只改本插件行，用户自己写的行逐字节不变，且迁移幂等。
func TestMigrateStaleBinariesRewritesOnlyOwnLines(t *testing.T) {
	root := writeFakeInstall(t, []string{"0.4.1", "0.6.0"}, "0.6.0")
	exe := filepath.Join(root, "cron", "0.6.0", "bin", "cron")
	layout, ok := binaryLayoutFrom(exe)
	if !ok {
		t.Fatal("布局解析失败")
	}
	oldBin := filepath.Join(root, "cron", "0.4.1", "bin", "cron")
	stable := filepath.Join(root, "cron", currentLinkName, "bin", "cron")
	logPath := filepath.Join(root, "runlog", "runlog.jsonl")

	own := func(schedule string) string {
		return schedule + " " + shellQuote(oldBin) + " " + taskRunMode + " " +
			shellQuote(logPath) + " 'nightly' '/usr/bin/backup --full'"
	}
	userLine := "0 5 * * * /usr/bin/echo user-own-line"
	envLine := `MAILTO=""`
	content := strings.Join([]string{
		"# 用户自己的注释",
		own("0 3 * * *"),
		envLine,
		userLine,
		DisabledPrefix + own("0 4 * * *"),
		"",
	}, "\n")
	cronFile := writeCronFile(t, content)

	store := NewStore()
	result, err := store.migrateStaleBinaries(context.Background(), exe)
	if err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	if result.Rewritten != 2 || result.Skipped != 0 {
		t.Fatalf("应改写 2 条、跳过 0 条: %+v", result)
	}
	if !result.StableReady {
		t.Fatalf("current 链接存在时应标记 StableReady: %+v", result)
	}
	if result.BackupPath == "" {
		t.Fatal("迁移写盘前必须备份")
	}
	if _, err := os.Stat(result.BackupPath); err != nil {
		t.Fatalf("备份文件不存在: %v", err)
	}

	after, err := os.ReadFile(cronFile)
	if err != nil {
		t.Fatalf("读迁移后 crontab 失败: %v", err)
	}
	got := string(after)
	t.Logf("迁移前 crontab:\n%s\n迁移后 crontab:\n%s", content, got)

	if strings.Contains(got, oldBin) {
		t.Fatalf("迁移后不应再引用旧版本二进制: %s", got)
	}
	if strings.Count(got, shellQuote(stable)) != 2 {
		t.Fatalf("两条任务都应指向稳定路径 %s: %s", stable, got)
	}
	if !strings.Contains(got, DisabledPrefix+"0 4 * * * "+shellQuote(stable)) {
		t.Fatalf("停用标记必须保留: %s", got)
	}
	// 非本插件行逐字节不变。
	for _, keep := range []string{"# 用户自己的注释", envLine, userLine} {
		if !strings.Contains(got, keep) {
			t.Fatalf("用户行被改动: %q\n%s", keep, got)
		}
	}

	// 核心断言：稳定路径解析到新版本二进制。
	resolved, err := filepath.EvalSymlinks(stable)
	if err != nil {
		t.Fatalf("稳定路径不可解析: %v", err)
	}
	if resolved != exe {
		t.Fatalf("升级后 crontab 应指向新版本二进制: resolved=%s want=%s", resolved, exe)
	}

	// 幂等：第二次不再改写、不再产生备份，内容逐字不变。
	second, err := store.migrateStaleBinaries(context.Background(), exe)
	if err != nil {
		t.Fatalf("第二次迁移失败: %v", err)
	}
	if second.Rewritten != 0 || second.BackupPath != "" {
		t.Fatalf("迁移必须幂等: %+v", second)
	}
	again, err := os.ReadFile(cronFile)
	if err != nil {
		t.Fatalf("读 crontab 失败: %v", err)
	}
	if string(again) != got {
		t.Fatalf("幂等迁移改变了内容:\nbefore=%q\nafter=%q", got, string(again))
	}

	// 用安装布局解析：两条都标记 stable（失败可见字段与真实状态一致）。
	parsedEntries := parseCrontab(string(again)).resultWith(layout, true)
	if len(parsedEntries) != 3 || parsedEntries[0].BinaryStatus != "stable" || parsedEntries[2].BinaryStatus != "stable" {
		t.Fatalf("迁移后条目应标记 stable: %+v", parsedEntries)
	}
	for _, idx := range []int{0, 2} {
		if parsedEntries[idx].Binary != stable {
			t.Fatalf("条目 %d 应指向稳定路径: %+v", idx, parsedEntries[idx])
		}
	}

	// 公开 List 走真实进程布局（测试二进制不在安装目录）：不应误报 stale。
	list, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("List 失败: %v", err)
	}
	if list.Count != 3 || list.StaleCount != 0 {
		t.Fatalf("List 结果不符: %+v", list)
	}
}

// TestMigrateSkipsUnparseableWrapperLines 验证手改过/认不出的行原样保留并计数。
func TestMigrateSkipsUnparseableWrapperLines(t *testing.T) {
	root := writeFakeInstall(t, []string{"0.4.1", "0.6.0"}, "0.6.0")
	exe := filepath.Join(root, "cron", "0.6.0", "bin", "cron")
	oldBin := filepath.Join(root, "cron", "0.4.1", "bin", "cron")
	brokenSchedule := "not-a-schedule " + shellQuote(oldBin) + " " + taskRunMode + " 'x' 'y' 'z'"
	doubleQuoted := `0 3 * * * "` + oldBin + `" ` + taskRunMode + " 'x' 'y' 'z'"
	content := brokenSchedule + "\n" + doubleQuoted + "\n"
	cronFile := writeCronFile(t, content)

	store := NewStore()
	result, err := store.migrateStaleBinaries(context.Background(), exe)
	if err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	if result.Rewritten != 0 || result.Skipped != 2 {
		t.Fatalf("认不出的行应跳过并计数: %+v", result)
	}
	if result.Note == "" {
		t.Fatal("跳过时必须给出说明，不能静默")
	}
	after, err := os.ReadFile(cronFile)
	if err != nil {
		t.Fatalf("读 crontab 失败: %v", err)
	}
	if string(after) != content {
		t.Fatalf("认不出的行必须逐字保留:\n got=%q\nwant=%q", string(after), content)
	}
}

// TestMigrateFallsBackWhenStableLinkMissing 验证内核未建 current 链接时退化为当前版本路径，
// 并显式标记 StableReady=false（降级可见）。
func TestMigrateFallsBackWhenStableLinkMissing(t *testing.T) {
	root := writeFakeInstall(t, []string{"0.4.1", "0.6.0"}, "")
	exe := filepath.Join(root, "cron", "0.6.0", "bin", "cron")
	oldBin := filepath.Join(root, "cron", "0.4.1", "bin", "cron")
	line := "0 3 * * * " + shellQuote(oldBin) + " " + taskRunMode + " 'x' 'y' 'z'"
	cronFile := writeCronFile(t, line+"\n")

	store := NewStore()
	result, err := store.migrateStaleBinaries(context.Background(), exe)
	if err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	if result.Rewritten != 1 || result.StableReady || result.Note == "" {
		t.Fatalf("无稳定路径时应退化并说明: %+v", result)
	}
	after, _ := os.ReadFile(cronFile)
	if !strings.Contains(string(after), shellQuote(exe)) {
		t.Fatalf("应退化为当前版本路径 %s: %s", exe, after)
	}
	second, err := store.migrateStaleBinaries(context.Background(), exe)
	if err != nil || second.Rewritten != 0 {
		t.Fatalf("退化路径也应幂等: %+v err=%v", second, err)
	}
}

// TestListReportsMissingAndStaleBinary 验证 binary_status 的三个非正常取值与可操作文案。
func TestListReportsMissingAndStaleBinary(t *testing.T) {
	root := writeFakeInstall(t, []string{"0.4.1", "0.6.0"}, "0.6.0")
	exe := filepath.Join(root, "cron", "0.6.0", "bin", "cron")
	layout, ok := binaryLayoutFrom(exe)
	if !ok {
		t.Fatal("布局解析失败")
	}
	stable := layout.stable
	stale := filepath.Join(root, "cron", "0.4.1", "bin", "cron")
	missing := filepath.Join(root, "cron", "0.2.0", "bin", "cron")
	content := strings.Join([]string{
		"0 1 * * * " + shellQuote(stable) + " " + taskRunMode + " 'x' 'a' 'b'",
		"0 2 * * * " + shellQuote(stale) + " " + taskRunMode + " 'x' 'a' 'b'",
		"0 3 * * * " + shellQuote(missing) + " " + taskRunMode + " 'x' 'a' 'b'",
		"0 4 * * * /usr/bin/echo plain",
	}, "\n")
	entries := parseCrontab(content).resultWith(layout, true)
	if len(entries) != 4 {
		t.Fatalf("条目数不符: %+v", entries)
	}
	want := []struct {
		status string
		note   bool
	}{
		{"stable", false}, {"stale", true}, {"missing", true}, {"", false},
	}
	for i, w := range want {
		if entries[i].BinaryStatus != w.status {
			t.Fatalf("第 %d 条状态 = %q，期望 %q (%+v)", i, entries[i].BinaryStatus, w.status, entries[i])
		}
		if w.note && entries[i].BinaryNote == "" {
			t.Fatalf("第 %d 条 status=%s 必须给可操作文案", i, w.status)
		}
	}
	if !strings.Contains(entries[1].BinaryNote, "旧版本 0.4.1") ||
		!strings.Contains(entries[1].BinaryNote, "cron.replace") {
		t.Fatalf("stale 文案要指出旧版本与处理方式: %q", entries[1].BinaryNote)
	}
	if !strings.Contains(entries[2].BinaryNote, "不存在") {
		t.Fatalf("missing 文案要指出二进制不存在: %q", entries[2].BinaryNote)
	}

	// 走公开 List：missing/stale 计入 stale_count，并出现在 warning 里。
	writeCronFile(t, content)
	list, err := NewStore().List(context.Background())
	if err != nil {
		t.Fatalf("List 失败: %v", err)
	}
	// 测试二进制不在安装布局中，stale 只能按 missing 归类；稳定路径也不适用。
	if list.StaleCount == 0 || !strings.Contains(list.Warning, "不可用或为旧版本") {
		t.Fatalf("List 必须让失败可见: %+v", list)
	}
}
