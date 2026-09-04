package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"lumeidc/internal/config"
	"lumeidc/internal/handler"
	"lumeidc/internal/httpserver"
)

// version 由构建期 -ldflags 注入，与 git tag 对应；dev 为本地未打 tag 构建。
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
	inst := &handler.Installer{ConfigPath: "config.yaml"}
	inst.Register(mux)
	addr := installListenAddr()
	srv := &http.Server{Addr: addr, Handler: mux}
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		srv.Close()
	}()
	done := make(chan struct{})
	inst.OnInstalled = func(cfg *config.Config) {
		signal.Stop(sigCh) // 安装阶段信号监听已无用，防止吞掉正式运行后的 SIGTERM
		_ = srv.Close()
		// 完整应用在新协程中运行；main 等待其退出，避免进程随之结束。
		go func() {
			startApp(cfg, version)
			close(done)
		}()
	}
	log.Printf("LumeIDC 未安装，安装向导已启动: http://localhost%s/install", addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
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
	log.Printf("LumeIDC 运行中 (v%s)", version)
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
	return "127.0.0.1:8080"
}
