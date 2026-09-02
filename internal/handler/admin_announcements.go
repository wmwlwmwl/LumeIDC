package handler

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

func (a *Admin) adminAnnouncements(w http.ResponseWriter, r *http.Request) {
	if !a.require(w, r) {
		return
	}
	rows, err := a.Announcements.List(r.Context(), false)
	if err != nil {
		http.Error(w, "查询失败", 500)
		return
	}
	a.renderAdmin(w, "admin_announcements.html", AdminData{
		Rows:  rows,
		CSRF:  a.adminCSRF(w, r),
		Error: r.URL.Query().Get("err"),
		Msg:   r.URL.Query().Get("msg"),
	})
}

// adminAnnouncementForm GET /admin/announcements/edit?id= — 新建/编辑表单。

func (a *Admin) adminAnnouncementForm(w http.ResponseWriter, r *http.Request) {
	if !a.require(w, r) {
		return
	}
	data := AdminData{CSRF: a.adminCSRF(w, r)}
	if id := r.URL.Query().Get("id"); id != "" {
		if pid, err := strconv.ParseInt(id, 10, 64); err == nil {
			// 仅成功时赋值：把类型化 nil 指针塞进 any 会让模板 {{if .Product}} 判空失效。
			if an, gerr := a.Announcements.Get(r.Context(), pid); gerr == nil {
				data.Product = an
			}
		}
	}
	a.renderAdmin(w, "admin_announcement_form.html", data)
}

// adminAnnouncementSave POST /admin/announcements/save — 保存（新增/更新）。

func (a *Admin) adminAnnouncementSave(w http.ResponseWriter, r *http.Request) {
	if !a.require(w, r) {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", 400)
		return
	}
	id, _ := strconv.ParseInt(r.PostFormValue("id"), 10, 64)
	title := strings.TrimSpace(r.PostFormValue("title"))
	content := r.PostFormValue("content")
	hidden := r.PostFormValue("hidden") != ""
	pinned := r.PostFormValue("pinned") != ""
	if title == "" {
		http.Redirect(w, r, "/admin/announcements?err=标题不能为空", http.StatusSeeOther)
		return
	}
	if err := a.Announcements.Save(r.Context(), id, title, content, hidden, pinned); err != nil {
		http.Redirect(w, r, "/admin/announcements?err="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/admin/announcements?msg=已保存", http.StatusSeeOther)
}

// adminAnnouncementDelete POST /admin/announcements/{id}/delete — 删除。

func (a *Admin) adminAnnouncementDelete(w http.ResponseWriter, r *http.Request) {
	if !a.require(w, r) {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err := a.Announcements.Delete(r.Context(), id); err != nil {
		http.Redirect(w, r, "/admin/announcements?err="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/admin/announcements?msg=已删除", http.StatusSeeOther)
}

// adminCoupons GET /admin/coupons — 优惠码列表 + 新建表单。
