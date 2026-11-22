package croncore

import "strings"

// parsedCrontab 是 crontab 文本的按行解析结果：lines 保留每一行原文
// （注释、空行、环境变量、无法识别的行都原样保留），entries 只记录可管理的
// 5 段任务行及其行号与紧邻注释块，改动时不重排、不改写无关行。
type parsedCrontab struct {
	lines   []string
	entries []parsedEntry
}

// parsedEntry 是一条任务行：line 为下标，commentStart 为紧邻其上方连续
// 注释块的起始下标（-1 表示无注释）。
type parsedEntry struct {
	line         int
	commentStart int
	schedule     string
	command      string
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
func parseCrontab(content string) parsedCrontab {
	p := parsedCrontab{lines: splitLines(content)}
	pending := -1
	for i, raw := range p.lines {
		trimmed := strings.TrimLeft(raw, " \t")
		if strings.TrimSpace(raw) == "" {
			pending = -1
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			if pending < 0 {
				pending = i
			}
			continue
		}
		schedule, command, ok := splitEntry(trimmed)
		if !ok || ValidateSchedule(schedule) != nil {
			pending = -1
			continue
		}
		p.entries = append(p.entries, parsedEntry{
			line: i, commentStart: pending, schedule: schedule, command: command,
		})
		pending = -1
	}
	return p
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
		entries = append(entries, Entry{
			Index: i, Schedule: e.schedule, Command: e.command,
			Comment: p.commentOf(e), Raw: p.lines[e.line],
		})
	}
	return entries
}

// commentOf 返回紧邻任务行上方的注释文本（去掉 # 与前导空格）。
func (p parsedCrontab) commentOf(e parsedEntry) string {
	if e.commentStart < 0 {
		return ""
	}
	parts := make([]string, 0, e.line-e.commentStart)
	for i := e.commentStart; i < e.line; i++ {
		text := strings.TrimPrefix(strings.TrimLeft(p.lines[i], " \t"), "#")
		parts = append(parts, strings.TrimPrefix(text, " "))
	}
	return strings.Join(parts, "\n")
}

// appendEntry 在末尾追加可选注释行与任务行。
func (p *parsedCrontab) appendEntry(schedule, command, comment string) {
	if comment != "" {
		for _, line := range strings.Split(comment, "\n") {
			p.lines = append(p.lines, "# "+line)
		}
	}
	p.lines = append(p.lines, schedule+" "+command)
}

// removeEntry 删除第 idx 条任务及其紧邻注释块。
func (p *parsedCrontab) removeEntry(idx int) (Entry, error) {
	if idx < 0 || idx >= len(p.entries) {
		return Entry{}, wrap(ErrInvalidParam, "index 越界: "+itoa(idx)+"，共 "+itoa(len(p.entries))+" 条")
	}
	e := p.entries[idx]
	removed := Entry{
		Index: idx, Schedule: e.schedule, Command: e.command,
		Comment: p.commentOf(e), Raw: p.lines[e.line],
	}
	start := e.line
	if e.commentStart >= 0 {
		start = e.commentStart
	}
	p.lines = append(p.lines[:start], p.lines[e.line+1:]...)
	return removed, nil
}

// replaceEntry 原地替换第 idx 条任务的 schedule 与 command，保留其注释。
func (p *parsedCrontab) replaceEntry(idx int, schedule, command string) (Entry, error) {
	if idx < 0 || idx >= len(p.entries) {
		return Entry{}, wrap(ErrInvalidParam, "index 越界: "+itoa(idx)+"，共 "+itoa(len(p.entries))+" 条")
	}
	e := p.entries[idx]
	p.lines[e.line] = schedule + " " + command
	return Entry{
		Index: idx, Schedule: schedule, Command: command,
		Comment: p.commentOf(e), Raw: p.lines[e.line],
	}, nil
}
