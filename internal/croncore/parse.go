package croncore

import (
	"os"
	"strings"
)

// DisabledPrefix 是停用任务的注释标记：系统 cron 把以 # 开头的行当注释，
// 因此 "#KORE-DISABLED# " + 原始任务行既不会被系统执行，又完整保留定义。
// 该标记必须紧贴行首（允许前导空白），避免和普通注释混淆。
const DisabledPrefix = "#KORE-DISABLED# "

// parsedCrontab 是 crontab 文本的按行解析结果：lines 保留每一行原文
// （注释、空行、环境变量、无法识别的行都原样保留），entries 只记录可管理的
// 5 段任务行及其行号与紧邻注释块，改动时不重排、不改写无关行。
type parsedCrontab struct {
	lines   []string
	entries []parsedEntry
}

// parsedEntry 是一条任务行：line 为下标，commentStart 为紧邻其上方连续
// 注释块的起始下标（-1 表示无注释）；disabled 表示该行带停用标记。
type parsedEntry struct {
	line         int
	commentStart int
	schedule     string
	command      string
	disabled     bool
	// meta 是 wrapper 任务的逻辑定义；nil 表示普通（未包装）任务。
	meta *taskMeta
	// metaLine 是元数据注释所在行（-1 表示无）。
	metaLine int
}

// cutDisabledPrefix 剥掉行首停用标记；ok=false 表示不是停用行。
func cutDisabledPrefix(line string) (string, bool) {
	if !strings.HasPrefix(line, DisabledPrefix) {
		return "", false
	}
	return line[len(DisabledPrefix):], true
}

// splitLines 去掉末尾换行后按行拆分；空文本返回 nil。
func splitLines(content string) []string {
	content = strings.TrimSuffix(content, "\n")
	if content == "" {
		return nil
	}
	return strings.Split(content, "\n")
}

// joinLines 统一以换行结尾；空文本返回空串。
func joinLines(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

// parseCrontab 解析整份 crontab。无法识别的行不参与任务列表，但保留原文。
// 带停用标记的行按"任务"解析并标记 disabled；标记后不是合法任务的定义按普通
// 注释处理，绝不让它变成可执行任务。
func parseCrontab(content string) parsedCrontab {
	p := parsedCrontab{lines: splitLines(content)}
	pending := -1
	for i, raw := range p.lines {
		trimmed := strings.TrimLeft(raw, " \t")
		if strings.TrimSpace(raw) == "" {
			pending = -1
			continue
		}
		taskLine := trimmed
		disabled := false
		if rest, ok := cutDisabledPrefix(trimmed); ok {
			disabled, taskLine = true, rest
		}
		if strings.HasPrefix(taskLine, "#") {
			if pending < 0 {
				pending = i
			}
			continue
		}
		schedule, command, ok := splitEntry(taskLine)
		if !ok || ValidateSchedule(schedule) != nil {
			pending = -1
			continue
		}
		entry := parsedEntry{
			line: i, commentStart: pending, schedule: schedule, command: command,
			disabled: disabled, metaLine: -1,
		}
		if pending >= 0 {
			if meta, metaLine, ok := findTaskMeta(p.lines, pending, i); ok {
				entry.meta, entry.metaLine = meta, metaLine
			}
		}
		p.entries = append(p.entries, entry)
		pending = -1
	}
	return p
}

// findTaskMeta 在紧邻任务行的注释块中查找元数据行；最后一条生效。
func findTaskMeta(lines []string, start, end int) (*taskMeta, int, bool) {
	for i := end - 1; i >= start; i-- {
		if meta, ok := parseTaskMeta(lines[i]); ok {
			return &meta, i, true
		}
	}
	return nil, -1, false
}

// splitEntry 把一行拆成 5 段 schedule 与剩余 command。环境变量、@ 宏、
// 段数不足或 command 为空的行返回 ok=false。
func splitEntry(line string) (schedule, command string, ok bool) {
	fields := make([]string, 0, 5)
	i := 0
	for len(fields) < 5 {
		for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
			i++
		}
		if i >= len(line) {
			return "", "", false
		}
		start := i
		for i < len(line) && line[i] != ' ' && line[i] != '\t' {
			i++
		}
		fields = append(fields, line[start:i])
	}
	if strings.Contains(fields[0], "=") || strings.HasPrefix(fields[0], "@") {
		return "", "", false
	}
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	command = line[i:]
	if command == "" {
		return "", "", false
	}
	return strings.Join(fields, " "), command, true
}

// result 生成对外的条目数组，Index 为 0 起下标。
func (p parsedCrontab) result() []Entry {
	entries := make([]Entry, 0, len(p.entries))
	for i, e := range p.entries {
		entries = append(entries, p.entryOf(i, e))
	}
	return entries
}

// entryOf 组装一条对外条目：停用行的 Raw 返回去掉标记后的定义，
// 让界面展示的是"任务本身"，而 enabled=false 表明它当前不会被执行。
func (p parsedCrontab) entryOf(index int, e parsedEntry) Entry {
	raw := strings.TrimLeft(p.lines[e.line], " \t")
	if e.disabled {
		raw = strings.TrimPrefix(raw, DisabledPrefix)
	}
	command, typ, taskName, wrapped := e.command, inferType(e.command), "", false
	if e.meta != nil {
		command, typ, taskName, wrapped = e.meta.Command, e.meta.Type, e.meta.Name, true
	}
	comment := p.commentOf(e)
	if taskName == "" {
		taskName = deriveTaskName(comment, command)
	}
	return Entry{
		Index: index, Type: typ, Schedule: e.schedule, Command: command,
		Comment: comment, Raw: raw, Enabled: !e.disabled,
		TaskName: taskName, Wrapped: wrapped,
	}
}

// commentOf 返回紧邻任务行上方的注释文本（去掉 # 与前导空格）。
func (p parsedCrontab) commentOf(e parsedEntry) string {
	if e.commentStart < 0 {
		return ""
	}
	parts := make([]string, 0, e.line-e.commentStart)
	for i := e.commentStart; i < e.line; i++ {
		if i == e.metaLine {
			continue // 元数据是内部结构，不属于用户备注
		}
		text := strings.TrimPrefix(strings.TrimLeft(p.lines[i], " \t"), "#")
		parts = append(parts, strings.TrimPrefix(text, " "))
	}
	return strings.Join(parts, "\n")
}

// appendEntry 在末尾追加可选注释、元数据行与任务行。
func (p *parsedCrontab) appendEntry(schedule, command, comment, metaLine string) {
	if comment != "" {
		for _, line := range strings.Split(comment, "\n") {
			p.lines = append(p.lines, "# "+line)
		}
	}
	if metaLine != "" {
		p.lines = append(p.lines, metaLine)
	}
	p.lines = append(p.lines, schedule+" "+command)
}

// removeEntry 删除第 idx 条任务及其紧邻注释块。
func (p *parsedCrontab) removeEntry(idx int) (Entry, error) {
	if idx < 0 || idx >= len(p.entries) {
		return Entry{}, wrap(ErrInvalidParam, "index 越界: "+itoa(idx)+"，共 "+itoa(len(p.entries))+" 条")
	}
	e := p.entries[idx]
	removed := p.entryOf(idx, e)
	start := e.line
	if e.commentStart >= 0 {
		start = e.commentStart
	}
	p.lines = append(p.lines[:start], p.lines[e.line+1:]...)
	return removed, nil
}

// replaceEntry 原地替换第 idx 条任务的 schedule 与 command，保留其注释；
// 停用状态也必须保留——替换参数只改定义，不隐含"顺手启用"。
func (p *parsedCrontab) replaceEntry(idx int, schedule, command string) (Entry, error) {
	if idx < 0 || idx >= len(p.entries) {
		return Entry{}, wrap(ErrInvalidParam, "index 越界: "+itoa(idx)+"，共 "+itoa(len(p.entries))+" 条")
	}
	e := p.entries[idx]
	if e.meta != nil {
		bin, err := os.Executable()
		if err != nil || bin == "" {
			return Entry{}, wrap(ErrIO, "取不到插件自身路径，无法重写包装命令")
		}
		meta := taskMeta{V: metaVersion, Type: inferType(command), Name: e.meta.Name, Command: command}
		metaLine := meta.encode()
		wrapped := wrapperCommand(bin, runlogFilePath(), meta.Name, command)
		if metaLine == "" || len(metaLine) > maxWrappedLineLen ||
			len(schedule)+1+len(wrapped) > maxWrappedLineLen {
			return Entry{}, wrap(ErrInvalidParam, "替换后的命令过长，无法保留执行日志包装")
		}
		p.lines[e.metaLine] = metaLine
		e.meta = &meta
		command = wrapped
	}
	line := schedule + " " + command
	if e.disabled {
		line = DisabledPrefix + line
	}
	p.lines[e.line] = line
	e.schedule, e.command = schedule, command
	return p.entryOf(idx, e), nil
}

// setEnabled 停用/启用第 idx 条任务，返回改后的条目与是否真的发生了变化。
//
// 停用只加注释标记、启用只去标记：任务行原文（schedule + command）与上方注释
// 都原样留在 crontab 里，因此 enable 能完整还原，不存在"停用即丢失定义"。
// 目标状态与当前一致时 changed=false，调用方不应写盘（避免无意义的备份）。
func (p *parsedCrontab) setEnabled(idx int, enabled bool) (Entry, bool, error) {
	if idx < 0 || idx >= len(p.entries) {
		return Entry{}, false, wrap(ErrInvalidParam, "index 越界: "+itoa(idx)+"，共 "+itoa(len(p.entries))+" 条")
	}
	e := p.entries[idx]
	if e.disabled == !enabled {
		return p.entryOf(idx, e), false, nil
	}
	line := strings.TrimLeft(p.lines[e.line], " \t")
	if enabled {
		line = strings.TrimPrefix(line, DisabledPrefix)
	} else {
		line = DisabledPrefix + line
	}
	p.lines[e.line] = line
	e.disabled = !enabled
	return p.entryOf(idx, e), true, nil
}
