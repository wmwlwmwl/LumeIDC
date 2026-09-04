// Package update 提供后台在线更新：从 GitHub Releases 检查最新版本，
// 下载匹配平台的二进制并 SHA256 校验，备份原文件后原子替换。
// 替换成功后不自动重启（1Panel/宝塔部署形态差异大），由管理员手动重启生效。
package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Asset GitHub Release 资产。
type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type releaseJSON struct {
	TagName     string  `json:"tag_name"`
	PublishedAt string  `json:"published_at"`
	Body        string  `json:"body"`
	Assets      []Asset `json:"assets"`
}

// Info 更新页展示数据。
type Info struct {
	CurrentVersion string `json:"current_version"`
	Version        string `json:"version"`
	PublishedAt    string `json:"published_at"`
	Notes          string `json:"notes"`
	Available      bool   `json:"available"`      // 存在可安装的新版本
	Hint           string `json:"hint,omitempty"` // 不可用原因（已最新/无匹配包）
}

// Client 从 GitHub Releases 拉取更新的客户端。
// Repo 与 Version 由组合根注入；HTTP 可覆盖（测试用）。
type Client struct {
	Repo    string       // 形如 wmwlwmwl/LumeIDC
	Version string       // 当前版本（main 注入）
	HTTP    *http.Client // 默认 60s 超时
	mu      sync.Mutex   // 串行化检查/应用，防并发替换
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 60 * time.Second}
}

// Check 查询最新发布版并返回对比信息，不触发下载。
func (c *Client) Check(ctx context.Context) (*Info, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	rel, _, _, ok, err := c.checkLocked(ctx)
	if err != nil {
		return nil, err
	}
	info := &Info{
		CurrentVersion: c.Version,
		Version:        rel.TagName,
		PublishedAt:    rel.PublishedAt,
		Notes:          rel.Body,
	}
	switch {
	case !VersionNewer(c.Version, rel.TagName):
		info.Hint = "当前已是最新版本"
	case !ok:
		info.Hint = fmt.Sprintf("没有找到适用于 %s/%s 的安装包", runtime.GOOS, runtime.GOARCH)
	default:
		info.Available = true
	}
	return info, nil
}

// Apply 下载最新版并替换当前二进制；成功后返回待重启，失败时恢复原文件。
func (c *Client) Apply(ctx context.Context) error {
	if runtime.GOOS != "linux" {
		return errors.New("系统更新仅支持 Linux 服务器")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	rel, binURL, shaURL, ok, err := c.checkLocked(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("没有找到适用于 %s/%s 的安装包", runtime.GOOS, runtime.GOARCH)
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("定位程序路径失败: %w", err)
	}
	return c.install(ctx, exe, rel.TagName, binURL, shaURL)
}

// checkLocked 在调用方持锁前提下查询并解析最新发布。
func (c *Client) checkLocked(ctx context.Context) (rel releaseJSON, binURL, shaURL string, ok bool, err error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", c.Repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return rel, "", "", false, err
	}
	// GitHub API 强制要求 User-Agent，否则返回 403。
	req.Header.Set("User-Agent", "LumeIDC-Update/"+c.Version)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := c.http().Do(req)
	if err != nil {
		return rel, "", "", false, fmt.Errorf("检查更新失败（网络错误）: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 120))
		msg := fmt.Sprintf("检查更新失败: GitHub API 返回 http %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
		if resp.StatusCode == http.StatusForbidden {
			msg += "（触发限流时请稍后再试）"
		}
		return rel, "", "", false, errors.New(msg)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return rel, "", "", false, fmt.Errorf("读取更新信息失败: %w", err)
	}
	rel, binURL, shaURL, ok = parseRelease(data, runtime.GOOS, runtime.GOARCH)
	return rel, binURL, shaURL, ok, nil
}

// install 下载、校验、备份并原子替换二进制；任何失败尽量恢复原文件。
func (c *Client) install(ctx context.Context, exe, tag, binURL, shaURL string) error {
	dir := filepath.Dir(exe)
	tmp, err := os.CreateTemp(dir, ".lumeidc-update-*")
	if err != nil {
		return fmt.Errorf("无法在程序目录创建临时文件，请检查挂载目录写权限: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	log.Printf("[update] 开始下载 %s (%s)", binaryLabel(), binURL)
	if err := c.downloadTo(ctx, tmp, binURL); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("写入安装包失败: %w", err)
	}
	want, err := c.fetchSHA(ctx, shaURL)
	if err != nil {
		return err
	}
	if err := verifySHA256(tmpPath, want); err != nil {
		return fmt.Errorf("安装包校验失败，已中止（未改动原文件）: %w", err)
	}
	log.Printf("[update] 校验通过，备份当前版本并替换: %s", exe)
	if err := os.Rename(exe, exe+".bak"); err != nil {
		return fmt.Errorf("备份当前程序失败: %w", err)
	}
	if err := os.Rename(tmpPath, exe); err != nil {
		_ = os.Rename(exe+".bak", exe) // 恢复原文件
		return fmt.Errorf("替换程序失败，已恢复原版本: %w", err)
	}
	_ = os.Chmod(exe, 0o755)
	log.Printf("[update] 已更新 %s → %s，等待手动重启生效", exe, tag)
	return nil
}

func (c *Client) downloadTo(ctx context.Context, w io.Writer, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return fmt.Errorf("下载安装包失败（网络错误）: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("下载安装包失败: 下载源返回 http %d", resp.StatusCode)
	}
	if _, err := io.Copy(w, resp.Body); err != nil {
		return fmt.Errorf("下载安装包失败: %w", err)
	}
	return nil
}

// fetchSHA 读取 .sha256 侧车（标准 sha256sum 格式，取首字段 hex）。
func (c *Client) fetchSHA(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return "", fmt.Errorf("获取校验文件失败（网络错误）: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("获取校验文件失败: 下载源返回 http %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if err != nil {
		return "", fmt.Errorf("读取校验文件失败: %w", err)
	}
	fields := strings.Fields(string(b))
	if len(fields) == 0 {
		return "", errors.New("SHA256 校验文件内容无效")
	}
	return fields[0], nil
}

// BinaryName 与发布工作流的产物命名严格一致（小写）。
func BinaryName(goos, goarch string) string {
	return "lumeidc_" + goos + "_" + goarch
}

func sha256Name(goos, goarch string) string {
	return BinaryName(goos, goarch) + ".sha256"
}

func binaryLabel() string {
	return BinaryName(runtime.GOOS, runtime.GOARCH)
}

// parseRelease 解析 releases/latest 响应，按平台精确匹配二进制与校验文件。
func parseRelease(body []byte, goos, goarch string) (rel releaseJSON, binURL, shaURL string, ok bool) {
	if err := json.Unmarshal(body, &rel); err != nil {
		return rel, "", "", false
	}
	want, shaWant := BinaryName(goos, goarch), sha256Name(goos, goarch)
	for _, a := range rel.Assets {
		switch a.Name {
		case want:
			binURL = a.BrowserDownloadURL
		case shaWant:
			shaURL = a.BrowserDownloadURL
		}
	}
	return rel, binURL, shaURL, binURL != "" && shaURL != ""
}

// VersionNewer 判断 latest 是否严格高于 current。
// 格式 v1.2.3（允许缺 v）；dev/空视为无正式版本，有正式版即可升级。
func VersionNewer(current, latest string) bool {
	cur, l := trimV(current), trimV(latest)
	if l == "" {
		return false // 最新版无效
	}
	if cur == "" || cur == "dev" {
		return l != "dev"
	}
	cn, ln := splitNums(cur), splitNums(l)
	if ln == nil {
		// 最新版含非纯数字段（非标准发布），保守视为不可升级。
		return false
	}
	if cn == nil {
		// 当前版本非标准（如 1.2.beta），退化为整体字典序比较。
		return cur != l && l > cur
	}
	for i := 0; i < len(cn) || i < len(ln); i++ {
		c, v := 0, 0
		if i < len(cn) {
			c = cn[i]
		}
		if i < len(ln) {
			v = ln[i]
		}
		if c != v {
			return v > c
		}
	}
	return false
}

func trimV(s string) string {
	return strings.TrimPrefix(strings.TrimSpace(s), "v")
}

// splitNums 提取全部纯数字段；存在非纯数字段（beta 等）时返回 nil。
func splitNums(s string) []int {
	parts := strings.Split(s, ".")
	nums := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil
		}
		nums = append(nums, n)
	}
	return nums
}

// verifySHA256 校验文件内容与期望 hex 是否一致。
func verifySHA256(path, want string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, strings.TrimSpace(want)) {
		return fmt.Errorf("实际 %s，期望 %s", got, want)
	}
	return nil
}
