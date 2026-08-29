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

func main() {
	cfg, err := config.Load("config.yaml")
	if errors.Is(err, config.ErrNotInstalled) {
		mux := http.NewServeMux()
		inst := &handler.Installer{ConfigPath: "config.yaml"}
		inst.Register(mux)
		addr := listenAddr()
		srv := &http.Server{Addr: addr, Handler: mux}
		go func() {
			sig := make(chan os.Signal, 1)
			signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
			<-sig
			srv.Close()
		}()
		log.Printf("LumeIDC 未安装，安装向导已启动: http://localhost%s/install", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
		return
	}
	if err != nil {
		log.Fatalf("读取配置失败: %v", err)
	}
	app, err := httpserver.Build(cfg)
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
	log.Printf("LumeIDC 运行中: %s", cfg.BaseURL)
	if err := app.Server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
	log.Println("LumeIDC 已关闭")
}

func listenAddr() string {
	if a := os.Getenv("LISTEN"); a != "" {
		return a
	}
	return ":8080"
}
