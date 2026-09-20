// Package webhooknotify 示例业务插件：订单支付、服务开通/停用/删除等事件
// 向站点主配置的 URL POST JSON Webhook（HMAC-SHA256 签名，GitHub 风格）。
// 全链路示范插件框架：迁移（Migrator）+ 事件订阅 + 配置 schema 自动渲染
// （ConfigSchemaProvider）+ 管理路由（AdminRouteRegistrar）+ 后台菜单（AdminMenuProvider）。
package webhooknotify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"strconv"
	"time"

	"lumeidc/internal/plugin"
)

// deliverTimeout 单次投递超时；事件频率低，固定值即可。
const deliverTimeout = 10 * time.Second

// logKeep 投递日志保留条数（插入时裁切，免维护任务）。
const logKeep = 100

type Plugin struct {
	host *plugin.Host
}

func (p *Plugin) Info() plugin.Info {
	return plugin.Info{
		Name:        "webhooknotify",
		Title:       "Webhook 通知",
		Version:     "1.1.0",
		Description: "订单支付、服务开通/停用/删除等事件向自定义 URL 推送 JSON Webhook（HMAC-SHA256 签名）。",
	}
}

func (p *Plugin) Init(h *plugin.Host) error {
	p.host = h
	// 通配订阅全部事件（含其他插件注册的事件；实际投递范围由配置的事件勾选决定）。
	h.Subscribe("*", p.onEvent)
	return nil
}

// ConfigSchema 声明配置项：框架自动提供 /admin/plugin/webhooknotify/config
// 读写 API 与后台配置表单页（存 settings 表，经 host.Config 读取）。
func (p *Plugin) ConfigSchema() []plugin.ConfigField {
	return []plugin.ConfigField{
		{Key: "enabled", Title: "启用", Type: "switch"},
		{Key: "url", Title: "Webhook URL", Type: "text", Tip: "事件发生时向该地址 POST JSON（{event,time,data}）"},
		{Key: "secret", Title: "签名密钥", Type: "password", Tip: "可选；用于 X-LumeIDC-Signature 签名（HMAC-SHA256(body)）"},
		{Key: "events", Title: "订阅事件", Type: "multiselect", OptionsRef: "plugin.events"},
	}
}

//go:embed migrations/*.sql
var migrationsFS embed.FS

func (p *Plugin) Migrations() fs.FS { return migrationsFS }

// RegisterAdminRoutes 管理 API（子 mux 相对路径；/config 由框架统一提供）。
func (p *Plugin) RegisterAdminRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /logs", p.getLogs)
	mux.HandleFunc("POST /test", p.testSend)
}

func (p *Plugin) AdminMenu() plugin.MenuItem {
	return plugin.MenuItem{Title: "Webhook 通知", Icon: "ri:webhook-line"}
}

// AdminWidget 后台首页挂件：今日投递概况（示范挂件能力）。
func (p *Plugin) AdminWidget(ctx context.Context) (*plugin.WidgetData, error) {
	var today, failed int64
	if err := p.host.DB.QueryRowContext(ctx,
		`SELECT count(*), count(*) FILTER (WHERE error<>'' OR http_status<200 OR http_status>=300)
		 FROM plugin_webhooknotify_log WHERE created_at >= CURRENT_DATE`).Scan(&today, &failed); err != nil {
		return nil, err
	}
	return &plugin.WidgetData{
		Title: "Webhook 通知",
		Icon:  "ri:webhook-line",
		Rows: []plugin.WidgetRow{
			{Label: "今日投递", Value: itoa(today), To: "/plugin/webhooknotify"},
			{Label: "今日失败", Value: itoa(failed), To: "/plugin/webhooknotify"},
		},
	}, nil
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }

func init() { plugin.Register(&Plugin{}) }

// envelope 投递载荷：事件名 + 时间 + 业务数据。
type envelope struct {
	Event string `json:"event"`
	Time  string `json:"time"`
	Data  any    `json:"data"`
}

// onEvent 事件处理器：读配置、过滤事件开关后异步投递（Emit 同步执行，
// 网络投递必须起 goroutine，避免拖慢支付/开通主流程）。
func (p *Plugin) onEvent(ctx context.Context, payload any) error {
	event := plugin.EventName(ctx)
	if event == "" {
		return nil
	}
	cfg := p.loadConfig(ctx)
	if !cfg.Enabled || cfg.URL == "" || !cfg.eventOn(event) {
		return nil
	}
	go p.deliver(event, payload)
	return nil
}

// deliver 实际投递：签名 → POST → 落投递日志；失败重试 1 次。
// 独立于事件 ctx（其随请求结束即取消），用 Background 派生超时。
func (p *Plugin) deliver(event string, payload any) {
	cfg := p.loadConfig(context.Background())
	if cfg.URL == "" {
		return
	}
	body, err := json.Marshal(envelope{Event: event, Time: time.Now().UTC().Format(time.RFC3339), Data: payload})
	if err != nil {
		log.Printf("[webhooknotify] 事件 %s 序列化失败: %v", event, err)
		return
	}
	var lastStatus int
	var lastErr error
	var elapsed time.Duration
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			time.Sleep(2 * time.Second)
		}
		lastStatus, elapsed, lastErr = p.postOnce(cfg, event, body)
		if lastErr == nil && lastStatus >= 200 && lastStatus < 300 {
			break
		}
	}
	p.logDelivery(event, body, lastStatus, elapsed, lastErr)
}

func (p *Plugin) postOnce(cfg config, event string, body []byte) (status int, elapsed time.Duration, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), deliverTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.URL, bytes.NewReader(body))
	if err != nil {
		return 0, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-LumeIDC-Event", event)
	if cfg.Secret != "" {
		mac := hmac.New(sha256.New, []byte(cfg.Secret))
		mac.Write(body)
		req.Header.Set("X-LumeIDC-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}
	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, time.Since(start), err
	}
	defer resp.Body.Close()
	return resp.StatusCode, time.Since(start), nil
}

// logDelivery 落投递日志并裁切到最近 logKeep 条。
func (p *Plugin) logDelivery(event string, body []byte, status int, elapsed time.Duration, derr error) {
	errMsg := ""
	if derr != nil {
		errMsg = derr.Error()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := p.host.DB.ExecContext(ctx,
		`INSERT INTO plugin_webhooknotify_log(event,payload,http_status,error,duration_ms) VALUES($1,$2,$3,$4,$5)`,
		event, string(body), status, errMsg, elapsed.Milliseconds()); err != nil {
		log.Printf("[webhooknotify] 投递日志写入失败: %v", err)
		return
	}
	if _, err := p.host.DB.ExecContext(ctx,
		`DELETE FROM plugin_webhooknotify_log WHERE id NOT IN (
		   SELECT id FROM plugin_webhooknotify_log ORDER BY id DESC LIMIT $1)`, logKeep); err != nil {
		log.Printf("[webhooknotify] 投递日志裁切失败: %v", err)
	}
}
