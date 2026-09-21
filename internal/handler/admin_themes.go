package handler

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"strings"

	"lumeidc/internal/repo"
	"lumeidc/internal/service"
)

// ThemeInfo 描述一套前台模板。
type ThemeInfo struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Author      string `json:"author"`
	Version     string `json:"version"`
	Active      bool   `json:"active"`
}

// themeMeta theme.json 的可选元信息。
type themeMeta struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Author      string `json:"author"`
	Version     string `json:"version"`
}

// validThemeKey 模板目录 key 只允许安全字符，禁止路径穿越。
func validThemeKey(key string) bool {
	if key == "" || len(key) > 64 {
		return false
	}
	for _, c := range key {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '_' {
			continue
		}
		return false
	}
	return true
}

// listThemes 扫描 dist/themes/ 列出可用模板（含 theme.json 元信息）。
func listThemes(dist fs.FS) ([]ThemeInfo, error) {
	entries, err := fs.ReadDir(dist, "themes")
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var list []ThemeInfo
	for _, e := range entries {
		if !e.IsDir() || !validThemeKey(e.Name()) {
			continue
		}
		key := e.Name()
		if _, err := fs.Stat(dist, "themes/"+key+"/index.html"); err != nil {
			continue // 无入口文件，不是有效模板
		}
		t := ThemeInfo{Key: key, Name: key}
		if raw, err := fs.ReadFile(dist, "themes/"+key+"/theme.json"); err == nil {
			var m themeMeta
			if json.Unmarshal(raw, &m) == nil {
				if m.Name != "" {
					t.Name = m.Name
				}
				t.Description, t.Author, t.Version = m.Description, m.Author, m.Version
			}
		}
		list = append(list, t)
	}
	return list, nil
}

// activeThemeKey 读当前激活模板，校验合法性并回退 default。
func activeThemeKey(r *http.Request, settings *repo.Settings, dist fs.FS) string {
	key := ""
	if settings != nil {
		key, _ = settings.Get(r.Context(), service.KeySiteTheme)
	}
	if !validThemeKey(key) {
		return "default"
	}
	if _, err := fs.Stat(dist, "themes/"+key+"/index.html"); err != nil {
		return "default"
	}
	return key
}

// themesListCtx GET /admin/themes：模板列表 + 当前激活。
func (a *Admin) themesListCtx(w http.ResponseWriter, r *http.Request) {
	if !a.require(w, r) {
		return
	}
	dist, err := webUIDist()
	if err != nil {
		writeJSON(w, map[string]any{"ok": 0, "msg": "前端资源未构建"})
		return
	}
	list, err := listThemes(dist)
	if err != nil {
		writeJSON(w, map[string]any{"ok": 0, "msg": "读取模板列表失败"})
		return
	}
	if list == nil {
		list = []ThemeInfo{}
	}
	active := activeThemeKey(r, a.Settings, dist)
	for i := range list {
		list[i].Active = list[i].Key == active
	}
	writeJSON(w, map[string]any{"ok": 1, "themes": list})
}

// themeSwitchCtx POST /admin/themes/switch：切换前台模板。
func (a *Admin) themeSwitchCtx(w http.ResponseWriter, r *http.Request) {
	if !a.require(w, r) {
		return
	}
	key := strings.TrimSpace(jsonVals(r)["key"])
	if !validThemeKey(key) {
		writeJSON(w, map[string]any{"ok": 0, "msg": "模板标识不合法"})
		return
	}
	dist, err := webUIDist()
	if err != nil {
		writeJSON(w, map[string]any{"ok": 0, "msg": "前端资源未构建"})
		return
	}
	if _, err := fs.Stat(dist, "themes/"+key+"/index.html"); err != nil {
		writeJSON(w, map[string]any{"ok": 0, "msg": "模板不存在"})
		return
	}
	if err := a.Settings.Set(r.Context(), service.KeySiteTheme, key); err != nil {
		writeJSON(w, map[string]any{"ok": 0, "msg": "保存失败，请稍后重试"})
		return
	}
	writeJSON(w, map[string]any{"ok": 1})
}

// themePreviewCtx GET /admin/themes/{key}/preview：模板预览图。
func (a *Admin) themePreviewCtx(w http.ResponseWriter, r *http.Request) {
	if !a.require(w, r) {
		return
	}
	key := r.PathValue("key")
	if !validThemeKey(key) {
		http.NotFound(w, r)
		return
	}
	dist, err := webUIDist()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	p := "themes/" + key + "/theme.png"
	if _, err := fs.Stat(dist, p); err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	http.ServeFileFS(w, r, dist, p)
}
