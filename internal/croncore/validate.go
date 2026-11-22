package croncore

import "strings"

// maxCommandLen / maxCommentLen 是命令与注释的长度上限，防止恶意超长入参。
const (
	maxCommandLen = 512
	maxCommentLen = 512
)

// ValidateCommand 校验命令：拒绝空命令、换行、回车与超长内容。
// 换行/回车是重点：crontab 按行解析，允许它们等于允许注入多条任务。
// 命令只作为字符串存储，不解析、不执行。
func ValidateCommand(command string) error {
	if strings.ContainsAny(command, "\r\n\x00") {
		return wrap(ErrInvalidParam, "command 不得包含换行或回车")
	}
	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		return wrap(ErrInvalidParam, "command 不能为空")
	}
	if len(trimmed) > maxCommandLen {
		return wrap(ErrInvalidParam, "command 不得超过 512 字节")
	}
	return nil
}

// ValidateComment 校验注释：允许为空，但不得含换行/回车（防止注释行注入）。
func ValidateComment(comment string) error {
	if strings.ContainsAny(comment, "\r\n\x00") {
		return wrap(ErrInvalidParam, "comment 不得包含换行或回车")
	}
	if len(strings.TrimSpace(comment)) > maxCommentLen {
		return wrap(ErrInvalidParam, "comment 不得超过 512 字节")
	}
	return nil
}

// cleanCommand 校验后返回去掉首尾空白的命令，供写回 crontab 使用。
func cleanCommand(command string) (string, error) {
	if err := ValidateCommand(command); err != nil {
		return "", err
	}
	return strings.TrimSpace(command), nil
}

// cleanSchedule 归一化空白并复用 5 段语法校验。
func cleanSchedule(schedule string) (string, error) {
	schedule = strings.Join(strings.Fields(schedule), " ")
	if err := ValidateSchedule(schedule); err != nil {
		return "", err
	}
	return schedule, nil
}

// cleanComment 校验后返回去掉首尾空白的注释；空注释视为无注释。
func cleanComment(comment string) (string, error) {
	if err := ValidateComment(comment); err != nil {
		return "", err
	}
	return strings.TrimSpace(comment), nil
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
