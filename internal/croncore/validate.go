package croncore

import "strings"

// maxNameLen / maxCmdLen 是任务定义的长度上限，防止恶意超长入参。
const (
	maxNameLen = 64
	maxCmdLen  = 4096
)

// ValidateName 校验任务名：非空、长度受限、只含字母数字下划线与连字符。
func ValidateName(name string) error {
	if name == "" || len(name) > maxNameLen {
		return wrap(ErrInvalidParam, "name 必须为 1-64 字符")
	}
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '_' || r == '-' {
			continue
		}
		return wrap(ErrInvalidParam, "name 仅允许字母数字下划线与连字符")
	}
	return nil
}

// ValidateSchedule 只做 5 段 crontab 语法校验，不计算触发时间、不调度。
// 字段：minute hour day-of-month month day-of-week。每段允许 *、数字、
// 数字-数字范围、*/数字 步长，以及逗号分隔的列表。
func ValidateSchedule(schedule string) error {
	fields := strings.Fields(schedule)
	if len(fields) != 5 {
		return wrap(ErrInvalidParam, "schedule 必须恰为 5 段 crontab 字段")
	}
	bounds := [5][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 7}}
	for i, field := range fields {
		if err := validateField(field, bounds[i][0], bounds[i][1]); err != nil {
			return wrap(ErrInvalidParam, "schedule 第 "+itoa(i+1)+" 段非法: "+err.Error())
		}
	}
	return nil
}

// validateField 校验单段：逗号分隔的每项为 * / n / a-b / *\/n / a-b\/n。
func validateField(field string, lo, hi int) error {
	if field == "" {
		return ErrInvalidParam
	}
	for _, item := range strings.Split(field, ",") {
		if err := validateItem(item, lo, hi); err != nil {
			return err
		}
	}
	return nil
}

// validateItem 校验单个 crontab 项。
func validateItem(item string, lo, hi int) error {
	base := item
	step := 1
	if idx := strings.IndexByte(item, '/'); idx >= 0 {
		base = item[:idx]
		stepStr := item[idx+1:]
		n, err := atoi(stepStr)
		if err != nil || n <= 0 {
			return ErrInvalidParam
		}
		step = n
	}
	if base == "*" {
		return nil
	}
	if loBound, hiBound, ok := cutRange(base); ok {
		a, ea := atoi(loBound)
		b, eb := atoi(hiBound)
		if ea != nil || eb != nil || a < lo || b > hi || a > b {
			return ErrInvalidParam
		}
		return nil
	}
	n, err := atoi(base)
	if err != nil || n < lo || n > hi {
		return ErrInvalidParam
	}
	_ = step
	return nil
}

// cutRange 拆分 a-b，返回 (a, b, true)；无连字符返回 ok=false。
func cutRange(s string) (string, string, bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == '-' {
			return s[:i], s[i+1:], true
		}
	}
	return "", "", false
}
