package handler

import (
	"log"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"time"

	"lumeidc/internal/middleware"
	"lumeidc/internal/update"
)

// adminUpdatePage GET /admin/update — 系统更新页。
func (a *Admin) adminUpdatePage(w http.ResponseWriter, r *http.Request) {
	if !adminRequire(w, r) { // 未登录 303 跳后台登录页（与其他后台页一致，不裸显 401 文本）
		return
	}
	data := AdminData{CSRF: a.adminCSRF(w, r), UpdateDisabled: runtime.GOOS != "linux"}
	if a.Updater != nil {
		data.UpdateInfo = &update.Info{CurrentVersion: a.Updater.Version}
		data.UpdatePending = a.Updater.HasPendingRestart()
	}
	a.renderAdmin(w, "admin_update.html", data)
}

// updateRequireAdmin 更新相关 JSON 接口的会话校验：失效时返回 JSON 错误，
// 便于 fetch 前端提示，而不是 401 文本/303 重定向到登录页 HTML。
func (a *Admin) updateRequireAdmin(w http.ResponseWriter, r *http.Request) (*middleware.Session, bool) {
	sess := middleware.FromSession(r.Context())
	if sess == nil || !sess.IsAdmin {
		jsonFail(w, "需要管理员登录，请刷新页面重新登录后再操作")
		return nil, false
	}
	return sess, true
}

// adminUpdateCheck POST /admin/update/check — 查询最新版本，不触发下载。
func (a *Admin) adminUpdateCheck(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.updateRequireAdmin(w, r); !ok {
		return
	}
	if a.Updater == nil {
		jsonFail(w, "更新服务未初始化")
		return
	}
	if runtime.GOOS != "linux" {
		jsonFail(w, "系统更新仅支持 Linux 服务器")
		return
	}
	info, err := a.Updater.Check(r.Context())
	if err != nil {
		jsonFail(w, err.Error())
		return
	}
	jsonOK(w, "info", info)
}

// adminUpdateApply POST /admin/update/apply — 下载校验并替换二进制，成功后提示手动重启。
func (a *Admin) adminUpdateApply(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.updateRequireAdmin(w, r)
	if !ok {
		return
	}
	if a.Updater == nil {
		jsonFail(w, "更新服务未初始化")
		return
	}
	if runtime.GOOS != "linux" {
		jsonFail(w, "系统更新仅支持 Linux 服务器")
		return
	}
	ver := a.Updater.Version
	if err := a.Updater.Apply(r.Context()); err != nil {
		if a.AdminLog != nil {
			a.AdminLog.Record(sess.UserID, "system_update", "system", 0, "更新失败 v"+ver+"："+err.Error(), requestIP(r))
		}
		jsonFail(w, "更新失败："+err.Error())
		return
	}
	if a.AdminLog != nil {
		a.AdminLog.Record(sess.UserID, "system_update", "system", 0, "已替换二进制 v"+ver+"→最新版，待重启", requestIP(r))
	}
	jsonOK(w, "msg", "更新成功，已替换二进制。请手动重启应用生效（1Panel: docker restart 容器名；宝塔: supervisorctl restart lumeidc）")
}

// adminUpdateRestart POST /admin/update/restart — 下载替换完成后就地重启进程。
// 先回写响应并 flush，再短暂等待让浏览器收到提示，最后 execve 切到新二进制。
// 重启后内存会话失效，需重新登录。
func (a *Admin) adminUpdateRestart(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.updateRequireAdmin(w, r)
	if !ok {
		return
	}
	if a.Updater == nil {
		jsonFail(w, "更新服务未初始化")
		return
	}
	if runtime.GOOS != "linux" {
		jsonFail(w, "系统更新仅支持 Linux 服务器")
		return
	}
	if a.AdminLog != nil {
		a.AdminLog.Record(sess.UserID, "system_update", "system", 0, "就地重启生效新版本", requestIP(r))
	}
	// 显式 Content-Length + Flush：exec 替换进程后剩余响应不会送达，
	// 若走 chunked（无长度头），终止块发不出去会导致浏览器收到 HTTP 200 但 body 不完整、JSON 解析失败。
	body := []byte(`{"ok":1,"msg":"正在重启，请稍后重新打开后台"}`)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	if _, err := w.Write(body); err == nil {
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
	// 给响应一个到达浏览器的窗口，然后进程替换；exec 成功则此处不可达。
	time.Sleep(800 * time.Millisecond)
	if err := a.Updater.ExecNew(); err != nil {
		log.Printf("[update] 就地重启失败: %v", err)
		jsonFail(w, "就地重启失败："+err.Error())
	}
	os.Exit(1) // ExecNew 返回 nil 时按 Exec 语义不会走到；此处兜底防死循环
}
