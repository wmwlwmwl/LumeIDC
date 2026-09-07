package handler

import (
	"context"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"time"

	"lumeidc/internal/middleware"
	"lumeidc/internal/server"
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
	var showQ, showY bool
	if pr, perr := h.Products.Price(r.Context(), d.ProductID, psID); perr == nil {
		showQ = priceVal(pr.Quarterly) > 0
		showY = priceVal(pr.Yearly) > 0
	}
	flash := ""
	if sess := middleware.FromSession(r.Context()); sess != nil {
		flash = sess.ConsumeFlash()
	}
	csrf := h.pageCSRF(w, r)
	overview := h.fetchOverview(r.Context(), userID, d.ID)
	// 升降级入口：仅上游支持升降级（或有本地可升级目标）时显示；60s 缓存探测结果。
	var canUpgrade bool
	if h.Console != nil {
		canUpgrade = h.Console.CanUpgrade(r.Context(), userID, d.ID)
	}
	// 供应商专属详情区块（插槽注入）；无该能力的供应商为空，回落全局面板。
	wctx, wcancel := context.WithTimeout(r.Context(), 8*time.Second)
	var providerWidget template.HTML
	if h.Console != nil {
		if wg, werr := h.Console.ProviderWidget(wctx, userID, d.ID, csrf, d.StatusText, overview); werr == nil {
			providerWidget = wg
		}
	}
	wcancel()
	h.render(w, r, "service_detail.html", map[string]any{
		"Svc":   d,
		"CSRF":  csrf,
		"ShowQ": showQ, "ShowY": showY,
		"Flash":          flash,
		"Overview":       overview,
		"ProviderWidget": providerWidget,
		"CanUpgrade":     canUpgrade,
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
		if sess := middleware.FromSession(r.Context()); sess != nil {
			sess.SetFlash("操作失败：" + err.Error())
		}
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
		if sess := middleware.FromSession(r.Context()); sess != nil {
			sess.SetFlash(actionName(action))
		}
		h.Svc.AppendLog(r.Context(), serviceID, userID, opLabel(action), "成功")
	}
	http.Redirect(w, r, dest, http.StatusSeeOther)
}

// reinstallOptions GET — 返回可用 OS 列表 JSON。
