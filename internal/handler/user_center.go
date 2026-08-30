package handler

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"lumeidc/internal/middleware"
	"lumeidc/internal/server"
	"lumeidc/internal/service"
)

type userStats struct {
	ServiceCount int64
	ActiveCount  int64
	PaidTotal    string
	UnpaidCount  int64
}

func (h *Pages) stats(r *http.Request, userID int64) userStats {
	var s userStats
	db := h.Svc.DB
	_ = db.QueryRowContext(r.Context(),
		`SELECT count(*),count(*) FILTER (WHERE status=1) FROM services WHERE user_id=$1 AND status<3`, userID).
		Scan(&s.ServiceCount, &s.ActiveCount)
	_ = db.QueryRowContext(r.Context(),
		`SELECT coalesce(sum(amount),0) FROM invoices WHERE user_id=$1 AND status=1`, userID).
		Scan(&s.PaidTotal)
	_ = db.QueryRowContext(r.Context(),
		`SELECT count(*) FROM invoices WHERE user_id=$1 AND status=0`, userID).
		Scan(&s.UnpaidCount)
	return s
}

func (h *Pages) userHome(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	bal, _ := h.Balance.Get(r.Context(), userID)
	render(w, r, "user_home.html", map[string]any{
		"Stats":         h.stats(r, userID),
		"Balance":       bal,
		"Announcements": h.listAnnouncements(r.Context())})
}

func (h *Pages) rechargeForm(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	bal, _ := h.Balance.Get(r.Context(), userID)
	render(w, r, "user_recharge.html", map[string]any{
		"Balance": bal, "CSRF": csrfOf(sessionsStore, w, r), "Error": r.URL.Query().Get("err"),
	})
}

func (h *Pages) rechargeSubmit(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	tok := r.PostFormValue("_csrf")
	sess := middleware.FromSession(r.Context())
	if tok == "" || sess == nil || tok != sess.CSRFToken() {
		http.Error(w, "CSRF 校验失败", http.StatusForbidden)
		return
	}
	id, err := h.Orders.CreateRechargeInvoice(r.Context(), userID, strings.TrimSpace(r.PostFormValue("amount")))
	if err != nil {
		http.Redirect(w, r, "/user/recharge?err="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/pay/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

type invoiceRow struct {
	ID        int64
	No        string
	Amount    string
	Kind      string
	Status    string
	CreatedAt string
}

func (h *Pages) userInvoices(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	rows, err := h.Svc.DB.QueryContext(r.Context(),
		`SELECT id,no,amount,kind,status,to_char(created_at,'YYYY-MM-DD HH24:MI') FROM invoices WHERE user_id=$1 ORDER BY id DESC LIMIT 100`,
		userID)
	if err != nil {
		http.Error(w, "查询失败", 500)
		return
	}
	defer rows.Close()
	var list []invoiceRow
	for rows.Next() {
		var inv invoiceRow
		var status int16
		if err := rows.Scan(&inv.ID, &inv.No, &inv.Amount, &inv.Kind, &status, &inv.CreatedAt); err != nil {
			continue
		}
		switch status {
		case 0:
			inv.Status = "未支付"
		case 1:
			inv.Status = "已支付"
		default:
			inv.Status = "作废"
		}
		list = append(list, inv)
	}
	if err := rows.Err(); err != nil {
		http.Error(w, "查询失败", 500)
		return
	}
	render(w, r, "user_invoices.html", map[string]any{"Invoices": list})
}

func (h *Pages) passwordForm(w http.ResponseWriter, r *http.Request) {
	if _, ok := middleware.RequireUser(w, r); !ok {
		return
	}
	render(w, r, "user_password.html", map[string]any{"CSRF": csrfOf(sessionsStore, w, r)})
}

func (h *Pages) passwordSubmit(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	oldPass := r.PostFormValue("old")
	newPass := r.PostFormValue("new")
	if len(newPass) < 8 {
		render(w, r, "user_password.html", map[string]any{"CSRF": csrfOf(sessionsStore, w, r), "Error": "新密码至少 8 位"})
		return
	}
	if err := h.UsersRepo.ChangePassword(r.Context(), userID, oldPass, newPass); err != nil {
		render(w, r, "user_password.html", map[string]any{"CSRF": csrfOf(sessionsStore, w, r), "Error": err.Error()})
		return
	}
	render(w, r, "user_password.html", map[string]any{"CSRF": csrfOf(sessionsStore, w, r), "OK": true})
}

// serviceRenew 用户续费自己的服务：生成续费账单并跳转支付页。
func (h *Pages) serviceRenew(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	serviceID, _ := strconv.ParseInt(r.PathValue("serviceID"), 10, 64)
	cycle := r.PostFormValue("cycle")
	_, invID, _, err := h.Orders.CreateRenewOrder(r.Context(), userID, serviceID, cycle)
	if err != nil {
		h.Svc.AppendLog(r.Context(), serviceID, userID, "续费下单", "失败："+err.Error())
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h.Svc.AppendLog(r.Context(), serviceID, userID, "续费下单", "周期 "+cycle)
	http.Redirect(w, r, "/pay/"+strconv.FormatInt(invID, 10), http.StatusSeeOther)
}

// serviceCancel 用户申请删除服务（直接终止；有上游绑定则同步销毁）。
func (h *Pages) serviceCancel(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	serviceID, _ := strconv.ParseInt(r.PathValue("serviceID"), 10, 64)
	// 归属校验
	var n int
	h.Svc.DB.QueryRowContext(r.Context(),
		`SELECT count(*) FROM services WHERE id=$1 AND user_id=$2`, serviceID, userID).Scan(&n)
	if n == 0 {
		http.NotFound(w, r)
		return
	}
	lc := &service.Lifecycle{DB: h.Svc.DB, Servers: h.ServersRepo, Products: h.Products}
	if err := lc.Terminate(r.Context(), serviceID); err != nil {
		h.Svc.AppendLog(r.Context(), serviceID, userID, "删除服务", "失败："+err.Error())
		http.Error(w, "删除失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	h.Svc.AppendLog(r.Context(), serviceID, userID, "删除服务", "成功")
	http.Redirect(w, r, "/services", http.StatusSeeOther)
}

// serviceChart GET /services/{id}/chart?type=cpu&range=24h — 监控图表时序（JSON）。
func (h *Pages) serviceChart(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("serviceID"), 10, 64)
	typ := r.URL.Query().Get("type")
	if typ == "" {
		typ = "cpu"
	}
	sel := r.URL.Query().Get("range")
	series, err := h.Console.Chart(r.Context(), userID, id, typ, sel)
	if err != nil {
		writeJSON(w, map[string]any{"ok": 0, "msg": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"ok": 1, "series": series})
}

// serviceUsage GET /services/{id}/usage — 流量用量（JSON）。
func (h *Pages) serviceUsage(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("serviceID"), 10, 64)
	u, err := h.Console.Usage(r.Context(), userID, id)
	if err != nil {
		writeJSON(w, map[string]any{"ok": 0, "msg": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"ok": 1, "usage": u})
}

// servicePower GET /services/{id}/power — 实时电源状态（JSON）。
func (h *Pages) servicePower(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("serviceID"), 10, 64)
	ps, err := h.Console.PowerStatus(r.Context(), userID, id)
	if err != nil {
		writeJSON(w, map[string]any{"ok": 0, "msg": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"ok": 1, "power": ps})
}

// serviceTraffic GET /services/{id}/traffic — 每日流量曲线（JSON）。
func (h *Pages) serviceTraffic(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("serviceID"), 10, 64)
	days, err := h.Console.TrafficUsage(r.Context(), userID, id)
	if err != nil {
		writeJSON(w, map[string]any{"ok": 0, "msg": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"ok": 1, "days": days})
}

// serviceSnapshot GET /services/{id}/snapshot — 快照/备份概况（JSON）。
func (h *Pages) serviceSnapshot(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("serviceID"), 10, 64)
	info, err := h.Console.SnapshotInfo(r.Context(), userID, id)
	if err != nil {
		writeJSON(w, map[string]any{"ok": 0, "msg": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"ok": 1, "info": info})
}

// serviceSnapshotAction POST /services/{id}/snapshot/{fn} — 快照/备份操作。
func (h *Pages) serviceSnapshotAction(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("serviceID"), 10, 64)
	fn := r.PathValue("fn")
	if err := r.ParseForm(); err != nil {
		writeJSON(w, map[string]any{"ok": 0, "msg": "表单解析失败"})
		return
	}
	raw, err := h.Console.SnapshotAction(r.Context(), userID, id, fn, r.PostForm)
	if err != nil {
		writeJSON(w, map[string]any{"ok": 0, "msg": err.Error()})
		return
	}
	writeJSON(w, moduleResultJSON(raw))
}

// serviceBlocks GET /services/{id}/blocks — 一次拉取该实例全部方块数据（NAT/建站/安全组/设置）。
func (h *Pages) serviceBlocks(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("serviceID"), 10, 64)
	cctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	// 方块集合因产品而异：按模块清单 Areas 里有哪些 key 拉哪些。
	sum, err := h.Console.ModuleSummary(cctx, userID, id)
	if err != nil {
		writeJSON(w, map[string]any{"ok": 0, "msg": err.Error()})
		return
	}
	has := map[string]bool{}
	for _, a := range sum.Areas {
		has[a.Key] = true
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	blocks := map[string]any{}
	errs := map[string]string{}

	add := func(key string, fn func() (any, error)) {
		if !has[key] {
			return
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := fn()
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs[key] = err.Error()
				return
			}
			blocks[key] = v
		}()
	}
	add("nat_acl", func() (any, error) { return h.Console.NatList(cctx, userID, id) })
	add("nat_web", func() (any, error) { return h.Console.NatWebList(cctx, userID, id) })
	add("security_groups", func() (any, error) { return h.Console.SecurityGroups(cctx, userID, id) })
	add("setting", func() (any, error) { return h.Console.SettingData(cctx, userID, id) })
	wg.Wait()

	if len(errs) > 0 {
		writeJSON(w, map[string]any{"ok": 0, "msg": "部分方块拉取失败", "errors": errs})
		return
	}
	writeJSON(w, map[string]any{"ok": 1, "blocks": blocks})
}

// serviceBlockAction POST /services/{id}/block/{fn} — 方块操作（NAT/建站/安全组/设置）。
func (h *Pages) serviceBlockAction(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("serviceID"), 10, 64)
	fn := r.PathValue("fn")
	if err := r.ParseForm(); err != nil {
		writeJSON(w, map[string]any{"ok": 0, "msg": "表单解析失败"})
		return
	}
	// showSecurityRules 走 BlockAction 白名单之外，单独放行（只读）。
	raw, err := h.Console.BlockAction(r.Context(), userID, id, fn, r.PostForm)
	if err != nil {
		writeJSON(w, map[string]any{"ok": 0, "msg": err.Error()})
		return
	}
	writeJSON(w, moduleResultJSON(raw))
}

// serviceBlockRules GET /services/{id}/block-rules?gid= — 某安全组规则列表。
func (h *Pages) serviceBlockRules(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("serviceID"), 10, 64)
	gid, _ := strconv.ParseInt(r.URL.Query().Get("gid"), 10, 64)
	rules, err := h.Console.SecurityRules(r.Context(), userID, id, gid)
	if err != nil {
		writeJSON(w, map[string]any{"ok": 0, "msg": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"ok": 1, "rules": rules})
}

// serviceDetail GET /services/{id} — 用户服务详情页。
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
	var showQ, showY bool
	if pr, perr := h.Products.Price(r.Context(), d.ProductID, psID); perr == nil {
		showQ = priceVal(pr.Quarterly) > 0
		showY = priceVal(pr.Yearly) > 0
	}
	flash := ""
	if sess := middleware.FromSession(r.Context()); sess != nil {
		flash = sess.ConsumeFlash()
	}
	csrf := csrfOf(sessionsStore, w, r)
	overview := h.fetchOverview(r.Context(), userID, d.ID)
	// 供应商专属详情区块（插槽注入）；无该能力的供应商为空，回落全局面板。
	wctx, wcancel := context.WithTimeout(r.Context(), 8*time.Second)
	var providerWidget template.HTML
	if h.Console != nil {
		if wg, werr := h.Console.ProviderWidget(wctx, userID, d.ID, csrf, d.StatusText, overview); werr == nil {
			providerWidget = wg
		}
	}
	wcancel()
	render(w, r, "service_detail.html", map[string]any{
		"Svc":   d,
		"CSRF":  csrf,
		"ShowQ": showQ, "ShowY": showY,
		"Flash":          flash,
		"Overview":       overview,
		"ProviderWidget": providerWidget,
	})
}

// fetchOverview 详情页一次拉取上游概况（登录/系统信息 + 模块清单）。
// /host/header 仅请求一次即同时得到两类数据，避免重复请求；失败返回零值，不阻塞页面渲染。
func (h *Pages) fetchOverview(ctx context.Context, userID, serviceID int64) server.HostOverview {
	cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	ov, err := h.Console.Overview(cctx, userID, serviceID)
	if err != nil {
		log.Printf("[detail] service %d 拉取上游概况失败: %v", serviceID, err)
		return server.HostOverview{}
	}
	return ov
}

// consoleAction POST /services/{id}/console — 电源/重装/改密/VNC 统一入口。
func (h *Pages) consoleAction(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	serviceID, _ := strconv.ParseInt(r.PathValue("serviceID"), 10, 64)
	action := r.PostFormValue("do")
	if action == "" {
		action = r.URL.Query().Get("do") // VNC 用 GET 链接
	}
	if r.Method == http.MethodGet && action != "vnc" {
		http.Error(w, "该操作必须使用 POST", http.StatusMethodNotAllowed)
		return
	}

	// VNC 是 GET 语义，改为本站服务端反向代理：页面内嵌 noVNC、静态资源与 wss 隧道均走本站，隐藏上游域名。
	if action == "vnc" {
		h.vncConsole(w, r, userID, serviceID)
		return
	}

	var err error
	var appliedPw string
	switch action {
	case "on", "off", "reboot", "hard_off", "hard_reboot":
		err = h.Console.Power(r.Context(), userID, serviceID, action)
	case "crack_pass":
		appliedPw, err = h.Console.ResetPassword(r.Context(), userID, serviceID, r.PostFormValue("password"))
	case "reinstall":
		err = h.Console.Reinstall(r.Context(), userID, serviceID, r.PostFormValue("os"))
	case "rescue":
		appliedPw, err = h.Console.RescueWithPass(r.Context(), userID, serviceID,
			r.PostFormValue("system"), r.PostFormValue("temp_pass"))
	case "exit_rescue":
		err = h.Console.ExitRescue(r.Context(), userID, serviceID)
	default:
		http.Redirect(w, r, "/services/"+strconv.FormatInt(serviceID, 10), http.StatusSeeOther)
		return
	}
	dest := "/services/" + strconv.FormatInt(serviceID, 10)
	if err != nil {
		dest += "?err=" + url.QueryEscape(err.Error())
		h.Svc.AppendLog(r.Context(), serviceID, userID, opLabel(action), "失败："+err.Error())
	} else if action == "crack_pass" || action == "rescue" {
		// 新密码敏感，走一次性 flash 而非 URL 回显
		if sess := middleware.FromSession(r.Context()); sess != nil {
			if action == "rescue" {
				sess.SetFlash("救援系统已启动（实例将重启挂载救援系统）。临时密码：" + appliedPw)
			} else {
				sess.SetFlash("密码已重置，新密码：" + appliedPw)
			}
		}
		h.Svc.AppendLog(r.Context(), serviceID, userID, opLabel(action), "成功")
	} else {
		dest += "?ok=" + url.QueryEscape(actionName(action))
		h.Svc.AppendLog(r.Context(), serviceID, userID, opLabel(action), "成功")
	}
	http.Redirect(w, r, dest, http.StatusSeeOther)
}

// reinstallOptions GET — 返回可用 OS 列表 JSON。
func (h *Pages) reinstallOptions(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("serviceID"), 10, 64)
	opts, err := h.Console.OSOptions(r.Context(), userID, id)
	if err != nil {
		writeJSON(w, map[string]any{"ok": 0, "msg": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"ok": 1, "os": opts})
}

// serviceRescueState GET /services/{id}/rescue-state — 救援模式状态（JSON）。
func (h *Pages) serviceRescueState(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("serviceID"), 10, 64)
	on, err := h.Console.RescueState(r.Context(), userID, id)
	if err != nil {
		writeJSON(w, map[string]any{"ok": 0, "msg": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"ok": 1, "rescue": on})
}

func actionName(a string) string {
	if n := opLabel(a); n != a {
		return n + "成功"
	}
	return a
}

// opLabel 操作的友好名称（操作日志与提示共用）。
func opLabel(a string) string {
	names := map[string]string{
		"on": "开机", "off": "关机", "reboot": "重启",
		"hard_off": "硬关机", "hard_reboot": "强制重启",
		"crack_pass": "重置密码", "reinstall": "重装系统",
		"rescue": "救援模式", "exit_rescue": "退出救援",
		"renew": "续费下单", "cancel": "删除服务",
	}
	if n, ok := names[a]; ok {
		return n
	}
	return a
}

// htmlAttrEscape 转义放入 HTML 属性值的字符串（&<>"' → 实体）。
func htmlAttrEscape(s string) string { return html.EscapeString(s) }

// ---------- VNC 服务端反向代理（隐藏上游域名） ----------

// vncConsole GET /services/{id}/console?do=vnc — 渲染本站 noVNC 页面。
// noVNC 库从本站静态资源代理加载，wss 走本站隧道，浏览器看不到任何上游地址。
// VNC 连接密码不在页面渲染时注入（会话 token 与密码绑定，拨号重试后会变），
// 改由 RFB credentialsrequired 时实时从 /vnc-pass 取当前会话密码；
// 实例登录密码稳定，注入页面供「粘贴密码」一键输入（对齐 ZJMF-CBAP）。
func (h *Pages) vncConsole(w http.ResponseWriter, r *http.Request, userID, serviceID int64) {
	if _, err := h.Console.VNCInfo(r.Context(), userID, serviceID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	pwd := ""
	if d, derr := h.Console.HostDetail(r.Context(), userID, serviceID); derr == nil {
		pwd = d.Password
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html><html lang="zh"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>VNC 控制台</title>
<style>body{margin:0;font-family:system-ui,-apple-system,sans-serif;background:#111;color:#eee;height:100vh;display:flex;flex-direction:column}
#top{display:flex;align-items:center;gap:10px;padding:8px 14px;background:#1f2937;border-bottom:1px solid #374151;flex-wrap:wrap}
#top a{color:#818cf8;text-decoration:none;font-size:.85rem}
#status{font-size:.8rem;color:#9ca3af;flex:1}
#top button{background:#374151;color:#eee;border:1px solid #4b5563;border-radius:6px;padding:4px 12px;cursor:pointer;font-size:.8rem}
#screen{flex:1;overflow:hidden;background:#000}</style>
</head><body>
<div id="top"><a href="/services/%d">← 返回实例</a><strong>VNC 控制台</strong><span id="status">连接中…</span>
<button onclick="location.reload()">重新连接</button>
<button onclick="sendCAD()">Ctrl+Alt+Del</button>
<button onclick="pasteText()">粘贴文本</button>
<button onclick="pastePwd()">粘贴密码</button>
<button onclick="pastePwdRetry()">反转重试</button></div>
<div id="screen"></div>
<script type="module">
import RFB from '/services/%d/vnc-assets/vendor/noVNC/core/rfb.js';
const screen=document.getElementById('screen'),statusEl=document.getElementById('status');
let rfb=null;
function status(t){statusEl.textContent=t;}
try{
  const wsProto=location.protocol==='https:'?'wss://':'ws://';
  rfb=new RFB(screen,wsProto+location.host+'/services/%d/vnc-ws');
  rfb.scaleViewport=true;
  rfb.addEventListener('connect',()=>status('已连接'));
  rfb.addEventListener('disconnect',e=>status(e.detail&&e.detail.clean?'连接已断开':'连接异常断开'));
  rfb.addEventListener('credentialsrequired',async()=>{
    status('获取会话密码…');
    try{
      const r=await fetch('/services/%d/vnc-pass');
      const j=await r.json();
      if(j.ok===1&&j.password){rfb.sendCredentials({password:j.password});status('认证中…');}
      else{status('获取密码失败');}
    }catch(e){status('获取密码失败');}
  });
  rfb.addEventListener('desktopname',e=>status(e.detail.name));
}catch(err){status('连接失败: '+err.message);}
window.sendCAD=function(){if(rfb)rfb.sendCtrlAltDel();};
// 逐键发送文本到控制台（QEMU 扩展键事件，物理键级输出）
let invertCase=false; // 目标机 CapsLock 状态未知：粘贴密码时若大小写反了点「反转重试」
window.pasteText=function(){
  const t=prompt("输入要发送到控制台的文本（不会发送回车键）");
  if(t&&rfb){rfb.focus();sendKeys(rfb,t);}
};
window.pastePwd=function(retry){
  const p=%s;
  if(!p){alert("未获取到实例密码");return;}
  if(!rfb)return;
  rfb.focus();
  const text=invertCase?swapCase(p):p;
  sendKeys(rfb,text);
  if(!retry){
    alert("密码已发送。若目标机显示的大小写相反（CapsLock 影响），请在目标机输入框全选删除后点「反转重试」。");
  }
};
window.pastePwdRetry=function(){
  invertCase=!invertCase;
  window.pastePwd(true);
};
function swapCase(s){return s.split('').map(c=>c>='a'&&c<='z'?c.toUpperCase():(c>='A'&&c<='Z'?c.toLowerCase():c)).join('');}
function sendKeys(rfb,t){
  // 键名映射：字符 -> [XT scancode 键名, 该键的基键 keysym, 是否需要 Shift]
  // 全部走 QEMU 扩展键事件（scancode+keysym），物理键级输出，不受目标机 CapsLock 状态影响。
  const KEYMAP={
    a:['KeyA',0x61],b:['KeyB',0x62],c:['KeyC',0x63],d:['KeyD',0x64],e:['KeyE',0x65],
    f:['KeyF',0x66],g:['KeyG',0x67],h:['KeyH',0x68],i:['KeyI',0x69],j:['KeyJ',0x6a],
    k:['KeyK',0x6b],l:['KeyL',0x6c],m:['KeyM',0x6d],n:['KeyN',0x6e],o:['KeyO',0x6f],
    p:['KeyP',0x70],q:['KeyQ',0x71],r:['KeyR',0x72],s:['KeyS',0x73],t:['KeyT',0x74],
    u:['KeyU',0x75],v:['KeyV',0x76],w:['KeyW',0x77],x:['KeyX',0x78],y:['KeyY',0x79],z:['KeyZ',0x7a],
    '1':['Digit1',0x31],'2':['Digit2',0x32],'3':['Digit3',0x33],'4':['Digit4',0x34],
    '5':['Digit5',0x35],'6':['Digit6',0x36],'7':['Digit7',0x37],'8':['Digit8',0x38],
    '9':['Digit9',0x39],'0':['Digit0',0x30],
    '!':['Digit1',0x21,1],'@':['Digit2',0x40,1],'#':['Digit3',0x23,1],'$':['Digit4',0x24,1],
    '%%':['Digit5',0x25,1],'^':['Digit6',0x5e,1],'&':['Digit7',0x26,1],'*':['Digit8',0x2a,1],
    '(':['Digit9',0x28,1],')':['Digit0',0x29,1],
    '-':['Minus',0x2d],'_':['Minus',0x5f,1],'=':['Equal',0x3d],'+':['Equal',0x2b,1],
    '[':['BracketLeft',0x5b],'{':['BracketLeft',0x7b,1],
    ']':['BracketRight',0x5d],'}':['BracketRight',0x7d,1],
    '\\\\':['Backslash',0x5c],'|':['Backslash',0x7c,1],
    ';':['Semicolon',0x3b],':':['Semicolon',0x3a,1],
    "'":['Quote',0x27],'"':['Quote',0x22,1],
    [String.fromCharCode(96)]:['Backquote',0x60],'~':['Backquote',0x7e,1],
    '.':['Period',0x2e],'>':['Period',0x3e,1],
    '/':['Slash',0x2f],'?':['Slash',0x3f,1]
  };
  const SHIFT=0xffe1;
  for(const ch of t){
    const lower=ch.toLowerCase();
    let entry=KEYMAP[lower];
    if(!entry){continue;}
    let [name,base,shift]=entry;
    let keysym=base;
    if(ch>='A'&&ch<='Z'){
      keysym=ch.charCodeAt();shift=1;
    } else if(KEYMAP[ch]&&KEYMAP[ch][2]){
      // 本身就是 shift 字符（如 '!'），直接用该条目
      [name,keysym,shift]=KEYMAP[ch];
    }
    if(shift){rfb.sendKey(SHIFT,'ShiftLeft',1);}
    rfb.sendKey(keysym,name,1);
    rfb.sendKey(keysym,name,0);
    if(shift){rfb.sendKey(SHIFT,'ShiftLeft',0);}
  }
}
</script>
</body></html>`, serviceID, serviceID, serviceID, serviceID, jsString(pwd))
}

// serviceVncPass GET /services/{id}/vnc-pass — 当前 VNC 会话密码（JSON，登录用户自己的服务）。
func (h *Pages) serviceVncPass(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("serviceID"), 10, 64)
	info, err := h.Console.VNCInfo(r.Context(), userID, id)
	if err != nil {
		writeJSON(w, map[string]any{"ok": 0, "msg": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"ok": 1, "password": info.Password})
}

// vncAssets GET /services/{id}/vnc-assets/{path...} — 代理上游 noVNC 静态资源。
// 静态资源来自 VNC 页面源站（AssetOrigin），非 API 源站，故通过 VNCInfo 获取。
// 文本类资源把上游源站改写为本站前缀，杜绝残留上游域名。
func (h *Pages) vncAssets(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	serviceID, _ := strconv.ParseInt(r.PathValue("serviceID"), 10, 64)
	info, err := h.Console.VNCInfo(r.Context(), userID, serviceID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	origin := info.AssetOrigin
	assetPath := r.PathValue("path")
	if assetPath == "" || strings.Contains(assetPath, "..") {
		http.NotFound(w, r)
		return
	}
	// 纵深防御：VNCInfo 返回的源站同样受 SSRF 白名单约束，仅放行已登记主机。
	if h.ServersRepo == nil || !h.proxyOriginAllowed(r.Context(), origin) {
		http.NotFound(w, r)
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, origin+"/"+assetPath, nil)
	if err != nil {
		http.Error(w, "请求失败", 500)
		return
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Referer", origin+"/dcim/novnc")
	resp, err := proxyClient.Do(req)
	if err != nil {
		http.Error(w, "资源代理失败", 502)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		http.Error(w, "资源不存在", http.StatusNotFound)
		return
	}
	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	if !isTextContent(ct) {
		io.Copy(w, resp.Body)
		return
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		io.Copy(w, resp.Body)
		return
	}
	prefix := "/services/" + strconv.FormatInt(serviceID, 10) + "/vnc-assets"
	w.Write([]byte(strings.ReplaceAll(string(b), origin, prefix)))
}

// vncWebSocket GET /services/{id}/vnc-ws — 浏览器 wss 隧道，转发到上游真实 wss。
// 上游 wss 为自签证书（控制面同信任域）故跳过校验；地址与令牌仅存于服务端。
func (h *Pages) vncWebSocket(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	serviceID, _ := strconv.ParseInt(r.PathValue("serviceID"), 10, 64)
	info, err := h.Console.VNCInfo(r.Context(), userID, serviceID)
	if err != nil {
		log.Printf("[vnc] svc=%d VNCInfo 失败: %v", serviceID, err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// 透传浏览器请求的子协议（noVNC 要求 binary；上游不认子协议，拨上游时不带）
	up := websocket.Upgrader{
		// CSWSH 防护：仅允许同源（Origin 主机 == 请求 Host）建立隧道，阻断跨站脚本驱动 VNC。
		CheckOrigin: func(req *http.Request) bool {
			origin := req.Header.Get("Origin")
			if origin == "" {
				return true // 同源直连（无 Origin 头）放行
			}
			ou, err := url.Parse(origin)
			if err != nil {
				return false
			}
			return strings.EqualFold(ou.Host, req.Host)
		},
		Subprotocols: websocket.Subprotocols(r),
	}
	client, err := up.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[vnc] svc=%d 浏览器升级失败: %v", serviceID, err)
		return
	}
	defer client.Close()
	d := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	d.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // ponytail: 同上游控制面信任域；如需严格校验可改为受信 CA
	header := http.Header{}
	if info.AssetOrigin != "" {
		header.Set("Origin", info.AssetOrigin)
	}
	upstream, _, err := d.DialContext(r.Context(), info.WebSocketURL, header)
	if err != nil {
		// 上游 wss 偶发 bad handshake（token 会话互斥/瞬断）：清缓存重新拿会话重拨一次。
		log.Printf("[vnc] svc=%d 上游拨号失败(%v)，刷新会话重试", serviceID, err)
		h.Console.InvalidateVNC(r.Context(), userID, serviceID)
		if info2, err2 := h.Console.VNCInfo(r.Context(), userID, serviceID); err2 == nil {
			info = info2
			upstream, _, err = d.DialContext(r.Context(), info.WebSocketURL, header)
		}
	}
	if err != nil {
		log.Printf("[vnc] svc=%d 上游拨号失败: %v (ws=%s)", serviceID, err, info.WebSocketURL)
		client.Close()
		return
	}
	log.Printf("[vnc] svc=%d 上游拨号成功，隧道建立", serviceID)
	defer upstream.Close()
	defer client.Close()

	// 空闲超时：任一方向 2 分钟无数据即判定死连接，双向断开释放上游会话
	// （浏览器异常关闭可能是半开 TCP，ReadMessage 不会立刻感知，上游 VNC 连接会被占住）。
	const idleTimeout = 2 * time.Minute
	var lastActive int64 = time.Now().Unix()
	atomic.StoreInt64(&lastActive, time.Now().Unix())
	touch := func() { atomic.StoreInt64(&lastActive, time.Now().Unix()) }

	// 保活：每 30s 向浏览器发 ping（客户端 pong 会刷新读活跃；上游断开也能借此检测）
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	go func() {
		for range ticker.C {
			if time.Now().Unix()-atomic.LoadInt64(&lastActive) > int64(idleTimeout.Seconds()) {
				log.Printf("[vnc] svc=%d 空闲超时，主动断开隧道", serviceID)
				client.Close()
				upstream.Close()
				return
			}
			_ = client.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second))
		}
	}()

	var nUp, nDown int
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			_ = upstream.SetReadDeadline(time.Now().Add(idleTimeout))
			mt, data, err := upstream.ReadMessage()
			if err != nil {
				log.Printf("[vnc] svc=%d 上游读取结束(下发%d条): %v", serviceID, nUp, err)
				client.Close()
				return
			}
			touch()
			if err := client.WriteMessage(mt, data); err != nil {
				log.Printf("[vnc] svc=%d 写浏览器失败(下发%d条): %v", serviceID, nUp, err)
				return
			}
			nUp++
		}
	}()
	_ = client.SetReadDeadline(time.Now().Add(idleTimeout))
	client.SetPongHandler(func(string) error {
		touch()
		_ = client.SetReadDeadline(time.Now().Add(idleTimeout))
		return nil
	})
	for {
		mt, data, err := client.ReadMessage()
		if err != nil {
			log.Printf("[vnc] svc=%d 浏览器读取结束(上行%d条): %v", serviceID, nDown, err)
			// 浏览器已断开：立即关上游，让阻塞中的上游 ReadMessage 立刻返回释放连接
			// （否则上游空闲时 goroutine 会一直阻塞到读超时，期间上游 VNC 会话被占用）。
			upstream.Close()
			break
		}
		touch()
		_ = client.SetReadDeadline(time.Now().Add(idleTimeout))
		if err := upstream.WriteMessage(mt, data); err != nil {
			log.Printf("[vnc] svc=%d 写上游失败(上行%d条): %v", serviceID, nDown, err)
			break
		}
		nDown++
	}
	<-done
	log.Printf("[vnc] svc=%d 隧道关闭（上行%d条 下发%d条）", serviceID, nDown, nUp)
}

// isTextContent 判断资源是否可按文本改写（仅文本类做源站替换，避免破坏二进制）。
func isTextContent(ct string) bool {
	ct = strings.ToLower(ct)
	if strings.HasPrefix(ct, "text/") {
		return true
	}
	return strings.Contains(ct, "javascript") || strings.Contains(ct, "json") ||
		strings.Contains(ct, "xml") || strings.Contains(ct, "svg")
}

// jsString 生成安全的单引号 JS 字符串字面量（含 </ 防护）。
func jsString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, "</", `<\/`)
	return "'" + s + "'"
}

// ---------- 产品模块（魔方云 service module 通用能力：快照/安全组/NAT/共享建站等） ----------

// moduleBlock 一个上游客户端方块（key + 展示名 + 已改写的页面内容）。
type moduleBlock struct {
	Key     string
	Name    string
	Content string
}

// moduleAssetRE 匹配上游面板自身绝对资源地址（/vendor/ 前缀），据此隐藏上游域名。
var moduleAssetRE = regexp.MustCompile(`(https?://[A-Za-z0-9._\-:]+)/vendor/`)

// moduleAssetPrefix 该服务内联资源代理前缀，host64 为 base64(origin) 站点专用码。
func moduleAssetPrefix(serviceID int64, origin string) string {
	token := base64.RawURLEncoding.EncodeToString([]byte(origin))
	return "/services/" + strconv.FormatInt(serviceID, 10) + "/module-assets/" + token
}

// proxyClient 资源代理专用客户端，固定超时避免慢上游耗尽 goroutine（DoS）。
var proxyClient = &http.Client{Timeout: 15 * time.Second}

// privateHostname 命中内网/保留地址段，禁止代理，避免 SSRF 打元数据或内网。
func isPrivateHostname(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	if h == "localhost" || h == "0.0.0.0" || h == "::1" || h == "[::1]" {
		return true
	}
	if strings.HasPrefix(h, "127.") || strings.HasPrefix(h, "10.") ||
		strings.HasPrefix(h, "192.168.") || strings.HasPrefix(h, "169.254.") {
		return true
	}
	if strings.HasPrefix(h, "172.") {
		// 172.16.0.0/12
		parts := strings.Split(h, ".")
		if len(parts) == 4 {
			if n, err := strconv.Atoi(parts[1]); err == nil && n >= 16 && n <= 31 {
				return true
			}
		}
	}
	return false
}

// proxyOriginAllowed 仅允许代理到管理员在 servers 表中配置的上下游面板主机。
// 即便客户端传入任意 origin，也必须命中已登记主机才放行，杜绝 SSRF 到任意公网/内网。
func (h *Pages) proxyOriginAllowed(ctx context.Context, origin string) bool {
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	host := u.Hostname()
	if host == "" || isPrivateHostname(host) {
		return false
	}
	ok, err := h.ServersRepo.IsAllowedProxyHost(ctx, host)
	if err != nil {
		return false
	}
	return ok
}

// rewriteModuleAssets 把方块内容里指向上游面板的资源地址改写为本站代理前缀，杜绝上游域名泄露。
// 以首个 /vendor/ 绝对地址的 origin 为准，仅改写该源；其余外部资源原样保留。
func rewriteModuleAssets(serviceID int64, content string) string {
	m := moduleAssetRE.FindStringSubmatch(content)
	if len(m) < 2 {
		return content
	}
	origin := m[1]
	prefix := moduleAssetPrefix(serviceID, origin)
	content = strings.ReplaceAll(content, origin+"/", prefix+"/")
	return strings.ReplaceAll(content, origin, prefix)
}

// serviceModuleOverview GET /services/{id}/module — 一次内嵌全部方块为标签页概览。
// 服务端循环拉取各 client_area 方块内容并汇总，用户无需逐个点击；表单统一改走本站 POST。
func (h *Pages) serviceModuleOverview(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	serviceID, _ := strconv.ParseInt(r.PathValue("serviceID"), 10, 64)
	sum, err := h.Console.ModuleSummary(r.Context(), userID, serviceID)
	if err != nil {
		http.Error(w, "模块清单拉取失败: "+err.Error(), http.StatusBadGateway)
		return
	}
	var blocks []moduleBlock
	for _, a := range sum.Areas {
		content, err := h.Console.ModulePageContent(r.Context(), userID, serviceID, a.Key)
		if err != nil {
			log.Printf("[module] service %d 拉取方块 %s 失败: %v", serviceID, a.Key, err)
			continue
		}
		blocks = append(blocks, moduleBlock{Key: a.Key, Name: a.Name, Content: rewriteModuleAssets(serviceID, content)})
	}
	if len(blocks) == 0 {
		http.Error(w, "该产品暂无可用功能模块", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	fmt.Fprint(w, moduleOverviewShell(serviceID, csrfOf(sessionsStore, w, r), blocks))
}

// serviceModulePage GET /services/{id}/module/{key} — 单个方块本地代理页（跳转直开时用）。
func (h *Pages) serviceModulePage(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	serviceID, _ := strconv.ParseInt(r.PathValue("serviceID"), 10, 64)
	key := r.PathValue("key")
	content, err := h.Console.ModulePageContent(r.Context(), userID, serviceID, key)
	if err != nil {
		http.Error(w, "模块页面拉取失败: "+err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	fmt.Fprint(w, moduleOverviewShell(serviceID, csrfOf(sessionsStore, w, r), []moduleBlock{
		{Key: key, Content: rewriteModuleAssets(serviceID, content)},
	}))
}

// moduleOverviewShell 组装概览页：标签导航 + 各方块内容 + 提交拦截脚本（本地 POST + CSRF）。
// 上游方块（快照/安全组/设置等）依赖宿主页的 jQuery/Bootstrap/SweetAlert2 与自定义 ajax()，
// 此处统一补齐：公共库走 CDN，ajax() 拦截改写上游绝对地址为本站模块端点，避免跨域与无凭据。
// ponytail: 多方块直接堆叠 DOM，若上游方块间存在同名 id/全局变量可能互相干扰；必要时改 iframe 隔离。
func moduleOverviewShell(serviceID int64, csrf string, blocks []moduleBlock) string {
	var sb strings.Builder
	sid := strconv.FormatInt(serviceID, 10)
	sb.WriteString("<!doctype html><html><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width,initial-scale=1\">")
	sb.WriteString(`<link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/bootstrap@4.6.2/dist/css/bootstrap.min.css">`)
	sb.WriteString(`<script src="https://cdn.jsdelivr.net/npm/jquery@3.6.4/dist/jquery.min.js"></script>`)
	sb.WriteString(`<script src="https://cdn.jsdelivr.net/npm/bootstrap@4.6.2/dist/js/bootstrap.bundle.min.js"></script>`)
	sb.WriteString(`<script src="https://cdn.jsdelivr.net/npm/sweetalert2@11"></script>`)
	sb.WriteString("<style>body{margin:0;font-family:system-ui,-apple-system,'Segoe UI',Roboto,sans-serif;color:#1a1a1a;background:#fff}#tabs{position:sticky;top:0;background:#fff;border-bottom:1px solid #e2e8f0;padding:8px 12px;display:flex;gap:6px;flex-wrap:wrap;z-index:1050}#tabs .tab{cursor:pointer;padding:6px 14px;border:1px solid #cbd5e1;border-radius:999px;background:#fff;color:#334155;font:inherit}#tabs .tab.on{background:#0e7490;color:#fff;border-color:#0e7490}.block{display:none;padding:16px}.block.on{display:block}button,input,select,textarea{font:inherit}.modal{z-index:2000}body.swal2-shown>. swal2-container{z-index:2100!important}</style></head><body>")
	if len(blocks) > 1 {
		sb.WriteString("<div id=\"tabs\">")
		for i, b := range blocks {
			name := b.Name
			if name == "" {
				name = b.Key
			}
			cls := ""
			if i == 0 {
				cls = " on"
			}
			fmt.Fprintf(&sb, "<button class=\"tab%s\" onclick=\"showTab(%d)\">%s</button>", cls, i, html.EscapeString(name))
		}
		sb.WriteString("</div>")
	}
	for i, b := range blocks {
		cls := ""
		if i == 0 {
			cls = " on"
		}
		fmt.Fprintf(&sb, "<section class=\"block%s\" data-key=%s>", cls, strconv.Quote(b.Key))
		sb.WriteString(b.Content)
		sb.WriteString("</section>")
	}
	fmt.Fprintf(&sb,
		`<script>(function(){var sid=%s,csrf=%s;
function showTab(n){document.querySelectorAll('.block').forEach(function(s,i){s.classList.toggle('on',i===n);});document.querySelectorAll('#tabs .tab').forEach(function(b,i){b.classList.toggle('on',i===n);});}window.showTab=showTab;
// SweetAlert2 v11 用 icon；上游方块 JS 传 type，做兼容映射
if(window.Swal){var _fire=Swal.fire.bind(Swal);Swal.fire=function(o){if(o&&o.type&&!o.icon){o.icon=o.type;}return _fire(o);};}
// ajax() 拦截：把上游绝对地址（…/provision/custom/<id>）改写为本站模块端点（ModuleAction 不区分 key），
// POST 表单与 JSON 两种 data 形态都支持，success 回调收到的保持上游 {status,msg} 形态。
window.ajax=function(opts){
  var url=opts.url||'';
  url=url.replace(/^https?:\/\/[^\/]+\/provision\/custom\/\d+.*/,'/services/'+sid+'/module/upstream');
  var body='';
  if(opts.data){
    if(typeof opts.data==='string'){body=opts.data;}
    else{var p=new URLSearchParams();Object.keys(opts.data).forEach(function(k){p.append(k,opts.data[k]);});body=p.toString();}
  }
  body+=(body?'&':'')+'_csrf='+encodeURIComponent(csrf);
  var xhr=new XMLHttpRequest();
  xhr.open((opts.type||'POST').toUpperCase(),url,true);
  xhr.setRequestHeader('Content-Type','application/x-www-form-urlencoded');
  xhr.onreadystatechange=function(){
    if(xhr.readyState!==4){return;}
    var data=null;
    try{data=JSON.parse(xhr.responseText);}catch(e){data={ok:0,msg:xhr.responseText||('http '+xhr.status)};}
    if(xhr.status>=400&&opts.error){opts.error(xhr);}else if(opts.success){opts.success(data);}
  };
  xhr.send(body);
};
document.querySelectorAll('form').forEach(function(f){var sec=f.closest('section');var key=sec?sec.getAttribute('data-key'):'';f.addEventListener('submit',function(ev){ev.preventDefault();var fd=new FormData(f);fd.set('_csrf',csrf);fetch('/services/'+sid+'/module/'+encodeURIComponent(key),{method:'POST',body:fd}).then(function(r){return r.json();}).then(function(j){if(j.ok){alert(j.msg||'操作成功');location.reload();}else{alert(j.msg||'操作失败');}}).catch(function(e){alert('网络错误:'+e);});});});})();</script>`,
		sid, jsString(csrf))
	sb.WriteString("</body></html>")
	return sb.String()
}

// serviceModuleAssets GET /services/{id}/module-assets/{host64}/{path...} — 代理上游面板静态资源。
// token 为 base64(origin)，文本类资源把原始 origin 改写回本站前缀，避免后续请求再泄露上游域名。
func (h *Pages) serviceModuleAssets(w http.ResponseWriter, r *http.Request) {
	_, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	serviceID, _ := strconv.ParseInt(r.PathValue("serviceID"), 10, 64)
	token := r.PathValue("host64")
	assetPath := r.PathValue("path")
	if token == "" || assetPath == "" || strings.Contains(assetPath, "..") {
		http.NotFound(w, r)
		return
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || !strings.HasPrefix(string(raw), "http://") && !strings.HasPrefix(string(raw), "https://") {
		http.NotFound(w, r)
		return
	}
	origin := string(raw)
	// SSRF 防护：origin 必须命中 servers 表中登记的上下游主机，禁止代理到任意地址。
	if h.ServersRepo == nil || !h.proxyOriginAllowed(r.Context(), origin) {
		http.NotFound(w, r)
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, origin+"/"+assetPath, nil)
	if err != nil {
		http.Error(w, "请求失败", 500)
		return
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := proxyClient.Do(req)
	if err != nil {
		http.Error(w, "资源代理失败", 502)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		http.Error(w, "资源不存在", http.StatusNotFound)
		return
	}
	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	if !isTextContent(ct) {
		io.Copy(w, resp.Body)
		return
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		io.Copy(w, resp.Body)
		return
	}
	prefix := moduleAssetPrefix(serviceID, origin)
	w.Write([]byte(strings.ReplaceAll(string(b), origin, prefix)))
}

// serviceModuleSubmit POST /services/{id}/module/{key} — 提交方块表单到上游，返回 JSON 结果。
func (h *Pages) serviceModuleSubmit(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	serviceID, _ := strconv.ParseInt(r.PathValue("serviceID"), 10, 64)
	if err := r.ParseForm(); err != nil {
		writeJSON(w, map[string]any{"ok": 0, "msg": "表单解析失败"})
		return
	}
	raw, err := h.Console.ModuleAction(r.Context(), userID, serviceID, r.PostForm)
	if err != nil {
		writeJSON(w, map[string]any{"ok": 0, "msg": err.Error()})
		return
	}
	writeJSON(w, moduleResultJSON(raw))
}

// moduleResultJSON 包装上游模块响应：透传上游 status/msg（方块内嵌 JS 依赖 data.status==200），
// 同时附加本地 ok/msg（供 shell 的表单拦截与 ajax 包装统一判断）。
func moduleResultJSON(raw string) map[string]any {
	out := map[string]any{"ok": 1, "msg": strings.TrimSpace(raw)}
	var m map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &m); err == nil {
		for k, v := range m {
			out[k] = v
		}
		ok := true
		if st, has := m["status"]; has {
			if v, isF := st.(float64); isF && v >= 400 {
				ok = false
			}
		}
		if s, has := m["msg"].(string); has {
			out["msg"] = s
		} else if s, has := m["message"].(string); has {
			out["msg"] = s
		}
		out["ok"] = ok
	}
	return out
}
