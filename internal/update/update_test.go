package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestVersionNewer(t *testing.T) {
	cases := []struct {
		cur, latest string
		want        bool
	}{
		{"v1.2.3", "v1.2.4", true},
		{"v1.2.3", "v1.2.3", false},
		{"1.2", "1.2.0", false},
		{"v0.9.9", "v1.0.0", true},
		{"dev", "v1.0.0", true},
		{"", "v1.0.0", true},
		{"v1.2.3", "", false},
		{"v1.2.3", "v1.2.beta", false}, // 最新版非标准段，视为无可升级版本
		{"v1.2.beta", "v1.2.0", false}, // 非纯数字段不 panic，退化字典序
	}
	for _, c := range cases {
		if got := VersionNewer(c.cur, c.latest); got != c.want {
			t.Errorf("VersionNewer(%q, %q) = %v, want %v", c.cur, c.latest, got, c.want)
		}
	}
}

func TestVerifySHA256(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "bin")
	content := []byte("hello lumeidc")
	if err := os.WriteFile(p, content, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	want := hex.EncodeToString(sum[:])
	if err := verifySHA256(p, want); err != nil {
		t.Fatalf("期望校验通过: %v", err)
	}
	if err := verifySHA256(p, strings.Repeat("f", 64)); err == nil {
		t.Fatal("期望校验失败（校验和不匹配）")
	}
	if err := verifySHA256(filepath.Join(dir, "missing"), want); err == nil {
		t.Fatal("期望校验失败（文件不存在）")
	}
}

func TestParseRelease(t *testing.T) {
	body := []byte(`{
  "tag_name": "v1.2.3",
  "published_at": "2026-09-04T00:00:00Z",
  "body": "更新日志内容",
  "assets": [
    {"name": "lumeidc_linux_amd64", "browser_download_url": "https://x/lumeidc_linux_amd64"},
    {"name": "lumeidc_linux_amd64.sha256", "browser_download_url": "https://x/lumeidc_linux_amd64.sha256"},
    {"name": "lumeidc_linux_arm64", "browser_download_url": "https://x/lumeidc_linux_arm64"},
    {"name": "lumeidc_linux_arm64.sha256", "browser_download_url": "https://x/lumeidc_linux_arm64.sha256"}
  ]
}`)
	rel, binURL, shaURL, ok := parseRelease(body, "linux", "arm64")
	if !ok {
		t.Fatal("期望匹配到 arm64 资产")
	}
	if rel.TagName != "v1.2.3" {
		t.Errorf("TagName = %q, want v1.2.3", rel.TagName)
	}
	if binURL != "https://x/lumeidc_linux_arm64" {
		t.Errorf("binURL = %q", binURL)
	}
	if shaURL != "https://x/lumeidc_linux_arm64.sha256" {
		t.Errorf("shaURL = %q", shaURL)
	}
	if _, _, _, ok := parseRelease(body, "windows", "amd64"); ok {
		t.Fatal("期望 windows 平台无匹配资产")
	}
	if _, _, _, ok := parseRelease([]byte("not json"), "linux", "amd64"); ok {
		t.Fatal("期望非法 JSON 解析失败")
	}
}

func TestBinaryName(t *testing.T) {
	if got := BinaryName("linux", "amd64"); got != "lumeidc_linux_amd64" {
		t.Errorf("BinaryName = %q", got)
	}
}

// fakeSource 起一个本地假安装源（二进制 + .sha256 侧车）。
func fakeSource(t *testing.T, bin []byte) *httptest.Server {
	t.Helper()
	sum := sha256.Sum256(bin)
	mux := http.NewServeMux()
	mux.HandleFunc("/bin", func(w http.ResponseWriter, r *http.Request) { w.Write(bin) })
	mux.HandleFunc("/bin.sha256", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  lumeidc_linux_amd64\n", hex.EncodeToString(sum[:]))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// TestInstallSwap 下载→校验→备份→替换 全链路。
func TestInstallSwap(t *testing.T) {
	bin := []byte("fake lumeidc binary v2")
	srv := fakeSource(t, bin)
	dir := t.TempDir()
	exe := filepath.Join(dir, "lumeidc_linux_amd64")
	if err := os.WriteFile(exe, []byte("old binary v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	c := &Client{}
	if err := c.install(context.Background(), exe, "v1.2.3", srv.URL+"/bin", srv.URL+"/bin.sha256"); err != nil {
		t.Fatalf("install 失败: %v", err)
	}
	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(bin) {
		t.Fatalf("替换后内容 = %q, want %q", got, bin)
	}
	if _, err := os.Stat(exe + ".bak"); err != nil {
		t.Fatal("期望存在备份文件 .bak")
	}
	// Windows 无 Unix 权限位，仅 Linux 上校验替换后权限。
	if runtime.GOOS != "windows" {
		if info, _ := os.Stat(exe); info.Mode().Perm() != 0o755 {
			t.Fatalf("替换后权限 = %v, want 0755", info.Mode().Perm())
		}
	}
}

// TestInstallChecksumAbort 校验不匹配时必须中止且不碰原文件。
func TestInstallChecksumAbort(t *testing.T) {
	old := []byte("old binary v1")
	dir := t.TempDir()
	exe := filepath.Join(dir, "lumeidc_linux_amd64")
	if err := os.WriteFile(exe, old, 0o755); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/bin", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("tampered binary")) })
	mux.HandleFunc("/bin.sha256", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, strings.Repeat("f", 64), " lumeidc_linux_amd64")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := &Client{}
	if err := c.install(context.Background(), exe, "v1.2.3", srv.URL+"/bin", srv.URL+"/bin.sha256"); err == nil {
		t.Fatal("期望校验失败返回错误")
	}
	got, _ := os.ReadFile(exe)
	if string(got) != string(old) {
		t.Fatalf("原文件被改动: %q", got)
	}
	if _, err := os.Stat(exe + ".bak"); err == nil {
		t.Fatal("校验失败时不应产生备份")
	}
}
