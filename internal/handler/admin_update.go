package handler

import (
	"log"
	"net/http"
	"os"
	"runtime"
	"time"

	"lumeidc/internal/middleware"
	"lumeidc/internal/update"
)

// adminUpdatePage GET /admin/update — 系统更新页。
func (a *Admin) adminUpdatePage(w http.ResponseWriter, r *http.Request) {
	if _, ok := middleware.RequireAdmin(w, r); !ok {
		return
	}
	data := AdminData{CSRF: a.adminCSRF(w, r), UpdateDisabled: runtime.GOOS != "linux"}
	if a.Updater != nil {
		data.UpdateInfo = &update.Info{CurrentVersion: a.Updater.Version}
	}
	a.renderAdmin(w, "admin_update.html", data)
}

// adminUpdateCheck POST /admin/update/check — 查询最新版本，不触发下载。
func (a *Admin) adminUpdateCheck(w http.ResponseWriter, r *http.Request) {
	if _, ok := middleware.RequireAdmin(w, r); !ok {
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
	sess, ok := middleware.RequireAdmin(w, r)
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
	sess, ok := middleware.RequireAdmin(w, r)
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
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Write([]byte(`{"ok":1,"msg":"正在重启，请稍后重新打开后台"}`))
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	// 给响应一个到达浏览器的窗口，然后进程替换；exec 成功则此处不可达。
	time.Sleep(800 * time.Millisecond)
	if err := a.Updater.ExecNew(); err != nil {
		log.Printf("[update] 就地重启失败: %v", err)
		jsonFail(w, "就地重启失败："+err.Error())
	}
	os.Exit(1) // ExecNew 返回 nil 时按 Exec 语义不会走到；此处兜底防死循环
}
