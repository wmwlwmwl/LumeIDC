package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"lumeidc/internal/middleware"
	"lumeidc/internal/repo"
	"lumeidc/internal/service"
)

// moduleResultJSON 把上游模块操作响应归一为前端 ActionResult。
// JSON 响应以 status 判定成败（≥400 视为失败）；非 JSON 正文保守视为失败。
// ponytail: 上游操作类接口正常返回 JSON 包装，纯文本/HTML 通常是错误页；
// 若未来出现以纯文本返回成功的模块 func，需为其显式加白名单而非放开默认值。
func moduleResultJSON(raw string) map[string]any {
	out := map[string]any{"ok": 0}
	trimmed := strings.TrimSpace(raw)
	var m map[string]any
	if err := json.Unmarshal([]byte(trimmed), &m); err != nil || m == nil {
		// 非 JSON：截断后作为错误信息透出，避免整段 HTML 刷屏
		msg := trimmed
		if len(msg) > 120 {
			msg = msg[:120]
		}
		out["msg"] = msg
		return out
	}
	for k, v := range m {
		out[k] = v
	}
	ok := true
	// 与 doRaw 的 checkBizAbnormal 同口径：status 为数值且不在成功集合（2xx/1000/1001）才判失败，
	// 无 status 或非数值 status 的标准包装视为成功。
	if st, has := m["status"]; has {
		if v, isF := st.(float64); isF && !((v >= 200 && v < 300) || v == 1000 || v == 1001) {
			ok = false
		}
	}
	if s, has := m["msg"].(string); has {
		out["msg"] = s
	} else if s, has := m["message"].(string); has {
		out["msg"] = s
	}
	out["ok"] = ok
	return out
}

func (h *Pages) serviceRenew(w http.ResponseWriter, r *http.Request) {
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
	cycle := fv("cycle")
	_, invID, amount, err := h.Orders.CreateRenewOrder(r.Context(), userID, serviceID, cycle)
	if err != nil {
		if errors.Is(err, service.ErrIdentityRequired) {
			if wantsJSON(r) {
				writeJSON(w, map[string]any{"ok": 0, "code": "identity_required", "msg": err.Error(), "redirect": "/user/verification"})
				return
			}
			http.Redirect(w, r, "/user/verification?err="+url.QueryEscape(err.Error()), http.StatusSeeOther)
			return
		}
		h.Svc.AppendLog(r.Context(), serviceID, userID, "续费下单", "失败："+err.Error())
		jsonStatus(w, r, http.StatusBadRequest, err.Error())
		return
	}
	h.Svc.AppendLog(r.Context(), serviceID, userID, "续费下单", "周期 "+cycle)
	// 0 元续费（免费商品）：创建即自动核销、跳过支付页——余额与网关都处理不了 0 金额。
	if amount == "0.00" {
		if h.Payment == nil {
			jsonStatus(w, r, http.StatusInternalServerError, "支付服务未配置")
			return
		}
		no, qerr := h.Invoices.NoByID(r.Context(), invID)
		if qerr != nil {
			log.Printf("[0元续费] 读取账单号失败 invoice=%d: %v", invID, qerr)
			jsonStatus(w, r, http.StatusInternalServerError, "免费续费失败，请联系管理员")
			return
		}
		if perr := h.Payment.MarkPaid(r.Context(), no, "FREERENEW-"+strconv.FormatInt(invID, 10), "balance"); perr != nil {
			log.Printf("[0元续费] 账单 %d 自动核销失败: %v", invID, perr)
			jsonStatus(w, r, http.StatusInternalServerError, "免费续费失败，请联系管理员")
			return
		}
		dest := "/services/" + strconv.FormatInt(serviceID, 10)
		if wantsJSON(r) {
			writeJSON(w, map[string]any{"ok": 1, "paid": true, "msg": "续费成功", "redirect": dest})
			return
		}
		http.Redirect(w, r, dest, http.StatusSeeOther)
		return
	}
	if wantsJSON(r) {
		writeJSON(w, map[string]any{"ok": 1, "invoice_id": invID, "redirect": "/pay/" + strconv.FormatInt(invID, 10)})
		return
	}
	http.Redirect(w, r, "/pay/"+strconv.FormatInt(invID, 10), http.StatusSeeOther)
}

// cancelReasonOptions 停用申请原因选项（对齐魔方财务 cancel_reason 默认项）。
var cancelReasonOptions = []string{"产品稳定性不足", "业务减少不需要", "其他"}

// serviceCancelRequestInfo GET /services/{id}/cancel-request — 当前停用申请与原因选项。
func (h *Pages) serviceCancelRequestInfo(w http.ResponseWriter, r *http.Request) {
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
	reasons := cancelReasonOptions
	req := any(nil)
	if h.CancelReqs != nil {
		if cur, cerr := h.CancelReqs.ActiveByService(r.Context(), serviceID); cerr == nil && cur != nil {
			req = map[string]any{
				"id": cur.ID, "type": cur.Type, "reason": cur.Reason,
				"reason_detail": cur.ReasonDetail, "created_at": cur.CreatedAt.Format("2006-01-02 15:04"),
			}
		}
	}
	writeJSON(w, map[string]any{"ok": 1, "reasons": reasons, "request": req})
}

// serviceCancelRequestSubmit POST /services/{id}/cancel-request — 提交停用申请。
// 服务实例保持不变，仅生成待处理申请，由后台审核。
func (h *Pages) serviceCancelRequestSubmit(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	if h.CancelReqs == nil {
		jsonStatus(w, r, http.StatusServiceUnavailable, "停用申请服务未启用")
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
	typ := strings.TrimSpace(fv("type"))
	if typ != "Immediate" && typ != "Endofbilling" {
		jsonStatus(w, r, http.StatusBadRequest, "停用类型无效")
		return
	}
	reason := strings.TrimSpace(fv("reason"))
	if reason == "" {
		jsonStatus(w, r, http.StatusBadRequest, "请选择停用原因")
		return
	}
	detail := strings.TrimSpace(fv("reason_detail"))
	if len([]rune(detail)) > 500 {
		jsonStatus(w, r, http.StatusBadRequest, "详细说明过长")
		return
	}
	// 状态校验：仅激活/已停机可申请（对齐魔方财务 Active/Suspended）。
	var status int16
	if err := h.DB.QueryRowContext(r.Context(),
		`SELECT status FROM services WHERE id=$1`, serviceID).Scan(&status); err != nil {
		jsonStatus(w, r, http.StatusBadRequest, "服务不存在")
		return
	}
	if status != 1 && status != 2 {
		jsonStatus(w, r, http.StatusBadRequest, "仅激活或已停机的服务可申请停用")
		return
	}
	id, cerr := h.CancelReqs.Create(r.Context(), serviceID, userID, typ, reason, detail)
	if cerr != nil {
		if errors.Is(cerr, repo.ErrCancelRequestExists) {
			jsonStatus(w, r, http.StatusConflict, "该服务已存在待处理的停用申请")
			return
		}
		h.Svc.AppendLog(r.Context(), serviceID, userID, "申请停用", "失败："+cerr.Error())
		jsonStatus(w, r, http.StatusInternalServerError, "提交失败，请稍后重试")
		return
	}
	typeText := "立即停用"
	if typ == "Endofbilling" {
		typeText = "等待账单周期结束"
	}
	h.Svc.AppendLog(r.Context(), serviceID, userID, "申请停用", typeText+"："+reason)
	if h.Notifier != nil {
		h.Notifier.NotifyTemplate(r.Context(), userID, "cancel_submitted", "停用申请已提交",
			"你的服务停用申请已提交（"+typeText+"），我们将尽快处理。\n原因："+reason+
				func() string {
					if detail != "" {
						return "（" + detail + "）"
					}
					return ""
				}())
	}
	writeJSON(w, map[string]any{"ok": 1, "id": id})
}

// serviceCancelRequestWithdraw POST /services/{id}/cancel-request/withdraw — 撤回停用申请。
func (h *Pages) serviceCancelRequestWithdraw(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	if h.CancelReqs == nil {
		jsonStatus(w, r, http.StatusServiceUnavailable, "停用申请服务未启用")
		return
	}
	serviceID, _ := strconv.ParseInt(r.PathValue("serviceID"), 10, 64)
	owned, err := h.Svc.Owns(r.Context(), serviceID, userID)
	if err != nil || !owned {
		http.NotFound(w, r)
		return
	}
	cur, gerr := h.CancelReqs.ActiveByService(r.Context(), serviceID)
	if gerr != nil || cur == nil {
		jsonStatus(w, r, http.StatusNotFound, "没有待处理的停用申请")
		return
	}
	done, werr := h.CancelReqs.Withdraw(r.Context(), cur.ID, userID)
	if werr != nil || !done {
		jsonStatus(w, r, http.StatusInternalServerError, "撤回失败，请稍后重试")
		return
	}
	h.Svc.AppendLog(r.Context(), serviceID, userID, "撤回停用申请", "成功")
	writeJSON(w, map[string]any{"ok": 1})
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
		jsonFail(w, err.Error())
		return
	}
	jsonOK(w, "series", series)
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
		jsonFail(w, err.Error())
		return
	}
	jsonOK(w, "usage", u)
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
		jsonFail(w, err.Error())
		return
	}
	jsonOK(w, "power", ps)
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
		jsonFail(w, err.Error())
		return
	}
	jsonOK(w, "days", days)
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
		jsonFail(w, err.Error())
		return
	}
	jsonOK(w, "info", info)
}

// serviceSnapshotAction POST /services/{id}/snapshot/{fn} — 快照/备份操作。

func (h *Pages) serviceSnapshotAction(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("serviceID"), 10, 64)
	fn := r.PathValue("fn")
	// 同 block 操作：FormData（multipart）提交，ParseForm 解析不到
	if err := r.ParseMultipartForm(1 << 20); err != nil && !errors.Is(err, http.ErrNotMultipart) {
		jsonFail(w, "表单解析失败")
		return
	}
	raw, err := h.Console.SnapshotAction(r.Context(), userID, id, fn, r.Form)
	if err != nil {
		jsonFail(w, err.Error())
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
		jsonFail(w, err.Error())
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
	jsonOK(w, "blocks", blocks)
}

// serviceBlockAction POST /services/{id}/block/{fn} — 方块操作（NAT/建站/安全组/设置）。

func (h *Pages) serviceBlockAction(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("serviceID"), 10, 64)
	fn := r.PathValue("fn")
	// 前端以 FormData（multipart）提交；ParseForm 不解析 multipart，
	// 须用 ParseMultipartForm（内部会合并进 r.Form）。urlencoded 同样兼容。
	if err := r.ParseMultipartForm(1 << 20); err != nil && !errors.Is(err, http.ErrNotMultipart) {
		jsonFail(w, "表单解析失败")
		return
	}
	// showSecurityRules 走 BlockAction 白名单之外，单独放行（只读）。
	raw, err := h.Console.BlockAction(r.Context(), userID, id, fn, r.Form)
	if err != nil {
		jsonFail(w, err.Error())
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
		jsonFail(w, err.Error())
		return
	}
	jsonOK(w, "rules", rules)
}

// reinstallOptions GET /services/{id}/reinstall-options — 可重装的 OS 列表。
func (h *Pages) reinstallOptions(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("serviceID"), 10, 64)
	opts, err := h.Console.OSOptions(r.Context(), userID, id)
	if err != nil {
		jsonFail(w, err.Error())
		return
	}
	jsonOK(w, "os", opts)
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
		jsonFail(w, err.Error())
		return
	}
	jsonOK(w, "rescue", on)
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
