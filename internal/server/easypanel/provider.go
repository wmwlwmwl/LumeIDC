package easypanel

import (
	"context"
	"embed"
	"fmt"
	"html/template"
	"strconv"
	"strings"

	"lumeidc/internal/server"
)

// Provider EasyPanel（kangle）上游适配器。
// 站点名规则：u{serviceID}（服务 ID 唯一稳定；EP 站点名仅允许小写字母数字）。
// 续费策略：上游建站不设过期（month 不传 = 永不过期），生命周期完全由本系统本地管控。
type Provider struct{}

func (Provider) Code() string { return "easypanel" }
func (Provider) Name() string { return "EasyPanel（kangle）" }

// PIDOptional 弹性模式：product_id 可省略，配额由订单配置项直传（web_quota 等）。
func (Provider) PIDOptional() bool { return true }

// MarkupFree EP 无上游成本概念（价格全本地定义），利润加成不适用。
func (Provider) MarkupFree() bool { return true }

// ProductFormHints 产品表单声明：PID 可留空走弹性模式；配置标识给出可直传 add_vh 的候选。
func (Provider) ProductFormHints() server.ProductFormHints {
	return server.ProductFormHints{
		PIDHint: "EasyPanel：填 EP 面板产品 ID（配额由 EP 产品定义）；留空 = 弹性模式，配额在下方配置项定义（field 用 web_quota/db_quota/templete 等，直传 add_vh）。",
		FieldSuggestions: []server.FieldSuggestion{
			{Field: "web_quota", Label: "网页空间 MB"},
			{Field: "db_quota", Label: "数据库大小 MB"},
			{Field: "db_type", Label: "数据库类型"},
			{Field: "subtemplete", Label: "指定 PHP 版本（php56/php74/php81…，缺省 5.6 可面板切换）"},
			{Field: "module", Label: "模块（php/iis，默认 php，一般不用配）"},
			{Field: "templete", Label: "模板（默认 easypanel 托管模式，勿改）"},
			{Field: "domain", Label: "可绑域名数"},
			{Field: "subdir_flag", Label: "允许子目录"},
			{Field: "max_subdir", Label: "最大子目录数"},
			{Field: "ftp", Label: "开启 FTP"},
			{Field: "ftp_connect", Label: "FTP 连接数"},
			{Field: "max_connect", Label: "最大连接数"},
			{Field: "max_worker", Label: "并发工作数"},
			{Field: "speed_limit", Label: "带宽限速 Mbps"},
			{Field: "flow_limit", Label: "月流量 MB"},
			{Field: "log_handle", Label: "日志处理"},
		},
	}
}

// CredentialFields EasyPanel 凭据：面板地址 + 通信安全码（无用户名概念）。
func (Provider) CredentialFields() []server.CredentialField {
	return []server.CredentialField{
		{Name: "api_url", Label: "面板地址", Placeholder: "如 http://1.2.3.4:3312", Required: true},
		{Name: "api_key", Label: "面板通信安全码", Placeholder: "EasyPanel 后台「服务器设置」里的安全码", Required: true, Secret: true},
	}
}

// SiteName 由本地服务 ID 派生上游站点名。
func SiteName(serviceID int64) string { return "u" + strconv.FormatInt(serviceID, 10) }

// serviceIDFromHost 反查服务 ID（Provision 返回的 hostID 即 serviceID）。
func serviceIDFromHost(hostID int64) (int64, error) {
	if hostID <= 0 {
		return 0, fmt.Errorf("EasyPanel 主机标识无效: %d", hostID)
	}
	return hostID, nil
}

// flexibleFields 弹性模式白名单：仅这些订单配置项会被透传为 add_vh 参数，
// 防止任意 field 注入协议保留参数（如 a/c/s/init）。
var flexibleFields = []string{
	"templete", "subtemplete", "module", "web_quota", "db_quota", "db_type",
	"subdir_flag", "subdir", "max_subdir", "domain", "ftp",
	"ftp_connect", "ftp_usl", "ftp_dsl", "max_connect", "max_worker",
	"speed_limit", "log_handle", "flow_limit", "cdn",
}

const ckAddVH = "ep_add_vh"

//go:embed productform.html
var productFormFS embed.FS

var productFormTpl = template.Must(template.ParseFS(productFormFS, "productform.html"))

// ProductFormWidget 产品表单独立区块：站点类型（虚拟主机/CDN）↔ 隐藏配置项 cdn。
func (Provider) ProductFormWidget() (template.HTML, error) {
	var sb strings.Builder
	if err := productFormTpl.Execute(&sb, nil); err != nil {
		return "", err
	}
	return template.HTML(sb.String()), nil
}

// TestConnection a=info 验证安全码与连通性。
func (p Provider) TestConnection(ctx context.Context, cfg server.Config) error {
	c := newClient(cfg)
	out, err := c.call(ctx, "info", nil)
	if err != nil {
		return err
	}
	if v := strField(out, "easypanel_version"); v != "" {
		return nil // EasyPanel 面板
	}
	return nil // kangle 也放行（部分老版仅 kangle 信息）
}

// Catalog EP 无商品目录 API：返回空（管理员在产品表单手填 EP 产品 ID 或走弹性模式）。
func (p Provider) Catalog(ctx context.Context, cfg server.Config) ([]server.UpstreamProduct, error) {
	return nil, nil
}

// Provision a=add_vh 创建站点。双模式：
//   - req.UpstreamPID>0：EP 产品 ID 模式（配额由 EP 面板产品定义）
//   - =0：弹性模式（白名单配置项直传配额参数）
//
// 幂等：500(重名) 时 getVh 验证存在即视为成功；此时密码不可知，用 change_password
// 强制同步为新密码，保证返回值与上游实际一致。
func (p Provider) Provision(ctx context.Context, cfg server.Config, req server.ProvisionRequest, ck server.CheckpointStore) (server.ProvisionResult, error) {
	if req.ServiceID <= 0 {
		return server.ProvisionResult{}, fmt.Errorf("EasyPanel 开通缺少本地服务 ID")
	}
	c := newClient(cfg)
	name := SiteName(req.ServiceID)

	pw := req.Password
	if pw == "" || !server.ValidHostPassword(pw) {
		pw = server.RandomHostPassword()
	}

	params := map[string]string{
		"init":   "1",
		"name":   name,
		"passwd": pw,
	}
	if req.UpstreamPID > 0 {
		params["product_id"] = strconv.FormatInt(req.UpstreamPID, 10)
	} else {
		// 弹性模式：白名单透传 + 必要默认值。
		// 模板必须用 "easypanel"（EP 托管模式）而非 "php"：templete=php 是裸 kangle 模板，
		// 用户面板不出现"切换php版本"；easypanel+module=php 才有版本切换（php56~php85）。
		for _, f := range flexibleFields {
			if v, ok := req.ConfigOpts[f]; ok && strings.TrimSpace(v) != "" {
				params[f] = strings.TrimSpace(v)
			}
		}
		if _, ok := params["templete"]; !ok {
			params["templete"] = "easypanel"
		}
		if _, ok := params["module"]; !ok && params["templete"] == "easypanel" {
			params["module"] = "php"
		}
		if _, ok := params["subdir_flag"]; !ok {
			params["subdir_flag"] = "1" // 允许绑定子目录
		}
		if _, ok := params["ftp"]; !ok {
			params["ftp"] = "1"
		}
	}

	// checkpoint：崩溃重试时先验证已建站则跳过 add_vh
	if ck != nil {
		if v, ok, err := ck.GetCheckpoint(ckAddVH); err == nil && ok && v == name {
			if _, gerr := p.getVh(ctx, c, name); gerr == nil {
				return server.ProvisionResult{UpstreamHostID: req.ServiceID, Password: pw}, nil
			}
		}
	}

	if _, err := c.call(ctx, "add_vh", params); err != nil {
		if apiCode(err) != 500 {
			return server.ProvisionResult{}, fmt.Errorf("EasyPanel 创建站点失败: %w", err)
		}
		// 500 = 站点名重复：幂等路径。验证确实存在后同步密码再返回成功。
		if _, gerr := p.getVh(ctx, c, name); gerr != nil {
			return server.ProvisionResult{}, fmt.Errorf("EasyPanel 创建站点失败: %w", err)
		}
		if _, cerr := c.call(ctx, "change_password", map[string]string{
			"name": name, "passwd": pw,
		}); cerr != nil {
			return server.ProvisionResult{}, fmt.Errorf("EasyPanel 重名站点密码同步失败: %w", cerr)
		}
	}
	if ck != nil {
		_ = ck.SetCheckpoint(ckAddVH, name)
	}
	return server.ProvisionResult{UpstreamHostID: req.ServiceID, Password: pw}, nil
}

// Renew 策略A：上游永不过期 + 本地管控，续费无需上游操作。
func (p Provider) Renew(ctx context.Context, cfg server.Config, upstreamHostID int64, cycle string) error {
	return nil
}

// Suspend a=update_vh status=1 关闭站点（到期停机）。
func (p Provider) Suspend(ctx context.Context, cfg server.Config, upstreamHostID int64) error {
	id, err := serviceIDFromHost(upstreamHostID)
	if err != nil {
		return err
	}
	c := newClient(cfg)
	if _, err := c.call(ctx, "update_vh", map[string]string{
		"name": SiteName(id), "status": "1",
	}); err != nil {
		return fmt.Errorf("EasyPanel 停机失败: %w", err)
	}
	return nil
}

// Unsuspend a=update_vh status=0 恢复站点。
func (p Provider) Unsuspend(ctx context.Context, cfg server.Config, upstreamHostID int64) error {
	id, err := serviceIDFromHost(upstreamHostID)
	if err != nil {
		return err
	}
	c := newClient(cfg)
	if _, err := c.call(ctx, "update_vh", map[string]string{
		"name": SiteName(id), "status": "0",
	}); err != nil {
		return fmt.Errorf("EasyPanel 恢复失败: %w", err)
	}
	return nil
}

// Terminate a=del_vh 删除站点。
func (p Provider) Terminate(ctx context.Context, cfg server.Config, upstreamHostID int64) error {
	id, err := serviceIDFromHost(upstreamHostID)
	if err != nil {
		return err
	}
	c := newClient(cfg)
	if _, err := c.call(ctx, "del_vh", map[string]string{
		"name": SiteName(id),
	}); err != nil {
		return fmt.Errorf("EasyPanel 删除失败: %w", err)
	}
	return nil
}

// getVh a=getVh 查询单个站点；不存在返回 errAPI(500)。
func (p Provider) getVh(ctx context.Context, c *client, name string) (map[string]any, error) {
	return c.call(ctx, "getVh", map[string]string{"name": name})
}

// Status a=getVh 映射上游站点状态。
func (p Provider) Status(ctx context.Context, cfg server.Config, upstreamHostID int64) (server.ServiceStatus, error) {
	id, err := serviceIDFromHost(upstreamHostID)
	if err != nil {
		return server.ServiceStatus{}, err
	}
	c := newClient(cfg)
	vh, err := p.getVh(ctx, c, SiteName(id))
	if err != nil {
		if apiCode(err) == 500 {
			return server.ServiceStatus{Status: "terminated"}, nil // 站点不存在
		}
		return server.ServiceStatus{}, fmt.Errorf("EasyPanel 查询状态失败: %w", err)
	}
	st := server.ServiceStatus{Status: "active", Hostname: strField(vh, "name")}
	if numField(vh, "status") == 1 {
		st.Status = "suspended"
	}
	// expire_time2=0 表示永不过期（策略A），不展示到期时间
	if t := numField(vh, "expire_time2"); t > 0 {
		st.ExpiryAt = strconv.FormatInt(int64(t), 10)
	}
	return st, nil
}

// ResetPassword a=change_password 重置站点密码（站点/FTP/数据库同步）。
// 返回最终应用的密码（入参为空或不合规时自动生成）。
func (p Provider) ResetPassword(ctx context.Context, cfg server.Config, upstreamHostID int64, password string) (string, error) {
	id, err := serviceIDFromHost(upstreamHostID)
	if err != nil {
		return "", err
	}
	if password == "" || !server.ValidHostPassword(password) {
		password = server.RandomHostPassword()
	}
	c := newClient(cfg)
	if _, err := c.call(ctx, "change_password", map[string]string{
		"name": SiteName(id), "passwd": password,
	}); err != nil {
		return "", fmt.Errorf("EasyPanel 重置密码失败: %w", err)
	}
	return password, nil
}

// HostOverview a=getVh 一次拉取详情页概况：登录信息 + 面板直登地址。
// Password 留空：由 Console 层用 services.password_crypt 解密填充（EP 查不回密码）。
func (p Provider) HostOverview(ctx context.Context, cfg server.Config, upstreamHostID int64) (server.HostOverview, error) {
	id, err := serviceIDFromHost(upstreamHostID)
	if err != nil {
		return server.HostOverview{}, err
	}
	c := newClient(cfg)
	vh, err := p.getVh(ctx, c, SiteName(id))
	if err != nil {
		return server.HostOverview{}, fmt.Errorf("EasyPanel 查询详情失败: %w", err)
	}
	d := server.HostDetail{
		Username: strField(vh, "name"),
		Status:   "运行中",
		OSName:   strField(vh, "module"), // php / iis
		PanelURL: c.base + "/vhost/index.php?c=session&a=login",
	}
	if numField(vh, "status") == 1 {
		d.Status = "已关闭"
	}
	return server.HostOverview{Detail: d}, nil
}
