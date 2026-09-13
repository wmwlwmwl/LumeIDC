package handler

import (
	"net/http"
	"sort"
	"strconv"
	"strings"

	"lumeidc/internal/repo"
)

// announcementItem 前台公告 → 小写键 JSON 视图。
func announcementItem(an repo.Announcement) map[string]any {
	return map[string]any{
		"id": an.ID, "title": an.Title, "category": an.Category, "summary": an.Summary,
		"content": an.Content, "cover": an.Cover, "pinned": an.Pinned, "reads": an.Reads,
		"created_at": an.CreatedAt.Format("2006-01-02"),
	}
}

// announcementsList GET /announcements — 前台公告列表（分类/关键字/分页，仅显示中）。
func (h *Pages) announcementsList(w http.ResponseWriter, r *http.Request) {
	if h.Announcements == nil {
		writeJSON(w, map[string]any{"ok": 1, "list": []any{}, "total": 0, "page": 1, "limit": 10, "categories": []string{}})
		return
	}
	q := r.URL.Query()
	category := strings.TrimSpace(q.Get("category"))
	keyword := strings.ToLower(strings.TrimSpace(q.Get("keyword")))
	page, err := strconv.Atoi(q.Get("page"))
	if err != nil || page < 1 {
		page = 1
	}
	limit, err := strconv.Atoi(q.Get("limit"))
	if err != nil || limit < 1 || limit > 50 {
		limit = 10
	}

	rows, err := h.Announcements.List(r.Context(), true)
	if err != nil {
		writeJSON(w, map[string]any{"ok": 0, "msg": "查询失败"})
		return
	}
	catSet := map[string]struct{}{}
	filtered := make([]map[string]any, 0, len(rows))
	for _, an := range rows {
		if an.Category != "" {
			catSet[an.Category] = struct{}{}
		}
		if category != "" && an.Category != category {
			continue
		}
		if keyword != "" && !strings.Contains(strings.ToLower(an.Title), keyword) {
			continue
		}
		filtered = append(filtered, announcementItem(an))
	}

	categories := make([]string, 0, len(catSet))
	for c := range catSet {
		categories = append(categories, c)
	}
	sort.Strings(categories)

	total := len(filtered)
	start := (page - 1) * limit
	if start > total {
		start = total
	}
	end := start + limit
	if end > total {
		end = total
	}

	writeJSON(w, map[string]any{
		"ok": 1, "list": filtered[start:end], "total": total,
		"page": page, "limit": limit, "categories": categories,
	})
}

// announcementDetail GET /announcements/{id} — 公告详情（阅读量 +1）。
func (h *Pages) announcementDetail(w http.ResponseWriter, r *http.Request) {
	if h.Announcements == nil {
		http.NotFound(w, r)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	an, err := h.Announcements.Get(r.Context(), id)
	if err != nil || an.Hidden {
		http.NotFound(w, r)
		return
	}
	if err := h.Announcements.IncRead(r.Context(), id); err == nil {
		an.Reads++
	}
	writeJSON(w, map[string]any{"ok": 1, "item": announcementItem(*an)})
}
