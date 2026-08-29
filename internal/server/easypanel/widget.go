package easypanel

import (
	"context"
	"embed"
	"fmt"
	"html/template"
	"strings"
	"time"

	"lumeidc/internal/server"
)

//go:embed widget.html
var widgetFS embed.FS

var widgetTpl = template.Must(template.ParseFS(widgetFS, "widget.html"))

// humanQuota MB 数值 → 人类可读（1024MB → 1G）。0/null 返回 "-"。
func humanQuota(mb float64) string {
	if mb <= 0 {
		return "-"
	}
	if mb >= 1024 && int(mb)%1024 == 0 {
		return fmt.Sprintf("%dG", int(mb)/1024)
	}
	return fmt.Sprintf("%dM", int(mb))
}

// DetailWidget EP 虚拟主机专属管理面板：登录信息 + 面板直登 + 重置密码 + 空间/数据库用量。
// 数据实时取 getVh / getDbUsed；单项失败不阻断整体（用量显示 "-"）。
func (p Provider) DetailWidget(ctx context.Context, cfg server.Config, upstreamHostID int64, data server.WidgetData) (template.HTML, error) {
	id, err := serviceIDFromHost(upstreamHostID)
	if err != nil {
		return "", err
	}
	c := newClient(cfg)
	name := SiteName(id)

	out := map[string]any{
		"ServiceID": data.ServiceID,
		"CSRF":      data.CSRF,
		"Password":  data.Password,
		"Name":      name,
		"WebQuota":  "-",
		"DBUsed":    "-",
	}
	vh, err := p.getVh(ctx, c, name)
	if err == nil {
		out["WebQuota"] = humanQuota(numField(vh, "web_quota"))
		if q := humanQuota(numField(vh, "db_quota")); q != "-" {
			out["DBQuota"] = q
		}
		if n := strField(vh, "db_name"); n != "" {
			out["DBName"] = n
		}
		if v := strField(vh, "db_type"); v != "" {
			out["DBType"] = v
		}
		out["FTP"] = numField(vh, "ftp") == 1
		if d := strField(vh, "domain"); d != "" && d != "0" {
			out["Domain"] = map[string]string{"-1": "不限"}[d]
			if out["Domain"] == nil {
				out["Domain"] = d
			}
		}
		if f := numField(vh, "flow_limit"); f > 0 {
			out["FlowLimit"] = humanQuota(f)
		}
		if s := numField(vh, "speed_limit"); s > 0 {
			out["SpeedLimit"] = fmt.Sprintf("%dMbps", int(s))
		}
		if t := numField(vh, "create_time"); t > 0 {
			out["CreateTime"] = time.Unix(int64(t), 0).Format("2006-01-02")
		}
		out["PanelURL"] = c.base + "/vhost/index.php?c=session&a=login"
	}
	// 数据库用量独立拉取，失败仅显示 "-"
	if u, uerr := c.call(ctx, "getDbUsed", map[string]string{"name": name}); uerr == nil {
		if used := numField(u, "used"); used > 0 {
			out["DBUsed"] = humanQuota(used)
		}
	}

	var sb strings.Builder
	if err := widgetTpl.Execute(&sb, out); err != nil {
		return "", err
	}
	return template.HTML(sb.String()), nil
}
