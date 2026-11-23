package croncore

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// 稳定二进制路径：防止 crontab 行钉死某个版本目录。
//
// 安装布局是 <pluginRoot>/<id>/<version>/bin/<id>。升级会新增版本目录、旧目录保留
// （供回滚），所以把版本目录写进**宿主 crontab** 会让升级后继续执行旧二进制，
// 且没有任何提示。内核 installer 在 <pluginRoot>/<id>/current 维护一个指向
// “当前版本目录”的相对符号链接；本插件写 crontab 时一律使用稳定路径
// <pluginRoot>/<id>/current/bin/<id>，升级后自动指向新版本。
//
// 存量迁移：插件启动时调用 MigrateStaleBinaries，把指向本插件版本目录的 wrapper
// 行改写到稳定路径（稳定路径不可用时退化为当前版本路径），升级后立即生效；
// cron.list 对仍不可用的条目返回 binary_status / binary_note，让失败可发现。
const currentLinkName = "current"

// versionDirPattern 识别安装布局中的版本目录名（1–4 段数字 + 可选预发布/构建后缀）。
var versionDirPattern = regexp.MustCompile(`^v?[0-9]+(\.[0-9]+){1,3}([-.+][0-9A-Za-z.-]+)?$`)

// binaryLayout 是当前二进制在安装布局中的位置：
// versioned = <pluginRoot>/<id>/<version>/bin/<id>，stable = <pluginRoot>/<id>/current/bin/<id>。
type binaryLayout struct {
	pluginDir string
	version   string
	versioned string
	stable    string
}

// binaryPath 是“应该写进 crontab 的二进制路径”及其来源。
type binaryPath struct {
	path     string
	stable   bool
	layout   binaryLayout
	layoutOK bool
}

// executablePath 返回当前进程可执行文件的真实路径；取不到返回空串。
func executablePath() string {
	exe, err := os.Executable()
	if err != nil || exe == "" {
		return ""
	}
	if resolved, resolveErr := filepath.EvalSymlinks(exe); resolveErr == nil {
		return resolved
	}
	return exe
}

// binaryLayoutFrom 解析安装布局；不是 <root>/<id>/<version>/bin/<id> 时 ok=false
// （开发构建、go test 临时二进制、用户自定义部署都不应被迁移逻辑误改）。
func binaryLayoutFrom(exe string) (binaryLayout, bool) {
	if exe == "" {
		return binaryLayout{}, false
	}
	clean := filepath.Clean(exe)
	parts := strings.Split(clean, string(filepath.Separator))
	if len(parts) < 4 {
		return binaryLayout{}, false
	}
	name := parts[len(parts)-1]
	if parts[len(parts)-2] != "bin" {
		return binaryLayout{}, false
	}
	version := parts[len(parts)-3]
	if !versionDirPattern.MatchString(version) {
		return binaryLayout{}, false
	}
	if parts[len(parts)-4] != name {
		return binaryLayout{}, false
	}
	pluginDir := strings.Join(parts[:len(parts)-3], string(filepath.Separator))
	layout := binaryLayout{
		pluginDir: pluginDir,
		version:   version,
		versioned: clean,
		stable:    filepath.Join(pluginDir, currentLinkName, "bin", name),
	}
	return layout, true
}

// wrapperBinaryPath 返回写进 crontab 的二进制路径：优先稳定路径，稳定路径不存在时
// 退化为当前版本路径（并让调用方在结果里说明，避免把降级伪装成正常）。
func wrapperBinaryPath() binaryPath {
	return wrapperBinaryPathFrom(executablePath())
}

// wrapperBinaryPathFrom 是 wrapperBinaryPath 的可注入版本，供单元测试使用。
func wrapperBinaryPathFrom(exe string) binaryPath {
	layout, ok := binaryLayoutFrom(exe)
	if !ok {
		return binaryPath{path: exe}
	}
	if fileExists(layout.stable) {
		return binaryPath{path: layout.stable, stable: true, layout: layout, layoutOK: true}
	}
	return binaryPath{path: layout.versioned, layout: layout, layoutOK: true}
}

// fileExists 判断路径存在；悬空符号链接按不存在处理（cron 会静默失败，必须报出来）。
func fileExists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

// referencesSelf 判断 bin 是否指向本插件（稳定路径或任一版本目录）。
func referencesSelf(layout binaryLayout, bin string) bool {
	if bin == layout.stable {
		return true
	}
	_, ok := selfVersionOf(layout, bin)
	return ok
}

// selfVersionOf 返回 bin 对应的版本目录名；不是本插件的版本路径时 ok=false。
func selfVersionOf(layout binaryLayout, bin string) (string, bool) {
	if layout.pluginDir == "" {
		return "", false
	}
	rel, err := filepath.Rel(layout.pluginDir, bin)
	if err != nil {
		return "", false
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) != 3 || parts[1] != "bin" {
		return "", false
	}
	if parts[2] != filepath.Base(layout.stable) || !versionDirPattern.MatchString(parts[0]) {
		return "", false
	}
	return parts[0], true
}

// classifyBinary 生成 cron.list 里单条任务的二进制状态：
// stable（稳定路径且存在）/ ok（存在但非稳定路径）/ stale（旧版本目录）/ missing（不存在）。
func classifyBinary(layout binaryLayout, layoutOK bool, bin string) (status, note string) {
	if !fileExists(bin) {
		return "missing", "指向的二进制不存在：" + bin +
			"；系统 cron 到点会静默失败，请用 cron.replace 重写该条或重装插件"
	}
	if !layoutOK || !referencesSelf(layout, bin) {
		return "ok", ""
	}
	if bin == layout.stable {
		return "stable", ""
	}
	if version, ok := selfVersionOf(layout, bin); ok && version != layout.version {
		return "stale", "这条任务指向旧版本 " + version + "（当前运行 " + layout.version +
			"），升级后仍会执行旧二进制，请用 cron.replace 重新保存该条（或重装插件）；插件下次启动也会自动迁移到 " + layout.stable
	}
	return "ok", "指向版本目录而非稳定路径 " + layout.stable + "；插件下次启动会自动迁移"
}

// splitWrapperCommand 拆出本插件生成的 wrapper 命令首 token（单引号路径）与剩余部分。
// 只接受本插件写出的单引号形式，用户自己写的其它引号/命令一律不动。
func splitWrapperCommand(command string) (bin, rest string, ok bool) {
	if !strings.HasPrefix(command, "'") {
		return "", "", false
	}
	var token strings.Builder
	for i := 1; i < len(command); {
		if command[i] == '\'' {
			// shellQuote 把路径里的单引号转义为 '\''；这里按同一规则还原。
			if strings.HasPrefix(command[i:], `'\''`) {
				token.WriteByte('\'')
				i += 4
				continue
			}
			remainder := command[i+1:]
			if !strings.HasPrefix(remainder, " "+taskRunMode+" ") {
				return "", "", false
			}
			return token.String(), remainder, true
		}
		token.WriteByte(command[i])
		i++
	}
	return "", "", false
}

// MigrateResult 是一次 crontab 稳定路径迁移的结果。
type MigrateResult struct {
	// Rewritten 是被改写的任务行数；0 表示无需迁移（已全部是稳定路径）。
	Rewritten int `json:"rewritten"`
	// BackupPath 是改写前的 crontab 备份（只有真正写盘时才有）。
	BackupPath string `json:"backup_path,omitempty"`
	// StablePath 是稳定二进制路径；StableReady=false 表示内核还没建立 current 链接。
	StablePath  string `json:"stable_path,omitempty"`
	StableReady bool   `json:"stable_ready"`
	// Skipped 是“看起来是本插件 wrapper、但格式无法识别”而被原样保留的行数；
	// 迁移绝不猜测重写用户手改过的行（只记数，由调用方告警）。
	Skipped int `json:"skipped,omitempty"`
	// Note 说明跳过/降级原因，避免把“没做”伪装成“已迁移”。
	Note string `json:"note,omitempty"`
}

// MigrateStaleBinaries 把 crontab 里指向本插件版本目录的 wrapper 行迁移到稳定路径。
//
// 幂等：已经是稳定路径（或不需要迁移）时 Rewritten=0，不写盘、不产生备份。
// 保留停用标记、schedule、注释与 wrapper 的其余参数（runlog/任务名/命令）逐字不变。
func (s *Store) MigrateStaleBinaries(ctx context.Context) (*MigrateResult, error) {
	return s.migrateStaleBinaries(ctx, executablePath())
}

// migrateStaleBinaries 是 MigrateStaleBinaries 的可注入版本（exe 供单元测试构造安装布局）。
func (s *Store) migrateStaleBinaries(ctx context.Context, exe string) (*MigrateResult, error) {
	layout, ok := binaryLayoutFrom(exe)
	if !ok {
		return &MigrateResult{Note: "当前二进制不在 <pluginRoot>/<id>/<version>/bin/<id> 安装布局中，跳过 crontab 稳定路径迁移"}, nil
	}
	result := &MigrateResult{StablePath: layout.stable, StableReady: fileExists(layout.stable)}
	target := layout.stable
	if !result.StableReady {
		// 内核还没建 current 链接（旧内核或符号链接不可用）：至少指向当前版本，
		// 下次插件启动仍会再尝试迁移；Note 让调用方/日志能看出这是降级。
		target = layout.versioned
		result.Note = "稳定路径 " + layout.stable + " 不存在（内核未建立 current 链接），已退化为当前版本路径 " + layout.versioned
	}
	if err := s.preflight(ctx, true); err != nil {
		return nil, err
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	content, err := s.readCrontab(ctx)
	if err != nil {
		return nil, err
	}
	lines := splitLines(content)
	rewritten := 0
	for i, line := range lines {
		newLine, changed := rewriteWrapperBinary(line, layout, target)
		if changed {
			// 写前自校验：新行必须仍是合法的 5 段任务行，否则宁可不动（crontab - 也会拒绝，
			// 但这里先拦住，避免整份写入被一条坏行拖累后依赖回滚）。
			if !validTaskLine(newLine) {
				result.Skipped++
				continue
			}
			lines[i] = newLine
			rewritten++
			continue
		}
		if unparseableWrapperLine(line) {
			result.Skipped++
		}
	}
	if result.Skipped > 0 {
		result.Note = appendNote(result.Note, itoa(result.Skipped)+" 行疑似本插件 wrapper 但格式无法识别，已原样保留（未猜测重写）")
	}
	result.Rewritten = rewritten
	if rewritten == 0 {
		return result, nil
	}
	backupPath, err := s.commit(ctx, content, joinLines(lines))
	if err != nil {
		return nil, err
	}
	result.BackupPath = backupPath
	return result, nil
}

// validTaskLine 判断一行是否是可安全写回的任务行（5 段 schedule 合法、command 非空）。
func validTaskLine(line string) bool {
	body := strings.TrimLeft(line, " \t")
	if rest, ok := cutDisabledPrefix(body); ok {
		body = rest
	}
	schedule, _, ok := splitEntry(body)
	return ok && ValidateSchedule(schedule) == nil
}

// unparseableWrapperLine 判断一行“看起来像本插件 wrapper 但格式认不出来”：
// 含 `__task-run`、不是注释、却解析不出合法 5 段任务或单引号首 token。
// 这类行一律跳过并计数，绝不按猜测重写。
func unparseableWrapperLine(line string) bool {
	body := strings.TrimLeft(line, " \t")
	if rest, ok := cutDisabledPrefix(body); ok {
		body = rest
	}
	if body == "" || strings.HasPrefix(body, "#") || !strings.Contains(body, " "+taskRunMode+" ") {
		return false
	}
	schedule, command, ok := splitEntry(body)
	if !ok || ValidateSchedule(schedule) != nil {
		return true
	}
	_, _, ok = splitWrapperCommand(command)
	return !ok
}

// appendNote 合并多条说明，避免后一条覆盖前一条。
func appendNote(current, extra string) string {
	if current == "" {
		return extra
	}
	return current + "；" + extra
}

// rewriteWrapperBinary 只改写“本插件 wrapper 行的首 token”，其余内容保持逐字不变。
func rewriteWrapperBinary(line string, layout binaryLayout, target string) (string, bool) {
	body := strings.TrimLeft(line, " \t")
	leading := line[:len(line)-len(body)]
	prefix := ""
	if rest, ok := cutDisabledPrefix(body); ok {
		prefix, body = DisabledPrefix, rest
	}
	schedule, command, ok := splitEntry(body)
	if !ok {
		return "", false
	}
	bin, rest, ok := splitWrapperCommand(command)
	if !ok || !referencesSelf(layout, bin) || bin == target {
		return "", false
	}
	newBody := schedule + " " + shellQuote(target) + rest
	return leading + prefix + newBody, true
}

// migrateNote 是 cron.add 在稳定路径不可用时的提示语（避免静默降级）。
func migrateNote(stable string) string {
	return fmt.Sprintf("内核尚未提供稳定路径 %s，本次已写入当前版本路径；插件下次启动会自动迁移到稳定路径", stable)
}
