package handler

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"lumeidc/internal/middleware"
	"lumeidc/internal/repo"
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
	name := strings.TrimSpace(r.PostFormValue("name"))
	remark := strings.TrimSpace(r.PostFormValue("remark"))
	if name == "" {
		http.Error(w, "服务名称不能为空", http.StatusBadRequest)
		return
	}
	if len([]rune(name)) > 100 || len([]rune(remark)) > 255 {
		http.Error(w, "名称或备注过长", http.StatusBadRequest)
		return
	}
	if err := h.Svc.Rename(r.Context(), serviceID, name, remark); err != nil {
		http.Error(w, "保存失败，请稍后重试", http.StatusInternalServerError)
		return
	}
	h.Svc.AppendLog(r.Context(), serviceID, userID, "修改名称/备注", name)
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

	// 目标产品（?target=）配置表单数据（复用 buy.html 的 recalc JS），并带 current_monthly 供差价展示。
	var targetID int64
	var targetJSON template.JS
	var targetName, targetMonthlyStr string
	var targetOpts []repo.ConfigOption
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
				b, _ := json.Marshal(map[string]any{
					"base": baseMap, "options": opts,
					"profit_type": et, "profit_value": ev,
					"current_monthly": currentMonthly,
				})
				targetJSON = template.JS(b)
				targetName = tp.Name
				targetOpts = opts
				tm, _ := service.MonthlySellPrice(r.Context(), h.Products, targetID, map[string]string{})
				targetMonthlyStr = fmt.Sprintf("%.2f", tm)
			}
		}
	}
	h.render(w, r, "service_upgrade.html", map[string]any{
		"Svc": d, "CSRF": h.pageCSRF(w, r), "Error": r.URL.Query().Get("err"),
		"Targets": targets, "TargetID": targetID,
		"CurrentMonthly": fmt.Sprintf("%.2f", currentMonthly),
		"TargetName": targetName, "TargetMonthly": targetMonthlyStr,
		"TargetConfig": targetJSON, "TargetOptions": targetOpts,
		"ShowQ": showQ, "ShowY": showY,
	})
}

// serviceUpgradeOrder POST /services/{serviceID}/upgrade — 创建升降级订单。
// 升级（diff>0）跳支付；降级（diff<0）0 元单直接余额核销并退差价。
func (h *Pages) serviceUpgradeOrder(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	serviceID, _ := strconv.ParseInt(r.PathValue("serviceID"), 10, 64)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "表单解析失败", http.StatusBadRequest)
		return
	}
	targetID, _ := strconv.ParseInt(r.PostFormValue("target_product_id"), 10, 64)
	cycle := r.PostFormValue("cycle")
	if cycle == "" {
		cycle = "monthly"
	}
	selection := map[string]string{}
	for key, vals := range r.PostForm {
		if strings.HasPrefix(key, "cfg_") && len(vals) > 0 && vals[0] != "" {
			selection[strings.TrimPrefix(key, "cfg_")] = vals[0]
		}
	}
	_, invID, _, diff, err := h.Orders.CreateUpgradeOrder(r.Context(), userID, serviceID, targetID, cycle, selection)
	if err != nil {
		http.Redirect(w, r, "/services/"+strconv.FormatInt(serviceID, 10)+"/upgrade?err="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	if diff > 0 {
		http.Redirect(w, r, "/pay/"+strconv.FormatInt(invID, 10), http.StatusSeeOther)
		return
	}
	// 降级：0 元单直接余额核销（不可走 MarkPaidByBalance 的正数校验），差价已在核销事务内退回。
	no, qerr := h.Invoices.NoByID(r.Context(), invID)
	if qerr != nil {
		http.Error(w, "创建降级订单失败", http.StatusInternalServerError)
		return
	}
	if h.Payment == nil {
		http.Error(w, "支付服务未配置", http.StatusInternalServerError)
		return
	}
	if perr := h.Payment.MarkPaid(r.Context(), no, "DOWN-"+strconv.FormatInt(invID, 10), "balance"); perr != nil {
		http.Error(w, "降级处理失败: "+perr.Error(), http.StatusInternalServerError)
		return
	}
	if sess := middleware.FromSession(r.Context()); sess != nil {
		sess.SetFlash("降级成功，差价已退回余额")
	}
	http.Redirect(w, r, "/services/"+strconv.FormatInt(serviceID, 10), http.StatusSeeOther)
}
