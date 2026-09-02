package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"lumeidc/internal/middleware"
	"lumeidc/internal/server"
)

type moduleBlock struct {
	Key     string
	Name    string
	Content string
}

// moduleAssetRE 匹配上游面板自身绝对资源地址（/vendor/ 前缀），据此隐藏上游域名。

var moduleAssetRE = regexp.MustCompile(`(https?://[A-Za-z0-9._\-:]+)/vendor/`)

// moduleAssetPrefix 该服务内联资源代理前缀，host64 为 base64(origin) 站点专用码。

func moduleAssetPrefix(serviceID int64, origin string) string {
	token := base64.RawURLEncoding.EncodeToString([]byte(origin))
	return "/services/" + strconv.FormatInt(serviceID, 10) + "/module-assets/" + token
}

// proxyClient 资源代理专用客户端，固定超时避免慢上游耗尽 goroutine（DoS）。

var proxyClient = &http.Client{
	Timeout: 15 * time.Second,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// privateHostname 命中内网/保留地址段，禁止代理，避免 SSRF 打元数据或内网。

func isPrivateHostname(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	if h == "localhost" || h == "0.0.0.0" || h == "::1" || h == "[::1]" {
		return true
	}
	if strings.HasPrefix(h, "127.") || strings.HasPrefix(h, "10.") ||
		strings.HasPrefix(h, "192.168.") || strings.HasPrefix(h, "169.254.") {
		return true
	}
	if strings.HasPrefix(h, "172.") {
		// 172.16.0.0/12
		parts := strings.Split(h, ".")
		if len(parts) == 4 {
			if n, err := strconv.Atoi(parts[1]); err == nil && n >= 16 && n <= 31 {
				return true
			}
		}
	}
	return false
}

// proxyOriginAllowed 仅允许代理到管理员在 servers 表中配置的上下游面板主机。
// 即便客户端传入任意 origin，也必须命中已登记主机才放行，杜绝 SSRF 到任意公网/内网。

func (h *Pages) proxyOriginAllowed(ctx context.Context, origin string) bool {
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	host := u.Hostname()
	if host == "" || isPrivateHostname(host) {
		return false
	}
	ok, err := h.ServersRepo.IsAllowedProxyHost(ctx, host)
	if err != nil {
		return false
	}
	return ok
}

// rewriteModuleAssets 把方块内容里指向上游面板的资源地址改写为本站代理前缀，杜绝上游域名泄露。
// 以首个 /vendor/ 绝对地址的 origin 为准，仅改写该源；其余外部资源原样保留。

func rewriteModuleAssets(serviceID int64, content string) string {
	m := moduleAssetRE.FindStringSubmatch(content)
	if len(m) < 2 {
		return content
	}
	origin := m[1]
	prefix := moduleAssetPrefix(serviceID, origin)
	content = strings.ReplaceAll(content, origin+"/", prefix+"/")
	return strings.ReplaceAll(content, origin, prefix)
}

// serviceModuleOverview GET /services/{id}/module — 一次内嵌全部方块为标签页概览。
// 服务端循环拉取各 client_area 方块内容并汇总，用户无需逐个点击；表单统一改走本站 POST。

func (h *Pages) serviceModuleOverview(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	serviceID, _ := strconv.ParseInt(r.PathValue("serviceID"), 10, 64)
	sum, err := h.Console.ModuleSummary(r.Context(), userID, serviceID)
	if err != nil {
		http.Error(w, "模块清单拉取失败: "+err.Error(), http.StatusBadGateway)
		return
	}
	var blocks []moduleBlock
	for _, a := range sum.Areas {
		content, err := h.Console.ModulePageContent(r.Context(), userID, serviceID, a.Key)
		if err != nil {
			log.Printf("[module] service %d 拉取方块 %s 失败: %v", serviceID, a.Key, err)
			continue
		}
		blocks = append(blocks, moduleBlock{Key: a.Key, Name: a.Name, Content: rewriteModuleAssets(serviceID, content)})
	}
	if len(blocks) == 0 {
		http.Error(w, "该产品暂无可用功能模块", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	fmt.Fprint(w, moduleOverviewShell(serviceID, h.pageCSRF(w, r), blocks))
}

// serviceModulePage GET /services/{id}/module/{key} — 单个方块本地代理页（跳转直开时用）。

func (h *Pages) serviceModulePage(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	serviceID, _ := strconv.ParseInt(r.PathValue("serviceID"), 10, 64)
	key := r.PathValue("key")
	content, err := h.Console.ModulePageContent(r.Context(), userID, serviceID, key)
	if err != nil {
		http.Error(w, "模块页面拉取失败: "+err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	fmt.Fprint(w, moduleOverviewShell(serviceID, h.pageCSRF(w, r), []moduleBlock{
		{Key: key, Content: rewriteModuleAssets(serviceID, content)},
	}))
}

// moduleOverviewShell 组装概览页：标签导航 + 各方块内容 + 提交拦截脚本（本地 POST + CSRF）。
// 上游方块（快照/安全组/设置等）依赖宿主页的 jQuery/Bootstrap/SweetAlert2 与自定义 ajax()，
// 此处统一补齐：公共库从应用内嵌资源加载，ajax() 拦截改写上游绝对地址为本站模块端点，避免跨域与无凭据。
// ponytail: 多方块直接堆叠 DOM，若上游方块间存在同名 id/全局变量可能互相干扰；必要时改 iframe 隔离。

func moduleOverviewShell(serviceID int64, csrf string, blocks []moduleBlock) string {
	var sb strings.Builder
	sid := strconv.FormatInt(serviceID, 10)
	sb.WriteString("<!doctype html><html><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width,initial-scale=1\">")
	sb.WriteString(`<link rel="stylesheet" href="/assets/vendor/legacy-module/bootstrap-4.6.2.min.css">`)
	sb.WriteString(`<script src="/assets/vendor/legacy-module/jquery-3.6.4.min.js"></script>`)
	sb.WriteString(`<script src="/assets/vendor/legacy-module/bootstrap-4.6.2.bundle.min.js"></script>`)
	sb.WriteString(`<script src="/assets/vendor/legacy-module/sweetalert2-11.all.min.js"></script>`)
	sb.WriteString("<style>body{margin:0;font-family:system-ui,-apple-system,'Segoe UI',Roboto,sans-serif;color:#1a1a1a;background:#fff}#tabs{position:sticky;top:0;background:#fff;border-bottom:1px solid #e2e8f0;padding:8px 12px;display:flex;gap:6px;flex-wrap:wrap;z-index:1050}#tabs .tab{cursor:pointer;padding:6px 14px;border:1px solid #cbd5e1;border-radius:999px;background:#fff;color:#334155;font:inherit}#tabs .tab.on{background:#0e7490;color:#fff;border-color:#0e7490}.block{display:none;padding:16px}.block.on{display:block}button,input,select,textarea{font:inherit}.modal{z-index:2000}body.swal2-shown>. swal2-container{z-index:2100!important}</style></head><body>")
	if len(blocks) > 1 {
		sb.WriteString("<div id=\"tabs\">")
		for i, b := range blocks {
			name := b.Name
			if name == "" {
				name = b.Key
			}
			cls := ""
			if i == 0 {
				cls = " on"
			}
			fmt.Fprintf(&sb, "<button class=\"tab%s\" onclick=\"showTab(%d)\">%s</button>", cls, i, html.EscapeString(name))
		}
		sb.WriteString("</div>")
	}
	for i, b := range blocks {
		cls := ""
		if i == 0 {
			cls = " on"
		}
		fmt.Fprintf(&sb, "<section class=\"block%s\" data-key=%s>", cls, strconv.Quote(b.Key))
		sb.WriteString(b.Content)
		sb.WriteString("</section>")
	}
	fmt.Fprintf(&sb,
		`<script>(function(){var sid=%s,csrf=%s;
function showTab(n){document.querySelectorAll('.block').forEach(function(s,i){s.classList.toggle('on',i===n);});document.querySelectorAll('#tabs .tab').forEach(function(b,i){b.classList.toggle('on',i===n);});}window.showTab=showTab;
// SweetAlert2 v11 用 icon；上游方块 JS 传 type，做兼容映射
if(window.Swal){var _fire=Swal.fire.bind(Swal);Swal.fire=function(o){if(o&&o.type&&!o.icon){o.icon=o.type;}return _fire(o);};}
// ajax() 拦截：把上游绝对地址（…/provision/custom/<id>）改写为本站模块端点（ModuleAction 不区分 key），
// POST 表单与 JSON 两种 data 形态都支持，success 回调收到的保持上游 {status,msg} 形态。
window.ajax=function(opts){
  var url=opts.url||'';
  url=url.replace(/^https?:\/\/[^\/]+\/provision\/custom\/\d+.*/,'/services/'+sid+'/module/upstream');
  var body='';
  if(opts.data){
    if(typeof opts.data==='string'){body=opts.data;}
    else{var p=new URLSearchParams();Object.keys(opts.data).forEach(function(k){p.append(k,opts.data[k]);});body=p.toString();}
  }
  body+=(body?'&':'')+'_csrf='+encodeURIComponent(csrf);
  var xhr=new XMLHttpRequest();
  xhr.open((opts.type||'POST').toUpperCase(),url,true);
  xhr.setRequestHeader('Content-Type','application/x-www-form-urlencoded');
  xhr.onreadystatechange=function(){
    if(xhr.readyState!==4){return;}
    var data=null;
    try{data=JSON.parse(xhr.responseText);}catch(e){data={ok:0,msg:xhr.responseText||('http '+xhr.status)};}
    if(xhr.status>=400&&opts.error){opts.error(xhr);}else if(opts.success){opts.success(data);}
  };
  xhr.send(body);
};
document.querySelectorAll('form').forEach(function(f){var sec=f.closest('section');var key=sec?sec.getAttribute('data-key'):'';f.addEventListener('submit',function(ev){ev.preventDefault();var fd=new FormData(f);fd.set('_csrf',csrf);fetch('/services/'+sid+'/module/'+encodeURIComponent(key),{method:'POST',body:fd}).then(function(r){return r.json();}).then(function(j){if(j.ok){alert(j.msg||'操作成功');location.reload();}else{alert(j.msg||'操作失败');}}).catch(function(e){alert('网络错误:'+e);});});});})();</script>`,
		sid, jsString(csrf))
	sb.WriteString("</body></html>")
	return sb.String()
}

// serviceModuleAssets GET /services/{id}/module-assets/{host64}/{path...} — 代理上游面板静态资源。
// token 为 base64(origin)，文本类资源把原始 origin 改写回本站前缀，避免后续请求再泄露上游域名。

func (h *Pages) serviceModuleAssets(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	serviceID, _ := strconv.ParseInt(r.PathValue("serviceID"), 10, 64)
	owned, err := h.Svc.OwnsActive(r.Context(), serviceID, userID)
	if err != nil || !owned {
		http.NotFound(w, r)
		return
	}
	token := r.PathValue("host64")
	assetPath := r.PathValue("path")
	if token == "" || assetPath == "" || strings.Contains(assetPath, "..") {
		http.NotFound(w, r)
		return
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || !strings.HasPrefix(string(raw), "http://") && !strings.HasPrefix(string(raw), "https://") {
		http.NotFound(w, r)
		return
	}
	origin := string(raw)
	// SSRF 防护：origin 必须命中 servers 表中登记的上下游主机，禁止代理到任意地址。
	if h.ServersRepo == nil || !h.proxyOriginAllowed(r.Context(), origin) {
		http.NotFound(w, r)
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, origin+"/"+assetPath, nil)
	if err != nil {
		http.Error(w, "请求失败", 500)
		return
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := proxyClient.Do(req)
	if err != nil {
		http.Error(w, "资源代理失败", 502)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		http.Error(w, "资源不存在", http.StatusNotFound)
		return
	}
	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	if !isTextContent(ct) {
		io.Copy(w, resp.Body)
		return
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		io.Copy(w, resp.Body)
		return
	}
	prefix := moduleAssetPrefix(serviceID, origin)
	w.Write([]byte(strings.ReplaceAll(string(b), origin, prefix)))
}

// serviceModuleSubmit POST /services/{id}/module/{key} — 提交方块表单到上游，返回 JSON 结果。

func (h *Pages) serviceModuleSubmit(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	serviceID, _ := strconv.ParseInt(r.PathValue("serviceID"), 10, 64)
	key := r.PathValue("key")
	if strings.TrimSpace(key) == "" {
		writeJSON(w, map[string]any{"ok": 0, "msg": "模块不可用"})
		return
	}
	sum, err := h.Console.ModuleSummary(r.Context(), userID, serviceID)
	if err != nil || !moduleKeyInSummary(sum, key) {
		writeJSON(w, map[string]any{"ok": 0, "msg": "模块不可用"})
		return
	}
	if err := r.ParseForm(); err != nil {
		writeJSON(w, map[string]any{"ok": 0, "msg": "表单解析失败"})
		return
	}
	if fn := r.PostFormValue("func"); fn != "" && !moduleFunctionInSummary(sum, fn) {
		writeJSON(w, map[string]any{"ok": 0, "msg": "操作不可用"})
		return
	}
	raw, err := h.Console.ModuleAction(r.Context(), userID, serviceID, r.PostForm)
	if err != nil {
		writeJSON(w, map[string]any{"ok": 0, "msg": err.Error()})
		return
	}
	writeJSON(w, moduleResultJSON(raw))
}

func moduleKeyInSummary(sum server.ModuleSummary, key string) bool {
	for _, a := range sum.Areas {
		if a.Key == key {
			return true
		}
	}
	return false
}

func moduleFunctionInSummary(sum server.ModuleSummary, fn string) bool {
	for _, b := range sum.Buttons {
		if b.Function == fn {
			return true
		}
	}
	return false
}

func moduleResultJSON(raw string) map[string]any {
	out := map[string]any{"ok": 1, "msg": strings.TrimSpace(raw)}
	var m map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &m); err == nil {
		for k, v := range m {
			out[k] = v
		}
		ok := true
		if st, has := m["status"]; has {
			if v, isF := st.(float64); isF && v >= 400 {
				ok = false
			}
		}
		if s, has := m["msg"].(string); has {
			out["msg"] = s
		} else if s, has := m["message"].(string); has {
			out["msg"] = s
		}
		out["ok"] = ok
	}
	return out
}
