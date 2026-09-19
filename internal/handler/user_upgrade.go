package handler

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"lumeidc/internal/middleware"
	"lumeidc/internal/service"
)

// serviceRename POST /services/{serviceID}/name — 用户修改服务名称与备注。
func (h *Pages) serviceRename(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	serviceID, _ := strconv.ParseInt(r.PathValue("serviceID"), 10, 64)
	owned, err := h.Svc.Owns(r.Context(), serviceID, userID)
	if err != nil || !owned {
		http.NotFound(w, r)
		return
	}
	vals := jsonVals(r)
	fv := func(k string) string {
		if vals != nil {
			return vals[k]
		}
		return r.PostFormValue(k)
	}
	name := strings.TrimSpace(fv("name"))
	remark := strings.TrimSpace(fv("remark"))
	if name == "" {
		jsonStatus(w, r, http.StatusBadRequest, "服务名称不能为空")
		return
	}
	if len([]rune(name)) > 100 || len([]rune(remark)) > 255 {
		jsonStatus(w, r, http.StatusBadRequest, "名称或备注过长")
		return
	}
	if err := h.Svc.Rename(r.Context(), serviceID, name, remark); err != nil {
		jsonStatus(w, r, http.StatusInternalServerError, "保存失败，请稍后重试")
		return
	}
	h.Svc.AppendLog(r.Context(), serviceID, userID, "修改名称/备注", name)
	if wantsJSON(r) {
		writeJSON(w, map[string]any{"ok": 1})
		return
	}
	http.Redirect(w, r, "/services/"+strconv.FormatInt(serviceID, 10), http.StatusSeeOther)
}

// serviceUpgradeForm GET /services/{serviceID}/upgrade — 服务升降级页（当前套餐 + 目标产品 + 配置）。
func (h *Pages) serviceUpgradeForm(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	serviceID, _ := strconv.ParseInt(r.PathValue("serviceID"), 10, 64)
	d, err := h.Svc.GetDetail(r.Context(), serviceID, userID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if d.Status != 1 && d.Status != 2 {
		http.Error(w, "服务当前状态不可升降级", http.StatusBadRequest)
		return
	}
	curSel := h.Svc.ConfigSelection(r.Context(), serviceID)
	currentMonthly, _ := service.MonthlySellPrice(r.Context(), h.Products, d.ProductID, curSel)
	targets := h.Console.UpgradeTargets(r.Context(), userID, serviceID)

	// 目标产品（?target=）配置表单数据：查询结果供 out["target"] 使用，不做第二套重复查询。
	var targetID int64
	var targetData map[string]any
	var showQ, showY bool
	targetID, _ = strconv.ParseInt(r.URL.Query().Get("target"), 10, 64)
	if targetID > 0 {
		tp, gerr := h.Products.Get(r.Context(), targetID)
		if gerr == nil && !tp.Hidden {
			psID, _ := h.Products.DefaultPricesetID(r.Context())
			pr, perr := h.Products.Price(r.Context(), targetID, psID)
			opts, _ := h.Products.GetConfigOptions(r.Context(), targetID)
			if perr == nil {
				showQ = priceVal(pr.Quarterly) > 0
				showY = priceVal(pr.Yearly) > 0
				baseMap := map[string]float64{}
				for c, k := range map[string]string{"monthly": pr.Monthly, "quarterly": pr.Quarterly, "yearly": pr.Yearly} {
					if v, e := strconv.ParseFloat(k, 64); e == nil {
						baseMap[c] = v
					}
				}
				et := productProfitType(r.Context(), h.Products, tp.ProfitType, tp.ProfitValue, tp.ID)
				ev := productProfitValue(r.Context(), h.Products, tp.ProfitType, tp.ProfitValue, tp.ID)
				tm, _ := service.MonthlySellPrice(r.Context(), h.Products, targetID, map[string]string{})
				targetData = map[string]any{
					"id": targetID, "name": tp.Name, "monthly": fmt.Sprintf("%.2f", tm),
					"base": baseMap, "options": opts,
					"profit_type": et, "profit_value": ev,
				}
			}
		}
	}
	out := map[string]any{
		"ok": 1,
		"svc": map[string]any{
			"id": d.ID, "name": d.Name, "status": d.Status, "status_text": map[int16]string{0: "待开通", 1: "激活", 2: "已停机"}[d.Status],
		},
		"current_monthly": fmt.Sprintf("%.2f", currentMonthly),
		"targets":         targets,
	}
	if targetData != nil {
		out["target"] = targetData
		out["show_q"] = showQ
		out["show_y"] = showY
	}
	writeJSON(w, out)
}

// serviceUpgradeOrder POST /services/{serviceID}/upgrade — 创建升降级订单。
// 升级（diff>0）跳支付；降级（diff<0）0 元单直接核销，差价不退还（仅记 orders.diff_amount 供审计）。
func (h *Pages) serviceUpgradeOrder(w http.ResponseWriter, r *http.Request) {
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
	if vals == nil {
		if err := r.ParseForm(); err != nil {
			jsonStatus(w, r, http.StatusBadRequest, "表单解析失败")
			return
		}
	}
	targetID, _ := strconv.ParseInt(fv("target_product_id"), 10, 64)
	cycle := fv("cycle")
	if cycle == "" {
		cycle = "monthly"
	}
	selection := map[string]string{}
	if vals != nil {
		for k, v := range vals {
			if strings.HasPrefix(k, "cfg_") && v != "" {
				selection[strings.TrimPrefix(k, "cfg_")] = v
			}
		}
	} else {
		for key, vals2 := range r.PostForm {
			if strings.HasPrefix(key, "cfg_") && len(vals2) > 0 && vals2[0] != "" {
				selection[strings.TrimPrefix(key, "cfg_")] = vals2[0]
			}
		}
	}
	_, invID, _, diff, err := h.Orders.CreateUpgradeOrder(r.Context(), userID, serviceID, targetID, cycle, selection)
	if err != nil {
		// 上游价格已变（本地已同步）：前端据此重载升级页，让用户按新价重新确认。
		// 不重载会出现"页面显示旧差价、实际按新差价下单"的静默改价。
		// 用 400 而非 200：前端 http 层只在非 2xx 时才抛错，200+ok:0 会被当成成功。
		if errors.Is(err, service.ErrUpstreamPriceChanged) && wantsJSON(r) {
			w.WriteHeader(http.StatusBadRequest)
			writeJSON(w, map[string]any{"ok": 0, "code": "price_changed", "msg": err.Error()})
			return
		}
		msg := consoleErrMsg(err)
		if wantsJSON(r) {
			writeJSON(w, map[string]any{"ok": 0, "msg": msg})
			return
		}
		http.Redirect(w, r, "/services/"+strconv.FormatInt(serviceID, 10)+"/upgrade?err="+url.QueryEscape(msg), http.StatusSeeOther)
		return
	}
	if diff > 0 {
		if wantsJSON(r) {
			writeJSON(w, map[string]any{"ok": 1, "diff_up": true, "invoice_id": invID, "redirect": "/pay/" + strconv.FormatInt(invID, 10)})
			return
		}
		http.Redirect(w, r, "/pay/"+strconv.FormatInt(invID, 10), http.StatusSeeOther)
		return
	}
	// 降级：0 元单直接核销（不可走 MarkPaidByBalance 的正数校验）；差价不再退到余额，
	// 与魔方系默认一致——降级只降配置，不返还差额。
	no, qerr := h.Invoices.NoByID(r.Context(), invID)
	if qerr != nil {
		jsonStatus(w, r, http.StatusInternalServerError, "创建降级订单失败")
		return
	}
	if h.Payment == nil {
		jsonStatus(w, r, http.StatusInternalServerError, "支付服务未配置")
		return
	}
	if perr := h.Payment.MarkPaid(r.Context(), no, "DOWN-"+strconv.FormatInt(invID, 10), "balance"); perr != nil {
		jsonStatus(w, r, http.StatusInternalServerError, "降级处理失败: "+perr.Error())
		return
	}
	if wantsJSON(r) {
		writeJSON(w, map[string]any{"ok": 1, "diff_up": false, "msg": "降级成功，差价不予退还", "redirect": "/services/" + strconv.FormatInt(serviceID, 10)})
		return
	}
	if sess := middleware.FromSession(r.Context()); sess != nil {
		sess.SetFlash("降级成功，差价不予退还")
	}
	http.Redirect(w, r, "/services/"+strconv.FormatInt(serviceID, 10), http.StatusSeeOther)
}
