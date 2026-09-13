package handler

import (
	"crypto/tls"
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

// ---------- VNC 隧道（隐藏上游域名） ----------
//
// 控制台页面由前台 SPA 提供（/services/{id}/console，noVNC 随前端打包），
// 浏览器只与本文件的两个接口交互：wss 隧道与当前会话密码。上游 wss 地址与
// 令牌仅存于服务端，任何时候都不下发给浏览器。

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
