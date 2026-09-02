package handler

import (
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"lumeidc/internal/middleware"
)

// ---------- VNC 服务端反向代理（隐藏上游域名） ----------

// vncConsole GET /services/{id}/console?do=vnc — 渲染本站 noVNC 页面。
// noVNC 库从本站静态资源代理加载，wss 走本站隧道，浏览器看不到任何上游地址。
// VNC 连接密码不在页面渲染时注入（会话 token 与密码绑定，拨号重试后会变），
// 改由 RFB credentialsrequired 时实时从 /vnc-pass 取当前会话密码；
// 实例登录密码稳定，注入页面供「粘贴密码」一键输入（对齐 ZJMF-CBAP）。

func (h *Pages) vncConsole(w http.ResponseWriter, r *http.Request, userID, serviceID int64) {
	if _, err := h.Console.VNCInfo(r.Context(), userID, serviceID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	pwd := ""
	if d, derr := h.Console.HostDetail(r.Context(), userID, serviceID); derr == nil {
		pwd = d.Password
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html><html lang="zh"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>VNC 控制台</title><link rel="stylesheet" href="/assets/css/lume.css">
<style>body{margin:0;font-family:system-ui,-apple-system,sans-serif;background:#111;color:#eee;height:100vh;display:flex;flex-direction:column}
#top{display:flex;align-items:center;gap:10px;padding:8px 14px;background:#1f2937;border-bottom:1px solid #374151;flex-wrap:wrap}
#top a{color:#818cf8;text-decoration:none;font-size:.85rem}
#status{font-size:.8rem;color:#9ca3af;flex:1}
#top button{background:#374151;color:#eee;border:1px solid #4b5563;border-radius:6px;padding:4px 12px;cursor:pointer;font-size:.8rem}
#screen{flex:1;overflow:hidden;background:#000}</style>
</head><body>
<div id="top" class="vnc-toolbar"><a href="/services/%d">← 返回实例</a><strong>VNC 控制台</strong><span id="status">连接中…</span>
<button onclick="location.reload()">重新连接</button>
<button onclick="sendCAD()">Ctrl+Alt+Del</button>
<button onclick="pasteText()">粘贴文本</button>
<button onclick="pastePwd()">粘贴密码</button>
<button onclick="pastePwdRetry()">反转重试</button></div>
<div id="screen"></div>
<script type="module">
import RFB from '/services/%d/vnc-assets/vendor/noVNC/core/rfb.js';
const screen=document.getElementById('screen'),statusEl=document.getElementById('status');
let rfb=null;
function status(t){statusEl.textContent=t;}
try{
  const wsProto=location.protocol==='https:'?'wss://':'ws://';
  rfb=new RFB(screen,wsProto+location.host+'/services/%d/vnc-ws');
  rfb.scaleViewport=true;
  rfb.addEventListener('connect',()=>status('已连接'));
  rfb.addEventListener('disconnect',e=>status(e.detail&&e.detail.clean?'连接已断开':'连接异常断开'));
  rfb.addEventListener('credentialsrequired',async()=>{
    status('获取会话密码…');
    try{
      const r=await fetch('/services/%d/vnc-pass');
      const j=await r.json();
      if(j.ok===1&&j.password){rfb.sendCredentials({password:j.password});status('认证中…');}
      else{status('获取密码失败');}
    }catch(e){status('获取密码失败');}
  });
  rfb.addEventListener('desktopname',e=>status(e.detail.name));
}catch(err){status('连接失败: '+err.message);}
window.sendCAD=function(){if(rfb)rfb.sendCtrlAltDel();};
// 逐键发送文本到控制台（QEMU 扩展键事件，物理键级输出）
let invertCase=false; // 目标机 CapsLock 状态未知：粘贴密码时若大小写反了点「反转重试」
window.pasteText=function(){
  const t=prompt("输入要发送到控制台的文本（不会发送回车键）");
  if(t&&rfb){rfb.focus();sendKeys(rfb,t);}
};
window.pastePwd=function(retry){
  const p=%s;
  if(!p){alert("未获取到实例密码");return;}
  if(!rfb)return;
  rfb.focus();
  const text=invertCase?swapCase(p):p;
  sendKeys(rfb,text);
  if(!retry){
    alert("密码已发送。若目标机显示的大小写相反（CapsLock 影响），请在目标机输入框全选删除后点「反转重试」。");
  }
};
window.pastePwdRetry=function(){
  invertCase=!invertCase;
  window.pastePwd(true);
};
function swapCase(s){return s.split('').map(c=>c>='a'&&c<='z'?c.toUpperCase():(c>='A'&&c<='Z'?c.toLowerCase():c)).join('');}
function sendKeys(rfb,t){
  // 键名映射：字符 -> [XT scancode 键名, 该键的基键 keysym, 是否需要 Shift]
  // 全部走 QEMU 扩展键事件（scancode+keysym），物理键级输出，不受目标机 CapsLock 状态影响。
  const KEYMAP={
    a:['KeyA',0x61],b:['KeyB',0x62],c:['KeyC',0x63],d:['KeyD',0x64],e:['KeyE',0x65],
    f:['KeyF',0x66],g:['KeyG',0x67],h:['KeyH',0x68],i:['KeyI',0x69],j:['KeyJ',0x6a],
    k:['KeyK',0x6b],l:['KeyL',0x6c],m:['KeyM',0x6d],n:['KeyN',0x6e],o:['KeyO',0x6f],
    p:['KeyP',0x70],q:['KeyQ',0x71],r:['KeyR',0x72],s:['KeyS',0x73],t:['KeyT',0x74],
    u:['KeyU',0x75],v:['KeyV',0x76],w:['KeyW',0x77],x:['KeyX',0x78],y:['KeyY',0x79],z:['KeyZ',0x7a],
    '1':['Digit1',0x31],'2':['Digit2',0x32],'3':['Digit3',0x33],'4':['Digit4',0x34],
    '5':['Digit5',0x35],'6':['Digit6',0x36],'7':['Digit7',0x37],'8':['Digit8',0x38],
    '9':['Digit9',0x39],'0':['Digit0',0x30],
    '!':['Digit1',0x21,1],'@':['Digit2',0x40,1],'#':['Digit3',0x23,1],'$':['Digit4',0x24,1],
    '%%':['Digit5',0x25,1],'^':['Digit6',0x5e,1],'&':['Digit7',0x26,1],'*':['Digit8',0x2a,1],
    '(':['Digit9',0x28,1],')':['Digit0',0x29,1],
    '-':['Minus',0x2d],'_':['Minus',0x5f,1],'=':['Equal',0x3d],'+':['Equal',0x2b,1],
    '[':['BracketLeft',0x5b],'{':['BracketLeft',0x7b,1],
    ']':['BracketRight',0x5d],'}':['BracketRight',0x7d,1],
    '\\\\':['Backslash',0x5c],'|':['Backslash',0x7c,1],
    ';':['Semicolon',0x3b],':':['Semicolon',0x3a,1],
    "'":['Quote',0x27],'"':['Quote',0x22,1],
    [String.fromCharCode(96)]:['Backquote',0x60],'~':['Backquote',0x7e,1],
    '.':['Period',0x2e],'>':['Period',0x3e,1],
    '/':['Slash',0x2f],'?':['Slash',0x3f,1]
  };
  const SHIFT=0xffe1;
  for(const ch of t){
    const lower=ch.toLowerCase();
    let entry=KEYMAP[lower];
    if(!entry){continue;}
    let [name,base,shift]=entry;
    let keysym=base;
    if(ch>='A'&&ch<='Z'){
      keysym=ch.charCodeAt();shift=1;
    } else if(KEYMAP[ch]&&KEYMAP[ch][2]){
      // 本身就是 shift 字符（如 '!'），直接用该条目
      [name,keysym,shift]=KEYMAP[ch];
    }
    if(shift){rfb.sendKey(SHIFT,'ShiftLeft',1);}
    rfb.sendKey(keysym,name,1);
    rfb.sendKey(keysym,name,0);
    if(shift){rfb.sendKey(SHIFT,'ShiftLeft',0);}
  }
}
</script>
</body></html>`, serviceID, serviceID, serviceID, serviceID, jsString(pwd))
}

// serviceVncPass GET /services/{id}/vnc-pass — 当前 VNC 会话密码（JSON，登录用户自己的服务）。

func (h *Pages) serviceVncPass(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("serviceID"), 10, 64)
	info, err := h.Console.VNCInfo(r.Context(), userID, id)
	if err != nil {
		jsonFail(w, err.Error())
		return
	}
	jsonOK(w, "password", info.Password)
}

// vncAssets GET /services/{id}/vnc-assets/{path...} — 代理上游 noVNC 静态资源。
// 静态资源来自 VNC 页面源站（AssetOrigin），非 API 源站，故通过 VNCInfo 获取。
// 文本类资源把上游源站改写为本站前缀，杜绝残留上游域名。

func (h *Pages) vncAssets(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	serviceID, _ := strconv.ParseInt(r.PathValue("serviceID"), 10, 64)
	info, err := h.Console.VNCInfo(r.Context(), userID, serviceID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	origin := info.AssetOrigin
	assetPath := r.PathValue("path")
	if assetPath == "" || strings.Contains(assetPath, "..") {
		http.NotFound(w, r)
		return
	}
	// 纵深防御：VNCInfo 返回的源站同样受 SSRF 白名单约束，仅放行已登记主机。
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
	req.Header.Set("Referer", origin+"/dcim/novnc")
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
	prefix := "/services/" + strconv.FormatInt(serviceID, 10) + "/vnc-assets"
	w.Write([]byte(strings.ReplaceAll(string(b), origin, prefix)))
}

// vncWebSocket GET /services/{id}/vnc-ws — 浏览器 wss 隧道，转发到上游真实 wss。
// 上游 wss 为自签证书（控制面同信任域）故跳过校验；地址与令牌仅存于服务端。

func (h *Pages) vncWebSocket(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	serviceID, _ := strconv.ParseInt(r.PathValue("serviceID"), 10, 64)
	info, err := h.Console.VNCInfo(r.Context(), userID, serviceID)
	if err != nil {
		log.Printf("[vnc] svc=%d VNCInfo 失败: %v", serviceID, err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// 透传浏览器请求的子协议（noVNC 要求 binary；上游不认子协议，拨上游时不带）
	up := websocket.Upgrader{
		// CSWSH 防护：仅允许同源（Origin 主机 == 请求 Host）建立隧道，阻断跨站脚本驱动 VNC。
		CheckOrigin: func(req *http.Request) bool {
			origin := req.Header.Get("Origin")
			if origin == "" {
				return true // 同源直连（无 Origin 头）放行
			}
			ou, err := url.Parse(origin)
			if err != nil {
				return false
			}
			return strings.EqualFold(ou.Host, req.Host)
		},
		Subprotocols: websocket.Subprotocols(r),
	}
	client, err := up.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[vnc] svc=%d 浏览器升级失败: %v", serviceID, err)
		return
	}
	defer client.Close()
	d := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	d.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // ponytail: 同上游控制面信任域；如需严格校验可改为受信 CA
	header := http.Header{}
	if info.AssetOrigin != "" {
		header.Set("Origin", info.AssetOrigin)
	}
	upstream, _, err := d.DialContext(r.Context(), info.WebSocketURL, header)
	if err != nil {
		// 上游 wss 偶发 bad handshake（token 会话互斥/瞬断）：清缓存重新拿会话重拨一次。
		log.Printf("[vnc] svc=%d 上游拨号失败(%v)，刷新会话重试", serviceID, err)
		h.Console.InvalidateVNC(r.Context(), userID, serviceID)
		if info2, err2 := h.Console.VNCInfo(r.Context(), userID, serviceID); err2 == nil {
			info = info2
			upstream, _, err = d.DialContext(r.Context(), info.WebSocketURL, header)
		}
	}
	if err != nil {
		log.Printf("[vnc] svc=%d 上游拨号失败: %v (ws=%s)", serviceID, err, info.WebSocketURL)
		client.Close()
		return
	}
	log.Printf("[vnc] svc=%d 上游拨号成功，隧道建立", serviceID)
	defer upstream.Close()
	defer client.Close()

	// 空闲超时：任一方向 2 分钟无数据即判定死连接，双向断开释放上游会话
	// （浏览器异常关闭可能是半开 TCP，ReadMessage 不会立刻感知，上游 VNC 连接会被占住）。
	const idleTimeout = 2 * time.Minute
	var lastActive int64 = time.Now().Unix()
	atomic.StoreInt64(&lastActive, time.Now().Unix())
	touch := func() { atomic.StoreInt64(&lastActive, time.Now().Unix()) }

	// 保活：每 30s 向浏览器发 ping（客户端 pong 会刷新读活跃；上游断开也能借此检测）
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	go func() {
		for range ticker.C {
			if time.Now().Unix()-atomic.LoadInt64(&lastActive) > int64(idleTimeout.Seconds()) {
				log.Printf("[vnc] svc=%d 空闲超时，主动断开隧道", serviceID)
				client.Close()
				upstream.Close()
				return
			}
			_ = client.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second))
		}
	}()

	var nUp, nDown int
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			_ = upstream.SetReadDeadline(time.Now().Add(idleTimeout))
			mt, data, err := upstream.ReadMessage()
			if err != nil {
				log.Printf("[vnc] svc=%d 上游读取结束(下发%d条): %v", serviceID, nUp, err)
				client.Close()
				return
			}
			touch()
			if err := client.WriteMessage(mt, data); err != nil {
				log.Printf("[vnc] svc=%d 写浏览器失败(下发%d条): %v", serviceID, nUp, err)
				return
			}
			nUp++
		}
	}()
	_ = client.SetReadDeadline(time.Now().Add(idleTimeout))
	client.SetPongHandler(func(string) error {
		touch()
		_ = client.SetReadDeadline(time.Now().Add(idleTimeout))
		return nil
	})
	for {
		mt, data, err := client.ReadMessage()
		if err != nil {
			log.Printf("[vnc] svc=%d 浏览器读取结束(上行%d条): %v", serviceID, nDown, err)
			// 浏览器已断开：立即关上游，让阻塞中的上游 ReadMessage 立刻返回释放连接
			// （否则上游空闲时 goroutine 会一直阻塞到读超时，期间上游 VNC 会话被占用）。
			upstream.Close()
			break
		}
		touch()
		_ = client.SetReadDeadline(time.Now().Add(idleTimeout))
		if err := upstream.WriteMessage(mt, data); err != nil {
			log.Printf("[vnc] svc=%d 写上游失败(上行%d条): %v", serviceID, nDown, err)
			break
		}
		nDown++
	}
	<-done
	log.Printf("[vnc] svc=%d 隧道关闭（上行%d条 下发%d条）", serviceID, nDown, nUp)
}

// isTextContent 判断资源是否可按文本改写（仅文本类做源站替换，避免破坏二进制）。

func isTextContent(ct string) bool {
	ct = strings.ToLower(ct)
	if strings.HasPrefix(ct, "text/") {
		return true
	}
	return strings.Contains(ct, "javascript") || strings.Contains(ct, "json") ||
		strings.Contains(ct, "xml") || strings.Contains(ct, "svg")
}

// jsString 生成安全的单引号 JS 字符串字面量（含 </ 防护）。

func jsString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, "</", `<\/`)
	return "'" + s + "'"
}

// ---------- 产品模块（魔方云 service module 通用能力：快照/安全组/NAT/共享建站等） ----------

// moduleBlock 一个上游客户端方块（key + 展示名 + 已改写的页面内容）。
