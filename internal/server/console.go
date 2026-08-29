package server

import "context"

// ConsoleProvider 实例控制台能力（开机/关机/重装等）。供应商按能力可选实现。
type ConsoleProvider interface {
	// PowerAction 开机 on / 关机 off / 重启 reboot / 硬关机 hard_off 等。
	PowerAction(ctx context.Context, cfg Config, upstreamHostID int64, action string) error
	// ResetPassword 重置实例密码；返回最终应用到实例的密码（为空/不合规时自动生成）。
	ResetPassword(ctx context.Context, cfg Config, upstreamHostID int64, password string) (string, error)
	// ReinstallOptions 可用的操作系统列表。
	ReinstallOptions(ctx context.Context, cfg Config, upstreamHostID int64) ([]OSOption, error)
	// Reinstall 重装系统。
	Reinstall(ctx context.Context, cfg Config, upstreamHostID int64, osID string) error
	// Rescue 进入救援模式。system: "1"=Windows, "2"=Linux（对齐上游取值）。
	Rescue(ctx context.Context, cfg Config, upstreamHostID int64, system string) error
	// Usage 流量/资源用量；无数据返回空结构。
	Usage(ctx context.Context, cfg Config, upstreamHostID int64) (UsageInfo, error)
}

// PasswordResetter 可选：重置实例密码能力（单独声明，虚拟主机类上游无需整套控制台）。
type PasswordResetter interface {
	ResetPassword(ctx context.Context, cfg Config, upstreamHostID int64, password string) (string, error)
}

// RescueProvider 可选：救援模式增强能力（临时密码/状态查询/退出）。
type RescueProvider interface {
	// RescueWithPass 带临时密码进入救援模式，返回最终应用的密码。
	RescueWithPass(ctx context.Context, cfg Config, upstreamHostID int64, system, tempPass string) (string, error)
	// RescueState 救援模式是否开启。
	RescueState(ctx context.Context, cfg Config, upstreamHostID int64) (bool, error)
	// ExitRescue 退出救援模式。
	ExitRescue(ctx context.Context, cfg Config, upstreamHostID int64) error
}

// OSOption 重装系统选项。
type OSOption struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Group string `json:"group,omitempty"`
}

// UsageInfo 资源用量。Limit<=0 表示不限或未知。
type UsageInfo struct {
	TrafficUsed  float64 `json:"traffic_used"`  // GB
	TrafficLimit float64 `json:"traffic_limit"` // GB
}

// VNCInfo VNC 会话信息（本站服务端反向代理用，完全隐藏上游域名）。
type VNCInfo struct {
	Password     string // VNC 密码（RFB 凭据，注入本站页面）
	WebSocketURL string // 上游 wss 地址（仅服务端拨号，不暴露给浏览器）
	AssetOrigin  string // 上游静态资源源站，如 https://tisula.com
}

// VNCInfoProvider 可选：返回结构化 VNC 会话信息（含密码与 wss），供服务端代理。
type VNCInfoProvider interface {
	VNCInfo(ctx context.Context, cfg Config, upstreamHostID int64) (VNCInfo, error)
}

var ErrNotSupported = consoleErr("该上游不支持此操作")

type consoleErr string

func (e consoleErr) Error() string { return string(e) }
