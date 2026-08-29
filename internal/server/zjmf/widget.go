package zjmf

import (
	"context"
	"embed"
	"html/template"
	"strings"

	"lumeidc/internal/server"
)

//go:embed widget.html
var widgetFS embed.FS

var widgetTpl = template.Must(template.ParseFS(widgetFS, "widget.html"))

// DetailWidget 提供云主机专属管理界面。所有按钮仍调用本站服务 API，
// 不把魔方财务地址或上游 WebSocket 地址暴露给浏览器。
func (p Provider) DetailWidget(ctx context.Context, cfg server.Config, upstreamHostID int64, data server.WidgetData) (template.HTML, error) {
	var b strings.Builder
	view := map[string]any{
		"ServiceID":  data.ServiceID,
		"CSRF":       data.CSRF,
		"Password":   data.Password,
		"StatusText": data.StatusText,
		"Overview":   data.Overview,
	}
	if err := widgetTpl.Execute(&b, view); err != nil {
		return "", err
	}
	return template.HTML(b.String()), nil
}
