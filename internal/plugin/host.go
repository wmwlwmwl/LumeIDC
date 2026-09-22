package plugin

import (
	"context"
	"database/sql"
	"path/filepath"
)

// SettingsStore 站点设置读写。窄接口：plugin 包只依赖标准库与自定窄接口，
// 由组合根用 *repo.Settings 适配注入，从根上杜绝与 service/repo 的循环依赖。
type SettingsStore interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key, value string) error
}

// NotifySender 站内通知/邮件发送（窄接口，与 service.Notifier 对应方法签名一致，
// 组合根直接注入）。
type NotifySender interface {
	NotifyTemplate(ctx context.Context, userID int64, code, title, body string, values ...map[string]string) error
	// Notify 通用站内消息（无邮件模板 code；开启邮件转发时以纯文本补发）。供业务插件通知用户。
	Notify(ctx context.Context, userID int64, title, body string) error
	// NotifyAdminOnce 按 alertKey 去重的管理员告警（同日同 key 只发一次，适合日报/告警）。
	NotifyAdminOnce(ctx context.Context, alertKey, category, subject, body string) error
}

// Refunder 已支付订单退款（窄接口，与 service.Payment.Refund 签名一致，组合根注入）。
// 未注入（nil）时插件应降级为「仅记录待人工处理」，不得 panic。
type Refunder interface {
	Refund(ctx context.Context, adminID, orderID int64, amount, reason, method string) error
}

// Host 宿主注入给插件的能力。组合根在 Init 循环中经 forPlugin 派生
// 携带插件名的副本——订阅归属、配置键前缀、私有目录均依赖该名。
type Host struct {
	DB       *sql.DB
	Settings SettingsStore
	Notify   NotifySender
	// Refunder 退款执行能力（可选）；核心未注入时为 nil，插件需自行降级。
	Refunder Refunder
	// PrivateRoot 站点私有数据根目录（不可公网访问）；插件派生自有目录用
	// PrivateDir()，未配置时为空串。
	PrivateRoot string

	pluginName string // forPlugin 派生时填入
}

// forPlugin 派生携带插件名的 Host 副本（仅组合根使用）。
func (h *Host) forPlugin(name string) *Host {
	cp := *h
	cp.pluginName = name
	return &cp
}

// ForPlugin 供组合根（httpserver）在 Init 循环中派生 per-plugin Host。
func (h *Host) ForPlugin(name string) *Host { return h.forPlugin(name) }

// Subscribe 订阅核心或插件事件（自动携带插件归属，禁用时不再投递）。
func (h *Host) Subscribe(event string, fn EventHandler) {
	subscribeAs(h.pluginName, event, fn)
}

// SubscribeFilter 注册过滤器（自动携带插件归属，禁用时跳过）。
func (h *Host) SubscribeFilter(name string, fn FilterHandler) {
	subscribeFilterAs(h.pluginName, name, fn)
}

// Config 读取本插件配置（settings 键 plugin.{name}.{key}）；缺失返回空串。
// 配合 ConfigSchemaProvider 使用：schema 声明字段，框架统一渲染配置页并存取。
func (h *Host) Config(ctx context.Context, key string) string {
	if h.Settings == nil || h.pluginName == "" {
		return ""
	}
	v, err := h.Settings.Get(ctx, "plugin."+h.pluginName+"."+key)
	if err != nil {
		return ""
	}
	return v
}

// PrivateDir 本插件私有存储目录（PrivateRoot/plugin/{name}）；调用方负责 MkdirAll。
// PrivateRoot 未配置时返回空串，插件应回退或报错。
func (h *Host) PrivateDir() string {
	if h.PrivateRoot == "" || h.pluginName == "" {
		return ""
	}
	return filepath.Join(h.PrivateRoot, "plugin", h.pluginName)
}
