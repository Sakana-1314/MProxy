package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"cdnproxy/internal/api"
	"cdnproxy/internal/config"
	"cdnproxy/internal/proxy"
	"cdnproxy/internal/store"
)

var version = "1.0.0"

func main() {
	cfg := config.Load()
	logger := log.New(os.Stdout, "", log.LstdFlags)

	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		logger.Fatalf("创建数据目录失败: %v", err)
	}
	st, err := store.Open(filepath.Join(cfg.DataDir, "cdn-proxy.db"))
	if err != nil {
		logger.Fatalf("打开数据库失败: %v", err)
	}
	defer st.Close()

	created, err := st.SeedAdmin(cfg.AdminUser, cfg.AdminPass)
	if err != nil {
		logger.Fatalf("初始化管理员失败: %v", err)
	}
	if created {
		logger.Printf("================================================================")
		logger.Printf("已创建初始管理员账号: %s / %s", cfg.AdminUser, cfg.AdminPass)
		logger.Printf("请登录管理后台后立即修改密码。")
		logger.Printf("================================================================")
	}

	proxyServer, err := proxy.New(proxy.Options{
		Addr:       cfg.ProxyAddr,
		DataDir:    cfg.DataDir,
		Store:      st,
		Logger:     logger,
		DOHURL:     cfg.DOHURL,
		DOHSubnets: splitList(cfg.DOHSubnets),
	})
	if err != nil {
		logger.Fatalf("启动代理失败: %v", err)
	}
	apiServer, err := api.New(api.Options{
		Store:     st,
		Proxy:     proxyServer,
		WebDir:    cfg.WebDir,
		ProxyAddr: cfg.ProxyAddr,
		Version:   version,
		Logger:    logger,
	})
	if err != nil {
		logger.Fatalf("启动管理接口失败: %v", err)
	}

	proxyServer.SetAdminHandler(apiServer.Router())
	if err := proxyServer.Start(); err != nil {
		logger.Fatalf("端口监听失败: %v", err)
	}

	logger.Printf("[listen] 代理与管理后台共用 http://%s", proxyServer.Addr())
	logger.Printf("[proxy] HTTP 代理入口: http://%s", proxyServer.Addr())
	logger.Printf("[admin] 管理后台: http://%s", proxyServer.Addr())
	logger.Printf("[data] 数据目录: %s", cfg.DataDir)
	logger.Printf("[web] 前端目录: %s", cfg.WebDir)

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	received := <-signals
	logger.Printf("收到信号 %s，正在退出...", received)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := proxyServer.Shutdown(shutdownCtx); err != nil {
		logger.Printf("关闭服务失败: %v", err)
	}
	logger.Printf("已退出")
}

func splitList(raw string) []string {
	return strings.FieldsFunc(raw, func(r rune) bool {
		switch r {
		case ',', ' ', '\t', '\n', '\r':
			return true
		}
		return false
	})
}
