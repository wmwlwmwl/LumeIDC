package violation

import (
	"net/http"
	"strconv"

	"lumeidc/internal/plugin"
)

// clientFeed 前台违规公示流：公告区（置顶优先，全部可见）+ 公示记录分页。
// 需登录；用户身份脱敏（name 或「用户#id」），不回邮箱/备注/处理人。
func (p *Plugin) clientFeed(w http.ResponseWriter, r *http.Request) {
	if _, ok := plugin.RequireUserID(w, r); !ok {
		return
	}
	q := r.URL.Query()
	page, limit := pageParam(q)

	anns, err := p.anns.PublicList(r.Context())
	if err != nil {
		plugin.JSONFail(w, "查询失败")
		return
	}
	total, err := p.records.PublicCount(r.Context())
	if err != nil {
		plugin.JSONFail(w, "查询失败")
		return
	}
	rows, err := p.records.PublicList(r.Context(), page, limit)
	if err != nil {
		plugin.JSONFail(w, "查询失败")
		return
	}

	list := make([]map[string]any, 0, len(anns)+len(rows))
	for _, a := range anns {
		item := announcementItem(a)
		item["kind"] = "announcement"
		list = append(list, item)
	}
	for _, row := range rows {
		item := recordItem(row, false)
		item["kind"] = "record"
		item["user_name"] = maskUser(row.UserName, row.UserID)
		list = append(list, item)
	}
	plugin.WriteJSON(w, map[string]any{"ok": 1, "list": list, "total": total, "page": page, "limit": limit})
}

// clientAnnouncement 前台公告详情（隐藏的不可见；阅读量 +1）。
func (p *Plugin) clientAnnouncement(w http.ResponseWriter, r *http.Request) {
	if _, ok := plugin.RequireUserID(w, r); !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		plugin.StatusFail(w, 400, "参数错误")
		return
	}
	ann, err := p.anns.Get(r.Context(), id)
	if err != nil {
		plugin.JSONFail(w, "查询失败")
		return
	}
	if ann == nil || ann.Hidden {
		http.NotFound(w, r)
		return
	}
	_ = p.anns.IncrReads(r.Context(), id)
	plugin.WriteJSON(w, map[string]any{"ok": 1, "item": announcementItem(*ann)})
}

// clientRecord 前台公示记录详情（严格 public+有效期，否则 404；身份脱敏）。
func (p *Plugin) clientRecord(w http.ResponseWriter, r *http.Request) {
	if _, ok := plugin.RequireUserID(w, r); !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		plugin.StatusFail(w, 400, "参数错误")
		return
	}
	row, err := p.records.PublicGet(r.Context(), id)
	if err != nil {
		plugin.JSONFail(w, "查询失败")
		return
	}
	if row == nil {
		http.NotFound(w, r)
		return
	}
	item := recordItem(*row, false)
	item["user_name"] = maskUser(row.UserName, row.UserID)
	plugin.WriteJSON(w, map[string]any{"ok": 1, "item": item})
}
