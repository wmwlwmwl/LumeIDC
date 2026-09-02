package handler

import (
	"crypto/rand"
	"embed"
	"encoding/base64"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"strings"

	"lumeidc/internal/db"
	"lumeidc/internal/repo"
)

//go:embed templates/*.html
var tplFS embed.FS

type Installer struct {
	ConfigPath string
}

func (h *Installer) isInstalled() bool {
	_, err := os.Stat(h.ConfigPath)
	return err == nil
}

func (h *Installer) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /install", h.form)
	mux.HandleFunc("POST /install", h.submit)
}

func (h *Installer) guard(w http.ResponseWriter) bool {
	if h.isInstalled() {
		http.Error(w, "系统已安装，安装向导已锁定", http.StatusForbidden)
		return false
	}
	return true
}

func (h *Installer) form(w http.ResponseWriter, r *http.Request) {
	if !h.guard(w) {
		return
	}
	tpl, err := template.ParseFS(tplFS, "templates/install.html")
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	tpl.Execute(w, nil)
}

type installForm struct {
	Host, Port, DBName, DBUser, DBPass string
	AdminUser, AdminPass, BaseURL      string
}

func (h *Installer) submit(w http.ResponseWriter, r *http.Request) {
	if !h.guard(w) {
		return
	}
	f := installForm{
		AdminUser: r.PostFormValue("admin_user"),
		AdminPass: r.PostFormValue("admin_pass"),
		BaseURL:   r.PostFormValue("base_url"),
		Host:      strings.TrimSpace(r.PostFormValue("db_host")),
		Port:      strings.TrimSpace(r.PostFormValue("db_port")),
		DBName:    strings.TrimSpace(r.PostFormValue("db_name")),
		DBUser:    strings.TrimSpace(r.PostFormValue("db_user")),
		DBPass:    r.PostFormValue("db_pass"),
	}
	fail := func(msg string) { renderInstallError(w, msg) }
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
	admins := &repo.Admins{DB: database}
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
base_url: %q
allow_insecure_db: true
`, listenEnv(), dsn, base64.StdEncoding.EncodeToString(key), base64.StdEncoding.EncodeToString(piiKey), "data/private", f.BaseURL)
	if err := atomicWriteFile(h.ConfigPath, []byte(cfgYAML), 0o600); err != nil {
		fail("写入 config.yaml 失败（需要当前目录可写权限）: " + err.Error())
		return
	}
	tpl, _ := template.ParseFS(tplFS, "templates/done.html")
	tpl.Execute(w, nil)
}

func listenEnv() string {
	if a := os.Getenv("LISTEN"); a != "" {
		return a
	}
	return ":8080"
}

func renderInstallError(w http.ResponseWriter, msg string) {
	w.WriteHeader(http.StatusBadRequest)
	tpl, _ := template.ParseFS(tplFS, "templates/error.html")
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
