package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"lumeidc/internal/middleware"
	"lumeidc/internal/repo"
	"lumeidc/internal/server"
	"lumeidc/internal/service"
)

type adminServiceRow struct {
	ID         int64
	A, B, C    string // 用户 / 产品 / 状态
	D          string // 到期时间
	Upstream   string // 上游 host id
	ProvErr    string // 开通/升级失败原因
	Transition string // 过渡状态（upgrading=升级处理中）
	Profit     string // 毛利（来自下单订单）
	Hostname   string
	ConfigDesc string
	Monthly    string
	DaysLeft   int
	UserID     int64
	RenewM     string // 固定续费价覆盖（空=跟随产品价）
	RenewQ     string
	RenewY     string
}

// ServicesList GET /admin/services — 全部服务列表（服务端筛选）。

func (m *AdminManage) ServicesList(w http.ResponseWriter, r *http.Request) {
	if !m.require(w, r) {
		return
	}
	f := service.AdminServiceFilter{Keyword: r.URL.Query().Get("q"), Status: -1}
	if pid, err := strconv.ParseInt(r.URL.Query().Get("product_id"), 10, 64); err == nil {
		f.ProductID = pid
	}
	if s := r.URL.Query().Get("status"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil && v >= 0 && v <= 2 {
			f.Status = int16(v)
		}
	}
	list, err := m.Svc.AdminList(r.Context(), f)
	if err != nil {
		log.Printf("[admin] 服务列表查询失败: %v", err)
		http.Error(w, "查询失败", 500)
		return
	}
	out := make([]adminServiceRow, 0, len(list))
	for _, svc := range list {
		row := adminServiceRow{ID: svc.ID, A: svc.User, B: svc.Name, C: svc.Status, D: svc.Expires,
			Upstream: svc.Upstream, ProvErr: svc.ProvErr, Transition: svc.Transition, Profit: svc.Profit, Hostname: svc.Hostname,
			UserID: svc.UserID, RenewM: svc.RenewM, RenewQ: svc.RenewQ, RenewY: svc.RenewY}
		// 配置摘要 + 月价：全部用主查询带回的数据在内存计算（不逐行查库；IP/系统留详情页）
		// 后台手工填写的配置说明优先（手工上架/迁移服务无法由产品配置项推导）
		row.ConfigDesc, row.Monthly = rowPricing(svc.ConfigSnap, svc.ConfigOpts, svc.MonthlyBase,
			svc.ProfitType, svc.ProfitValue, svc.ServerProfitType, svc.ServerProfitValue)
		if svc.ConfigNote != "" {
			row.ConfigDesc = svc.ConfigNote
		}
		row.DaysLeft = int(time.Until(svc.ExpiresAt).Hours() / 24)
		out = append(out, row)
	}
	products, _ := m.Products.ListAll(r.Context())
	if products == nil {
		products = []repo.Product{}
	}
	items := make([]map[string]any, 0, len(out))
	for _, svc := range out {
		items = append(items, map[string]any{
			"id": svc.ID, "user_id": svc.UserID, "user": svc.A, "product": svc.B, "status_text": svc.C, "expires": svc.D,
			"upstream": svc.Upstream, "prov_err": svc.ProvErr, "transition": svc.Transition, "profit": svc.Profit,
			// 上游改价导致的待处理：前端据此在点「重试」时弹二次确认（按新价继续会少赚）。
			"price_changed": strings.Contains(svc.ProvErr, server.PriceChangedPrefix),
			"hostname":      svc.Hostname, "config_desc": svc.ConfigDesc, "monthly": svc.Monthly,
			"days_left":     svc.DaysLeft,
			"renew_monthly": svc.RenewM, "renew_quarterly": svc.RenewQ, "renew_yearly": svc.RenewY,
		})
	}
	prods := make([]map[string]any, 0, len(products))
	for _, p := range products {
		prods = append(prods, map[string]any{"id": p.ID, "name": p.Name})
	}
	statusStr := ""
	if f.Status >= 0 {
		statusStr = strconv.FormatInt(int64(f.Status), 10)
	}
	writeJSON(w, map[string]any{
		"ok": 1, "list": items, "products": prods,
		"filter": map[string]any{"q": f.Keyword, "status": statusStr, "product_id": f.ProductID},
	})
}

// ServicesStatusJSON GET /admin/services/status — 后台轮询用：返回各服务状态与失败原因，
// 用于自动发现“上游已开通/开通失败”并刷新列表（ponytail: 仅读 DB，不重复调上游）。

func (m *AdminManage) ServicesStatusJSON(w http.ResponseWriter, r *http.Request) {
	if !m.require(w, r) {
		return
	}
	rows, err := m.Svc.AdminStatus(r.Context())
	if err != nil {
		http.Error(w, "查询失败", 500)
		return
	}
	type st struct {
		ID         int64  `json:"id"`
		C          string `json:"c"`
		ProvErr    string `json:"provErr"`
		Transition string `json:"transition"`
	}
	out := make([]st, 0, len(rows))
	for _, s := range rows {
		out = append(out, st{ID: s.ID, C: s.StatusLabel, ProvErr: s.ProvErr, Transition: s.Transition})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// ServiceAction POST /admin/services/{id}/action — 停机/解除/删除/重试开通。

func (m *AdminManage) ServiceAction(w http.ResponseWriter, r *http.Request) {
	if !m.require(w, r) {
		return
	}
	vals := jsonVals(r)
	if vals == nil {
		if !m.requireCSRF(w, r) {
			return
		}
	}
	fv := func(k string) string {
		if vals != nil {
			return vals[k]
		}
		return r.PostFormValue(k)
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	action := fv("do")
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	var err error
	errMsg := ""
	switch action {
	case "suspend":
		err = m.Lifecycle.Suspend(ctx, id)
	case "unsuspend":
		err = m.Lifecycle.Unsuspend(ctx, id)
	case "terminate":
		err = m.Lifecycle.Terminate(ctx, id)
	case "retry":
		err = m.Payment.RetryProvision(ctx, id)
	case "refund_pending":
		var aid int64
		if s := middleware.FromSession(r.Context()); s != nil {
			aid = s.UserID
		}
		err = m.Payment.RefundPendingService(ctx, aid, id, "价格变动，开通前退款")
	case "retry_upgrade":
		err = m.Payment.RetryUpgrade(ctx, id)
	case "refund_upgrade":
		var aid int64
		if s := middleware.FromSession(r.Context()); s != nil {
			aid = s.UserID
		}
		err = m.Payment.RefundUpgrade(ctx, aid, id, "价格变动，取消升级并退款")
	case "retry_renew":
		err = m.Payment.RetryRenew(ctx, id)
	case "refund_renew":
		var aid int64
		if s := middleware.FromSession(r.Context()); s != nil {
			aid = s.UserID
		}
		err = m.Payment.RefundRenew(ctx, aid, id, "价格变动，取消续费并退款")
	default:
		jsonStatus(w, r, http.StatusBadRequest, "未知操作")
		return
	}
	if err != nil {
		errMsg = err.Error()
		if wantsJSON(r) {
			writeJSON(w, map[string]any{"ok": 0, "msg": "操作失败：" + errMsg})
			return
		}
	}
	m.audit(r, "service_"+action, "service", id, errMsg)
	if wantsJSON(r) {
		writeJSON(w, map[string]any{"ok": 1})
		return
	}
	http.Redirect(w, r, "/admin/services?err="+url.QueryEscape(errMsg), http.StatusSeeOther)
}

// ServiceEdit POST /admin/services/{id}/edit — 编辑服务（对齐魔方财务服务编辑）：
// 换归属用户、到期时间、固定续费价、配置说明。未提交的字段=不修改；
// 续费价空串=清除覆盖（恢复跟随产品价）；配置说明空串=清除手工配置（恢复自动生成）。
func (m *AdminManage) ServiceEdit(w http.ResponseWriter, r *http.Request) {
	if !m.require(w, r) {
		return
	}
	vals := jsonVals(r)
	if vals == nil {
		if !m.requireCSRF(w, r) {
			return
		}
	}
	fv := func(k string) string {
		if vals != nil {
			return vals[k]
		}
		return r.PostFormValue(k)
	}
	// fvPresent 区分“未提交该字段”与“提交了空值”：JSON 请求按 key 是否存在判定，
	// 表单回退按非空判定（配置说明清空走 SPA JSON 请求）。
	fvPresent := func(k string) (string, bool) {
		if vals != nil {
			v, ok := vals[k]
			return v, ok
		}
		v := r.PostFormValue(k)
		return v, v != ""
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if id <= 0 {
		jsonStatus(w, r, http.StatusBadRequest, "服务不存在")
		return
	}
	// 换归属用户：目标用户必须存在且启用
	var userID *int64
	newEmail := ""
	if s := strings.TrimSpace(fv("user_id")); s != "" {
		uid, err := strconv.ParseInt(s, 10, 64)
		if err != nil || uid <= 0 {
			jsonStatus(w, r, http.StatusBadRequest, "用户 ID 无效")
			return
		}
		email, err := m.Users.EmailByID(r.Context(), uid)
		if err != nil {
			jsonStatus(w, r, http.StatusBadRequest, "目标用户不存在或已禁用")
			return
		}
		userID = &uid
		newEmail = email
	}
	// 到期时间：接受日期或日期时间（服务器本地时区）
	var expiresAt *time.Time
	if s := strings.TrimSpace(fv("expires_at")); s != "" {
		for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02"} {
			if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
				expiresAt = &t
				break
			}
		}
		if expiresAt == nil {
			jsonStatus(w, r, http.StatusBadRequest, "到期时间格式无效")
			return
		}
	}
	// 固定续费价：空=不改，数字=设置（0=清除覆盖），非法值直接拒绝
	var renews [3]*float64
	cycleLabels := []string{"月", "季", "年"}
	for i, k := range []string{"renew_monthly", "renew_quarterly", "renew_yearly"} {
		s := strings.TrimSpace(fv(k))
		if s == "" {
			continue
		}
		v, err := strconv.ParseFloat(s, 64)
		if err != nil || v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
			jsonStatus(w, r, http.StatusBadRequest, cycleLabels[i]+"续费价无效")
			return
		}
		renews[i] = &v
	}
	// 配置说明：直接写入数据库，为空表示清除手工配置（恢复按产品配置项自动生成）
	var configDesc *string
	if raw, ok := fvPresent("config_desc"); ok {
		s := strings.TrimSpace(raw)
		if len([]rune(s)) > 1000 {
			jsonStatus(w, r, http.StatusBadRequest, "配置说明过长（最多 1000 字）")
			return
		}
		configDesc = &s
	}
	if userID == nil && expiresAt == nil && renews[0] == nil && renews[1] == nil && renews[2] == nil && configDesc == nil {
		jsonStatus(w, r, http.StatusBadRequest, "没有要修改的内容")
		return
	}
	uid, err := m.Svc.AdminEdit(r.Context(), id, userID, expiresAt, renews[0], renews[1], renews[2], configDesc)
	if err != nil {
		jsonStatus(w, r, http.StatusInternalServerError, "保存失败："+err.Error())
		return
	}
	// 审计：后台审计日志 + 服务日志（服务日志跟随编辑后的归属用户，换绑后新用户可见）
	var desc []string
	if userID != nil {
		desc = append(desc, fmt.Sprintf("归属用户改为 #%d（%s）", *userID, newEmail))
	}
	if expiresAt != nil {
		desc = append(desc, "到期时间改为 "+fv("expires_at"))
	}
	for i := range renews {
		if renews[i] == nil {
			continue
		}
		if *renews[i] > 0 {
			desc = append(desc, fmt.Sprintf("固定%s续费价设为 %.2f", cycleLabels[i], *renews[i]))
		} else {
			desc = append(desc, fmt.Sprintf("清除固定%s续费价（恢复跟随产品价）", cycleLabels[i]))
		}
	}
	if configDesc != nil {
		if *configDesc == "" {
			desc = append(desc, "清除配置说明（恢复自动生成）")
		} else {
			desc = append(desc, "配置说明改为 "+*configDesc)
		}
	}
	m.Svc.AppendLog(r.Context(), id, uid, "后台编辑", strings.Join(desc, "；"))
	m.audit(r, "service_edit", "service", id, "")
	if wantsJSON(r) {
		writeJSON(w, map[string]any{"ok": 1})
		return
	}
	http.Redirect(w, r, "/admin/services", http.StatusSeeOther)
}

// ---------- 用户管理操作 ----------

// UserEdit GET /admin/users/{id}/edit — 用户管理表单（联系方式、余额/状态/重置密码）。
