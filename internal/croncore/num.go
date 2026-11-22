package croncore

// atoi 把纯十进制字符串转为整数；非数字或溢出均返回 error。
// 单独封装避免引入 strconv 之外的第三方依赖，并保持校验逻辑可测。
func atoi(s string) (int, error) {
	if s == "" {
		return 0, ErrInvalidParam
	}
	n := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, ErrInvalidParam
		}
		n = n*10 + int(c-'0')
		if n > 1<<30 {
			return 0, ErrInvalidParam
		}
	}
	return n, nil
}

// itoa 把非负整数转为十进制字符串，用于错误信息，无 fmt 依赖。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
