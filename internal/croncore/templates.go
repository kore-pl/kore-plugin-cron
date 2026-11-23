package croncore

import "context"

// Template 是只读的常用任务模板：前端一键填入表单，不写 crontab、不执行任何动作。
type Template struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Schedule    string `json:"schedule"`
	Type        string `json:"type"`
	Command     string `json:"command,omitempty"`
	URL         string `json:"url,omitempty"`
	Comment     string `json:"comment,omitempty"`
}

// TemplatesResult 是 cron.templates 的结果；只读、无参数、不产生副作用。
type TemplatesResult struct {
	Templates []Template `json:"templates"`
	Count     int        `json:"count"`
	Note      string     `json:"note"`
}

// builtinTemplates 是内置模板（3–5 条）：只描述"怎么配"，不含任何环境探测。
var builtinTemplates = []Template{
	{
		ID: "daily-backup", Name: "每日备份站点", Description: "每天 03:00 打包站点目录到 /var/backups（需按实际路径调整）",
		Schedule: "0 3 * * *", Type: TypeShell,
		Command: "tar czf /var/backups/site-$(date +%F).tar.gz /var/www", Comment: "每日备份站点",
	},
	{
		ID: "hourly-health", Name: "每小时健康检查", Description: "每小时请求一次健康检查地址，失败返回非 0 退出码",
		Schedule: "0 * * * *", Type: TypeHTTP,
		URL: "https://example.com/health", Comment: "每小时健康检查",
	},
	{
		ID: "weekly-log-cleanup", Name: "每周清理日志", Description: "每周日 04:00 删除 14 天前的 .log 文件（先确认目录范围）",
		Schedule: "0 4 * * 0", Type: TypeShell,
		Command: "find /var/log -name '*.log' -mtime +14 -delete", Comment: "每周清理日志",
	},
	{
		ID: "monthly-cert-check", Name: "每月证书检查", Description: "每月 1 日 06:00 检查 HTTPS 证书端点可用性",
		Schedule: "0 6 1 * *", Type: TypeHTTP,
		URL: "https://example.com", Comment: "每月证书检查",
	},
}

// Templates 返回只读模板列表（cron.templates）。
func (s *Store) Templates(ctx context.Context) (*TemplatesResult, error) {
	if err := s.preflight(ctx, false); err != nil {
		return nil, err
	}
	out := make([]Template, len(builtinTemplates))
	copy(out, builtinTemplates)
	return &TemplatesResult{
		Templates: out, Count: len(out),
		Note: "模板只读：点击填入表单后仍需确认路径/地址再保存，保存才会写入 crontab",
	}, nil
}
