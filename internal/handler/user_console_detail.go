package handler

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"lumeidc/internal/middleware"
	"lumeidc/internal/repo"
	"lumeidc/internal/server"
	"lumeidc/internal/service"
)

func (h *Pages) serviceDetail(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("serviceID"), 10, 64)
	d, err := h.Svc.GetDetail(r.Context(), id, userID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	// 续费周期仅展示该产品有实际价格（>0）的选项，避免按 0 价/月价误续费。
	psID, _ := h.Products.DefaultPricesetID(r.Context())
	var showMonthly, showQ, showY bool
	defaultCycle := "monthly"
	renewPrices := map[string]string{"monthly": d.Amount, "quarterly": "", "yearly": ""}
	// 有冻结的续费价（后台可改可清空）时以它为准：下单成交额含一次性初装费，不能当续费价展示
	// （否则开通页 33、续费实收 28 自相矛盾；冻结价本身已剔除此项，见 Payment.frozenRenewAmount）。
	if d.RenewM != "" {
		renewPrices["monthly"] = d.RenewM
	}
	if pr, perr := h.Products.Price(r.Context(), d.ProductID, psID); perr == nil {
		monthly, quarterly, yearly := priceVal(pr.Monthly), priceVal(pr.Quarterly), priceVal(pr.Yearly)
		cycles, selected := service.AvailableCycles(monthly, quarterly, yearly)
		showMonthly = len(cycles) == 0 || monthly > 0
		showQ = quarterly > 0
		showY = yearly > 0
		if selected != "" {
			defaultCycle = selected
		}
		renewPrices["quarterly"] = pr.Quarterly
		renewPrices["yearly"] = pr.Yearly
		if renewPrices["monthly"] == "" {
			renewPrices["monthly"] = pr.Monthly
		}
	}
	if d.RenewQ != "" {
		renewPrices["quarterly"] = d.RenewQ
	}
	if d.RenewY != "" {
		renewPrices["yearly"] = d.RenewY
	}
	csrf := h.pageCSRF(w, r)
	// 升降级入口：仅上游支持升降级（或有本地可升级目标）时显示；60s 缓存探测结果。
	var canUpgrade bool
	if h.Console != nil {
		canUpgrade = h.Console.CanUpgrade(r.Context(), userID, d.ID)
	}
	// 功能面板：已开通（status 1/2）且上游提供模块方块时，返回方块 key 列表
	// （nat_acl/nat_web/security_groups/setting/snapshot…），前端据此渲染对应标签页。
	modules := []string{}
	if d.HostSnapshot != nil && (d.Status == 1 || d.Status == 2) {
		for _, a := range d.HostSnapshot.Summary.Areas {
			modules = append(modules, a.Key)
		}
	}
	configs := make([]map[string]any, 0, len(d.Configs))
	for _, c := range d.Configs {
		configs = append(configs, map[string]any{"name": c.Name, "value": c.Value, "price": c.Price})
	}
	// 上游登录信息/面板直登地址使用最近一次本地快照，避免打开详情时自动请求上游。
	configDesc := d.ConfigNote
	if d.Provider == "easypanel" {
		if summary := easyPanelConfigSummary(d.HostSnapshot); summary != "" {
			configDesc = summary
		}
	}
	var host map[string]any
	if d.HostSnapshot != nil {
		ov := d.HostSnapshot
		host = map[string]any{
			"username": ov.Detail.Username, "password": ov.Detail.Password,
			"panel_url": panelDisplayURL(ov.Detail.PanelURL),
			// 自动登录表单的提交地址；旧快照没有该字段，回退到 panel_url
			// （旧快照的 panel_url 本身就是 a=login 提交入口，语义刚好对得上）。
			"panel_login_url": panelLoginURL(ov.Detail.PanelLoginURL, ov.Detail.PanelURL),
			"status":          ov.Detail.Status,
			"os":              ov.Detail.OSName, "ip": ov.Detail.IP,
			"additional_ips": ov.Detail.AdditionalIPs, "bw_limit": ov.Detail.BWLimit,
			"bw_usage": ov.Detail.BWUsage, "datacenter": ov.Detail.Datacenter,
			"os_version": ov.Detail.OSVersion, "port": ov.Detail.Port,
			"web_quota": ov.Detail.WebQuota, "db_name": ov.Detail.DBName,
			"db_quota": ov.Detail.DBQuota, "db_used": ov.Detail.DBUsed,
			"ftp": ov.Detail.FTP, "domain": ov.Detail.Domain,
			"flow_limit": ov.Detail.FlowLimit, "speed_limit": ov.Detail.SpeedLimit,
			"create_time": ov.Detail.CreateTime,
		}
	}
	writeJSON(w, map[string]any{
		"ok":   1,
		"csrf": csrf,
		"svc": map[string]any{
			"id": d.ID, "name": d.Name, "status": d.Status, "status_text": d.StatusText,
			"hostname": d.Hostname, "product_id": d.ProductID, "remark": d.Remark,
			"expires_at": d.ExpiresAt.Format("2006-01-02 15:04"), "created_at": d.CreatedAt.Format("2006-01-02"),
			"cycle": d.Cycle, "amount": d.Amount, "configs": configs,
			"config_desc": configDesc,
			"provider":    d.Provider,
			"transition":  d.Transition,
		},
		"host":         host,
		"show_monthly": showMonthly, "show_q": showQ, "show_y": showY, "default_cycle": defaultCycle,
		"renew_prices": renewPrices, "can_upgrade": canUpgrade, "modules": modules,
	})
}

func easyPanelConfigSummary(overview *server.HostOverview) string {
	if overview == nil {
		return ""
	}
	d := overview.Detail
	web := strings.TrimSpace(d.WebQuota)
	if web == "" {
		web = "-"
	}
	db := strings.TrimSpace(d.DBQuota)
	if db == "" {
		db = "-"
	}
	domain := strings.TrimSpace(d.Domain)
	if domain == "" {
		domain = "-"
	}
	return fmt.Sprintf("你现在是：网页空间 %s，数据库 %s，域名%s", web, db, domain)
}

func (h *Pages) serviceRefresh(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("serviceID"), 10, 64)
	if h.Console == nil {
		jsonFail(w, "实例服务不可用")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	ov, err := h.Console.Overview(ctx, userID, id)
	if err != nil {
		jsonFail(w, consoleErrMsg(err))
		return
	}
	if err := h.Svc.SaveHostSnapshot(r.Context(), id, userID, ov); err != nil {
		jsonFail(w, "保存实例信息失败")
		return
	}
	detail, detailErr := h.Svc.GetDetail(r.Context(), id, userID)
	if detailErr == nil && detail.Provider == "zjmf" {
		if expiry, err := parseUpstreamExpiryIn(ov.Detail.ExpiryAt, upstreamLocation(r.Context(), h.Settings)); err == nil {
			if err := h.Svc.UpdateExpiryFromHost(r.Context(), id, userID, expiry); err != nil {
				jsonFail(w, "保存到期时间失败")
				return
			}
		}
	}
	writeJSON(w, map[string]any{"ok": 1})
}

// upstreamLocation 上游面板时区：site 设置 upstream_timezone（IANA 名）优先，
// 留空回退本机时区。上游（如魔方财务 nextduedate）返回的无时区时间串按此时区解释——
// ponytail: 无法从上游响应自动探测时区，显式配置是唯一可靠手段；
// EasyPanel 传 Unix 时间戳（绝对时刻），不受此设置影响。
func upstreamLocation(ctx context.Context, s *repo.Settings) *time.Location {
	if s != nil {
		if v, err := s.Get(ctx, service.KeyUpstreamTimezone); err == nil {
			if v = strings.TrimSpace(v); v != "" {
				if loc, lerr := time.LoadLocation(v); lerr == nil {
					return loc
				}
			}
		}
	}
	return time.Local
}

func parseUpstreamExpiryIn(value string, loc *time.Location) (time.Time, error) {
	value = strings.TrimSpace(value)
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02"} {
		if parsed, err := time.ParseInLocation(layout, value, loc); err == nil {
			return parsed, nil
		}
	}
	if unix, err := strconv.ParseInt(value, 10, 64); err == nil && unix > 0 {
		if unix > 1e12 {
			unix /= 1000
		}
		return time.Unix(unix, 0), nil
	}
	return time.Time{}, fmt.Errorf("无法解析到期时间")
}

// consoleAction POST /services/{id}/console — 电源/重装/改密/救援统一入口。

func (h *Pages) consoleAction(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	serviceID, _ := strconv.ParseInt(r.PathValue("serviceID"), 10, 64)
	vals := jsonVals(r)
	fv := func(k string) string {
		if vals != nil {
			return vals[k]
		}
		return r.PostFormValue(k)
	}
	action := fv("do")

	var err error
	var appliedPw string
	switch action {
	case "on", "off", "reboot", "hard_off", "hard_reboot":
		err = h.Console.Power(r.Context(), userID, serviceID, action)
	case "crack_pass":
		appliedPw, err = h.Console.ResetPassword(r.Context(), userID, serviceID, fv("password"))
	case "reinstall":
		err = h.Console.Reinstall(r.Context(), userID, serviceID, fv("os"))
	case "rescue":
		appliedPw, err = h.Console.RescueWithPass(r.Context(), userID, serviceID,
			fv("system"), fv("temp_pass"))
	case "exit_rescue":
		err = h.Console.ExitRescue(r.Context(), userID, serviceID)
	default:
		jsonStatus(w, r, http.StatusBadRequest, "未知操作")
		return
	}
	dest := "/services/" + strconv.FormatInt(serviceID, 10)
	if err != nil {
		// 原文含上游地址，只进服务端日志；回显与服务日志统一用收敛后的文案。
		msg := consoleErrMsg(err)
		if !wantsJSON(r) {
			if sess := middleware.FromSession(r.Context()); sess != nil {
				sess.SetFlash("操作失败：" + msg)
			}
		}
		h.Svc.AppendLog(r.Context(), serviceID, userID, opLabel(action), "失败："+msg)
		if wantsJSON(r) {
			writeJSON(w, map[string]any{"ok": 0, "msg": "操作失败：" + msg})
			return
		}
	} else {
		msg := actionName(action)
		if action == "rescue" {
			msg = "救援系统已启动（实例将重启挂载救援系统）。临时密码：" + appliedPw
		} else if action == "crack_pass" {
			msg = "密码已重置，新密码：" + appliedPw
		}
		if !wantsJSON(r) {
			if sess := middleware.FromSession(r.Context()); sess != nil {
				sess.SetFlash(msg)
			}
		}
		h.Svc.AppendLog(r.Context(), serviceID, userID, opLabel(action), "成功")
		if wantsJSON(r) {
			writeJSON(w, map[string]any{"ok": 1, "msg": msg})
			return
		}
	}
	http.Redirect(w, r, dest, http.StatusSeeOther)
}

// panelDisplayURL 面板展示地址：存量快照里存的是带 ?c=session&a=login 的提交地址，
// 直接展示会让用户点开（GET）时被当成一次空凭证登录提交而报"账号密码错误"。
// 这里把历史数据里的提交参数剥掉，还原成可点开的面板首页。
func panelDisplayURL(raw string) string {
	if i := strings.Index(raw, "?c=session&a=login"); i > 0 {
		return raw[:i]
	}
	return raw
}

// panelLoginURL 自动登录表单的提交地址：新快照有独立 PanelLoginURL；
// 旧快照没有，回退到 panel_url（旧值本身即 a=login 提交入口）。
func panelLoginURL(loginURL, panelURL string) string {
	if strings.TrimSpace(loginURL) != "" {
		return loginURL
	}
	return panelURL
}

// reinstallOptions GET — 返回可用 OS 列表 JSON。
