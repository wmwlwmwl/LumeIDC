package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"lumeidc/internal/config"
	"lumeidc/internal/handler"
	"lumeidc/internal/httpserver"
	"lumeidc/internal/middleware"
)

// version 由构建期 -ldflags 注入（含 v 前缀的 git tag，如 v0.0.10），dev 为本地未打 tag 构建。
var version = "dev"

func main() {
	cfg, err := config.Load("config.yaml")
	if errors.Is(err, config.ErrNotInstalled) {
		runInstaller(version)
		return
	}
	if err != nil {
		log.Fatalf("读取配置失败: %v", err)
	}
	startApp(cfg, version)
}

// runInstaller 启动安装向导；config.yaml 写入成功后在同一进程内切换到完整应用，
// 免手动重启（安装向导与完整应用分属两套路由，切换是必需的）。
func runInstaller(version string) {
	mux := http.NewServeMux()
	handler.RegisterWebUI(mux) // 安装向导页由 SPA 承载（/app/* 资源 + index.html）
	inst := &handler.Installer{ConfigPath: "config.yaml"}
	inst.Register(mux) // POST /install（JSON）
	// 精确匹配优先于 RegisterWebUI 的 GET /{path...} 兜底。
	mux.HandleFunc("GET /install", handler.InstallPage)
	addr := installListenAddr()
	// 与正式服务同样设超时：安装向导同样对外监听，缺超时会被慢连接长期占用。
	// WriteTimeout 必须远大于正式服务：POST /install 会在**响应写出之前**跑完全部迁移、
	// 建管理员、写 config.yaml。这个流程不是幂等的——响应被超时切断后重试会撞
	// 「管理员已存在」而卡在半装状态，故给足 10 分钟（只用于兜住真正的挂死，
	// 防慢连接靠的是 ReadHeaderTimeout）。
	srv := &http.Server{
		Addr:              addr,
		Handler:           middleware.Recover(mux),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      10 * time.Minute,
		IdleTimeout:       120 * time.Second,
	}
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		srv.Close()
	}()
	done := make(chan struct{})
	var installed atomic.Bool
	inst.OnInstalled = func(cfg *config.Config) {
		signal.Stop(sigCh) // 安装阶段信号监听已无用，防止吞掉正式运行后的 SIGTERM
		installed.Store(true)
		go func() {
			// 先让"安装完成"页完整送达浏览器，再切关旧服务、启动完整应用。
			time.Sleep(800 * time.Millisecond)
			_ = srv.Close()
			startApp(cfg, version)
			close(done)
		}()
	}
	host := addr
	if strings.HasPrefix(host, ":") {
		host = "localhost" + host
	}
	log.Printf("LumeIDC 未安装，安装向导已启动: http://%s/install", host)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
	if !installed.Load() {
		// SIGINT/SIGTERM 中断安装向导：直接退出（done 只在安装完成切换路径关闭）
		return
	}
	<-done // 等待完整应用结束
}

// startApp 启动完整应用并阻塞，收到退出信号时优雅关闭。
func startApp(cfg *config.Config, version string) {
	app, err := httpserver.Build(cfg, version)
	if err != nil {
		log.Fatalf("启动失败: %v", err)
	}
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		<-sig
		log.Println("收到关闭信号，正在优雅关闭...")
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		app.Shutdown(ctx)
	}()
	log.Printf("LumeIDC 运行中 (%s)", version)
	if err := app.Server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
	// 端口被后台热切换（SwitchListen）后，初始服务退出不代表进程结束；
	// 统一等待 Stopped（Shutdown 或最终服务退出时关闭）。
	<-app.Stopped()
	log.Println("LumeIDC 已关闭")
}

func installListenAddr() string {
	if a := os.Getenv("INSTALL_LISTEN"); a != "" {
		return a
	}
	// ponytail: 安装向导默认监听全接口（:8080）而非回环，便于 1Panel/容器端口映射直达安装页；
	// 代价是装完以前向导对公网可见，门槛依赖有效数据库凭据（需提交正确库连接才能创建管理员）。
	return ":8080"
}
