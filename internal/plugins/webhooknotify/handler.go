package webhooknotify

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"lumeidc/internal/plugin"
)

// config 插件运行配置。存 settings 表（键 plugin.webhooknotify.*），
// 由框架统一 config API 读写（ConfigSchemaProvider）；这里只负责读。
type config struct {
	URL     string
	Secret  string
	Events  []string
	Enabled bool
}

func (c config) eventOn(event string) bool {
	for _, e := range c.Events {
		if e == event {
			return true
		}
	}
	return false
}

func (p *Plugin) loadConfig(ctx context.Context) config {
	c := config{
		URL:     p.host.Config(ctx, "url"),
		Secret:  p.host.Config(ctx, "secret"),
		Enabled: p.host.Config(ctx, "enabled") == "1",
	}
	_ = json.Unmarshal([]byte(p.host.Config(ctx, "events")), &c.Events)
	return c
}

// adminOK/writeJSON 统一由框架提供（plugin.AdminOK / plugin.WriteJSON）。

func (p *Plugin) getLogs(w http.ResponseWriter, r *http.Request) {
	if !plugin.AdminOK(w, r) {
		return
	}
	rows, err := p.host.DB.QueryContext(r.Context(),
		`SELECT id,event,payload,http_status,error,duration_ms,created_at
		 FROM plugin_webhooknotify_log ORDER BY id DESC LIMIT 50`)
	if err != nil {
		plugin.JSONFail(w, "查询失败")
		return
	}
	defer rows.Close()
	list := []map[string]any{}
	for rows.Next() {
		var (
			id, status, durationMs int64
			event, errMsg          string
			payload                json.RawMessage
			createdAt              time.Time
		)
		if err := rows.Scan(&id, &event, &payload, &status, &errMsg, &durationMs, &createdAt); err != nil {
			continue
		}
		list = append(list, map[string]any{
			"id": id, "event": event, "payload": payload, "httpStatus": status,
			"error": errMsg, "durationMs": durationMs,
			"createdAt": createdAt.Local().Format("2006-01-02 15:04:05"),
		})
	}
	plugin.WriteJSON(w, map[string]any{"ok": 1, "logs": list})
}

// testSend 发送一条测试事件到已配置 URL，便于核对连通性与签名。
func (p *Plugin) testSend(w http.ResponseWriter, r *http.Request) {
	if !plugin.AdminOK(w, r) {
		return
	}
	c := p.loadConfig(r.Context())
	if c.URL == "" {
		plugin.JSONFail(w, "请先保存 Webhook URL")
		return
	}
	body, _ := json.Marshal(envelope{
		Event: "test",
		Time:  time.Now().UTC().Format(time.RFC3339),
		Data:  map[string]string{"msg": "LumeIDC Webhook 连通性测试"},
	})
	status, elapsed, err := p.postOnce(c, "test", body)
	p.logDelivery("test", body, status, elapsed, err)
	if err != nil {
		plugin.JSONFail(w, "投递失败: "+err.Error())
		return
	}
	plugin.WriteJSON(w, map[string]any{"ok": 1, "httpStatus": status, "durationMs": elapsed.Milliseconds()})
}
