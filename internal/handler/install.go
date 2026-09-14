package handler

import (
	"crypto/rand"
	"embed"
	"encoding/base64"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"

	"lumeidc/internal/config"
	"lumeidc/internal/db"
	"lumeidc/internal/repo"
)

//go:embed templates/done.html templates/error.html
var tplFS embed.FS

type Installer struct {
	ConfigPath string
	// OnInstalled 在 config.yaml 写入成功后回调新配置；main 借此在同一进程内
	// 切换到完整应用，实现免手动重启的安装体验。nil 时保持"重启后生效"行为。
	OnInstalled func(cfg *config.Config)
}

func (h *Installer) isInstalled() bool {
	_, err := os.Stat(h.ConfigPath)
	return err == nil
}

func (h *Installer) Register(mux *http.ServeMux) {
	// 安装页由 SPA 承载（GET /install 由 webui.InstallPage 提供），这里只保留提交接口。
	mux.HandleFunc("POST /install", h.submit)
}

func (h *Installer) guard(w http.ResponseWriter, r *http.Request) bool {
	if h.isInstalled() {
		if wantsJSON(r) {
			writeJSON(w, map[string]any{"ok": 0, "msg": "系统已安装，安装向导已锁定"})
			return false
		}
		http.Error(w, "系统已安装，安装向导已锁定", http.StatusForbidden)
		return false
	}
	return true
}

type installForm struct {
	Host, Port, DBName, DBUser, DBPass string
	AdminUser, AdminPass               string
}

func (h *Installer) submit(w http.ResponseWriter, r *http.Request) {
	if !h.guard(w, r) {
		return
	}
	// SPA 用 JSON 提交，SSR 表单用 PostFormValue；两者统一。
	vals := jsonVals(r)
	fv := func(k string) string {
		if vals != nil {
			return vals[k]
		}
		return r.PostFormValue(k)
	}
	f := installForm{
		AdminUser: fv("admin_user"),
		AdminPass: fv("admin_pass"),
		Host:      strings.TrimSpace(fv("db_host")),
		Port:      strings.TrimSpace(fv("db_port")),
		DBName:    strings.TrimSpace(fv("db_name")),
		DBUser:    strings.TrimSpace(fv("db_user")),
		DBPass:    fv("db_pass"),
	}
	fail := func(msg string) {
		if wantsJSON(r) {
			writeJSON(w, map[string]any{"ok": 0, "msg": msg})
			return
		}
		renderInstallError(w, msg)
	}
	if f.Host == "" || f.Port == "" || f.DBName == "" || f.DBUser == "" || f.AdminUser == "" || len(f.AdminPass) < 8 {
		fail("数据库地址/端口/库名/用户、管理员用户名必填，管理员密码至少 8 位")
		return
	}
	dsn := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
		url.PathEscape(f.DBUser), url.QueryEscape(f.DBPass), f.Host, f.Port, f.DBName)
	database, err := db.Open(dsn)
	if err != nil {
		fail(err.Error())
		return
	}
	defer database.Close()
	if err := db.Migrate(r.Context(), database, db.Migrations()); err != nil {
		fail(err.Error())
		return
	}
	admins := repo.NewAdmins(database)
	if err := admins.Create(r.Context(), f.AdminUser, f.AdminPass); err != nil {
		fail("创建管理员失败: " + err.Error())
		return
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		fail("生成会话密钥失败")
		return
	}
	piiKey := make([]byte, 32)
	if _, err := rand.Read(piiKey); err != nil {
		fail("生成实名资料密钥失败")
		return
	}
	cfgYAML := fmt.Sprintf(`listen: %q
db_dsn: %q
secret_key: %q
pii_key: %q
private_data_dir: %q
allow_insecure_db: true
`, listenEnv(), dsn, base64.StdEncoding.EncodeToString(key), base64.StdEncoding.EncodeToString(piiKey), "data/private")
	if err := atomicWriteFile(h.ConfigPath, []byte(cfgYAML), 0o600); err != nil {
		fail("写入 config.yaml 失败（需要当前目录可写权限）: " + err.Error())
		return
	}
	if wantsJSON(r) {
		writeJSON(w, map[string]any{"ok": 1, "msg": "安装完成"})
	} else {
		tpl, _ := parseArtTemplate("templates/done.html")
		tpl.Execute(w, nil)
		if f, ok := w.(http.Flusher); ok {
			f.Flush() // 先让浏览器收到成功页，再切换服务
		}
	}
	if h.OnInstalled != nil {
		if cfg, err := config.Load(h.ConfigPath); err == nil {
			h.OnInstalled(cfg)
		} else {
			log.Printf("[install] 安装完成但配置加载失败（重启后仍可生效）: %v", err)
		}
	}
}

func listenEnv() string {
	if a := os.Getenv("LISTEN"); a != "" {
		return a
	}
	return ":8080"
}

func renderInstallError(w http.ResponseWriter, msg string) {
	w.WriteHeader(http.StatusBadRequest)
	tpl, _ := parseArtTemplate("templates/error.html")
	tpl.Execute(w, map[string]string{"Message": msg})
}

// atomicWriteFile 原子写入文件：先写临时文件，再 rename，避免写入中途崩溃导致文件损坏。
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
