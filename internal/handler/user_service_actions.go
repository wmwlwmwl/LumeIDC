package handler

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"lumeidc/internal/middleware"
	"lumeidc/internal/service"
)

func (h *Pages) serviceRenew(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	serviceID, _ := strconv.ParseInt(r.PathValue("serviceID"), 10, 64)
	cycle := r.PostFormValue("cycle")
	_, invID, _, err := h.Orders.CreateRenewOrder(r.Context(), userID, serviceID, cycle)
	if err != nil {
		if errors.Is(err, service.ErrIdentityRequired) {
			http.Redirect(w, r, "/user/verification?err="+url.QueryEscape(err.Error()), http.StatusSeeOther)
			return
		}
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
	owned, err := h.Svc.Owns(r.Context(), serviceID, userID)
	if err != nil || !owned {
		http.NotFound(w, r)
		return
	}
	if err := h.Lifecycle.Terminate(r.Context(), serviceID); err != nil {
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
	if err := r.ParseForm(); err != nil {
		jsonFail(w, "表单解析失败")
		return
	}
	raw, err := h.Console.SnapshotAction(r.Context(), userID, id, fn, r.PostForm)
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
	if err := r.ParseForm(); err != nil {
		jsonFail(w, "表单解析失败")
		return
	}
	// showSecurityRules 走 BlockAction 白名单之外，单独放行（只读）。
	raw, err := h.Console.BlockAction(r.Context(), userID, id, fn, r.PostForm)
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

// serviceDetail GET /services/{id} — 用户服务详情页。

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
