package handler

import (
	"encoding/json"
	"log"
	"net/http"

	"lumeidc/internal/middleware"
	"lumeidc/internal/plugin"
)

// AdminPlugins 插件管理：清单 / 启停切换 / 后台首页挂件收集。
type AdminPlugins struct{}

type pluginListItem struct {
	Name         string `json:"name"`
	Title        string `json:"title"`
	Version      string `json:"version"`
	Description  string `json:"description"`
	Enabled      bool   `json:"enabled"`
	HasAdminPage bool   `json:"hasAdminPage"`
	HasConfig    bool   `json:"hasConfig"`
	MenuTitle    string `json:"menuTitle,omitempty"`
	MenuIcon     string `json:"menuIcon,omitempty"`
}

func (h *AdminPlugins) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin/plugins", h.List)
	mux.HandleFunc("POST /admin/plugins/{name}/toggle", h.Toggle)
	mux.HandleFunc("GET /admin/widgets", h.Widgets)
	// 前台用户侧：启用且有前台页的插件菜单（登录用户可见）。
	mux.HandleFunc("GET /plugins/client", h.ClientList)
}

// List 全量插件清单（含禁用——管理页需要看到全部；菜单合并由前端按 enabled 过滤）。
func (h *AdminPlugins) List(w http.ResponseWriter, r *http.Request) {
	if !adminRequire(w, r) {
		return
	}
	list := []pluginListItem{}
	for _, p := range plugin.All() {
		info := p.Info()
		item := pluginListItem{
			Name:        info.Name,
			Title:       info.Title,
			Version:     info.Version,
			Description: info.Description,
			Enabled:     plugin.Enabled(info.Name),
		}
		_, item.HasConfig = p.(plugin.ConfigSchemaProvider)
		if mp, ok := p.(plugin.AdminMenuProvider); ok {
			m := mp.AdminMenu()
			item.HasAdminPage = true
			item.MenuTitle = m.Title
			item.MenuIcon = m.Icon
		}
		list = append(list, item)
	}
	writeJSON(w, map[string]any{"ok": 1, "plugins": list})
}

// Toggle 启用/禁用插件（软禁用：即时生效，无需重启；迁移与数据不受影响）。
func (h *AdminPlugins) Toggle(w http.ResponseWriter, r *http.Request) {
	if !adminRequire(w, r) {
		return
	}
	name := r.PathValue("name")
	found := false
	for _, p := range plugin.All() {
		if p.Info().Name == name {
			found = true
			break
		}
	}
	if !found {
		writeJSON(w, map[string]any{"ok": 0, "msg": "插件不存在"})
		return
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, map[string]any{"ok": 0, "msg": "请求格式错误"})
		return
	}
	if err := plugin.SetEnabled(r.Context(), name, req.Enabled); err != nil {
		writeJSON(w, map[string]any{"ok": 0, "msg": "保存失败，请稍后重试"})
		return
	}
	writeJSON(w, map[string]any{"ok": 1})
}

// Widgets 收集启用插件贡献的后台首页挂件（单个失败跳过记日志，不影响其他）。
func (h *AdminPlugins) Widgets(w http.ResponseWriter, r *http.Request) {
	if !adminRequire(w, r) {
		return
	}
	out := []map[string]any{}
	for _, p := range plugin.All() {
		name := p.Info().Name
		if !plugin.Enabled(name) {
			continue
		}
		wp, ok := p.(plugin.AdminWidgetProvider)
		if !ok {
			continue
		}
		data, err := wp.AdminWidget(r.Context())
		if err != nil || data == nil {
			if err != nil {
				log.Printf("[plugin] 插件 %s 挂件取数失败: %v", name, err)
			}
			continue
		}
		out = append(out, map[string]any{"plugin": name, "title": data.Title, "icon": data.Icon, "rows": data.Rows})
	}
	writeJSON(w, map[string]any{"ok": 1, "widgets": out})
}

// ClientList 前台插件菜单：启用且声明了前台页的插件（登录用户）。
func (h *AdminPlugins) ClientList(w http.ResponseWriter, r *http.Request) {
	sess := middleware.FromSession(r.Context())
	if sess == nil || sess.UserID <= 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"ok":0,"msg":"请先登录"}`))
		return
	}
	out := []map[string]string{}
	for _, p := range plugin.All() {
		name := p.Info().Name
		if !plugin.Enabled(name) {
			continue
		}
		cp, ok := p.(plugin.ClientPageProvider)
		if !ok {
			continue
		}
		m := cp.ClientPage()
		to := m.To
		if to == "" {
			to = "/plugin/" + name
		}
		out = append(out, map[string]string{"name": name, "title": m.Title, "icon": m.Icon, "to": to})
	}
	writeJSON(w, map[string]any{"ok": 1, "plugins": out})
}
