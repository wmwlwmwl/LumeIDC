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
		jsonStatus(w, r, 500, "查询失败")
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, an := range rows {
		out = append(out, map[string]any{
			"id": an.ID, "title": an.Title, "category": an.Category, "summary": an.Summary,
			"content": an.Content, "cover": an.Cover, "reads": an.Reads,
			"hidden": an.Hidden, "pinned": an.Pinned,
			"created_at": an.CreatedAt.Format("2006-01-02 15:04"), "updated_at": an.UpdatedAt.Format("2006-01-02 15:04"),
		})
	}
	writeJSON(w, map[string]any{"ok": 1, "list": out})
}

// adminAnnouncementForm GET /admin/announcements/edit?id= — 新建/编辑表单。

func (a *Admin) adminAnnouncementForm(w http.ResponseWriter, r *http.Request) {
	if !a.require(w, r) {
		return
	}
	var announcement any
	if id := r.URL.Query().Get("id"); id != "" {
		if pid, err := strconv.ParseInt(id, 10, 64); err == nil {
			if an, gerr := a.Announcements.Get(r.Context(), pid); gerr == nil {
				announcement = an
			}
		}
	}
	writeJSON(w, map[string]any{"ok": 1, "announcement": announcement})
}

// adminAnnouncementSave POST /admin/announcements/save — 保存（新增/更新）。

func (a *Admin) adminAnnouncementSave(w http.ResponseWriter, r *http.Request) {
	if !a.require(w, r) {
		return
	}
	vals := jsonVals(r)
	fv := func(k string) string {
		if vals != nil {
			return vals[k]
		}
		return r.PostFormValue(k)
	}
	if vals == nil {
		if err := r.ParseForm(); err != nil {
			jsonStatus(w, r, 400, "bad form")
			return
		}
	}
	id, _ := strconv.ParseInt(fv("id"), 10, 64)
	title := strings.TrimSpace(fv("title"))
	category := strings.TrimSpace(fv("category"))
	summary := strings.TrimSpace(fv("summary"))
	content := fv("content")
	cover := strings.TrimSpace(fv("cover"))
	hidden := fv("hidden") != "" && fv("hidden") != "0"
	pinned := fv("pinned") != "" && fv("pinned") != "0"
	if title == "" {
		jsonStatus(w, r, 400, "标题不能为空")
		return
	}
	if err := a.Announcements.Save(r.Context(), id, title, category, summary, content, cover, hidden, pinned); err != nil {
		if wantsJSON(r) {
			writeJSON(w, map[string]any{"ok": 0, "msg": err.Error()})
			return
		}
		http.Redirect(w, r, "/admin/announcements?err="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	if wantsJSON(r) {
		writeJSON(w, map[string]any{"ok": 1, "msg": "已保存"})
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
		if wantsJSON(r) {
			writeJSON(w, map[string]any{"ok": 0, "msg": err.Error()})
			return
		}
		http.Redirect(w, r, "/admin/announcements?err="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	if wantsJSON(r) {
		writeJSON(w, map[string]any{"ok": 1, "msg": "已删除"})
		return
	}
	http.Redirect(w, r, "/admin/announcements?msg=已删除", http.StatusSeeOther)
}

// adminCoupons GET /admin/coupons — 优惠码列表 + 新建表单。
