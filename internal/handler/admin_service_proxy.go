package handler

import (
	"net/http"
	"strconv"

	"lumeidc/internal/middleware"
)

// RegisterAdminServiceProxy 把用户侧的服务接口镜像到后台路径下：
//
//	/services/{serviceID}/...  →  /admin/services/{serviceID}/...
//
// 后台「服务实例 → 管理」需要与用户端服务详情页完全一致的能力。这里复用同一批 handler，
// 只把鉴权换成管理员会话，并把该服务的归属用户注入上下文（middleware.WithActAsUser），
// 用户侧 handler 因而照常工作，不再维护第二套实现。
//
// 前端同理复用 ServiceDetail 组件：后台 http 层会给相对路径自动加上 /admin 前缀。
func (h *Pages) RegisterAdminServiceProxy(mux *http.ServeMux) {
	for _, rt := range adminServiceRoutes(h) {
		mux.HandleFunc(rt.method+" /admin/services/{serviceID}"+rt.suffix, h.actAsServiceOwner(rt.h))
	}
}

type adminServiceRoute struct {
	method string
	suffix string
	h      http.HandlerFunc
}

// adminServiceRoutes 与 Pages.Register 里的 /services/{serviceID}/* 逐条对应，顺序也保持一致。
func adminServiceRoutes(h *Pages) []adminServiceRoute {
	return []adminServiceRoute{
		{"GET", "", h.serviceDetail},
		{"POST", "/refresh", h.serviceRefresh},
		{"GET", "/invoices", h.serviceInvoices},
		{"POST", "/renew", h.serviceRenew},
		{"POST", "/name", h.serviceRename},
		{"GET", "/upgrade", h.serviceUpgradeForm},
		{"POST", "/upgrade", h.serviceUpgradeOrder},
		{"GET", "/cancel-request", h.serviceCancelRequestInfo},
		{"POST", "/cancel-request", h.serviceCancelRequestSubmit},
		{"POST", "/cancel-request/withdraw", h.serviceCancelRequestWithdraw},
		{"POST", "/console", h.consoleAction},
		{"GET", "/vnc-ws", h.vncWebSocket},
		{"GET", "/vnc-pass", h.serviceVncPass},
		{"GET", "/chart", h.serviceChart},
		{"GET", "/usage", h.serviceUsage},
		{"GET", "/power", h.servicePower},
		{"GET", "/traffic", h.serviceTraffic},
		{"GET", "/snapshot", h.serviceSnapshot},
		{"POST", "/snapshot/{fn}", h.serviceSnapshotAction},
		{"GET", "/blocks", h.serviceBlocks},
		{"POST", "/block/{fn}", h.serviceBlockAction},
		{"GET", "/block-rules", h.serviceBlockRules},
		{"GET", "/rescue-state", h.serviceRescueState},
		{"GET", "/reinstall-options", h.reinstallOptions},
	}
}

// actAsServiceOwner 后台代管包装器：先校验管理员会话，再查出服务归属用户并注入代管上下文。
// 服务不存在时按 404 返回，避免管理员误操作不存在的实例。
func (h *Pages) actAsServiceOwner(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !adminRequire(w, r) {
			return
		}
		id, err := strconv.ParseInt(r.PathValue("serviceID"), 10, 64)
		if err != nil || id <= 0 {
			jsonStatus(w, r, http.StatusBadRequest, "服务 ID 无效")
			return
		}
		var uid int64
		if err := h.DB.QueryRowContext(r.Context(), `SELECT user_id FROM services WHERE id=$1`, id).Scan(&uid); err != nil {
			jsonStatus(w, r, http.StatusNotFound, "服务不存在")
			return
		}
		next(w, r.WithContext(middleware.WithActAsUser(r.Context(), uid)))
	}
}
