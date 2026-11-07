package config

import (
	"flag"
	"os"
	"path/filepath"
)

// Config holds the runtime configuration loaded from flags and environment.
type Config struct {
	ProxyAddr  string
	DataDir    string
	WebDir     string
	AdminUser  string
	AdminPass  string
	DOHURL     string
	DOHSubnets string
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// Load parses command line flags and environment variables.
func Load() *Config {
	cfg := &Config{}

	flag.StringVar(&cfg.ProxyAddr, "proxy-addr", env("PROXY_ADDR", ":8088"),
		"HTTP 代理监听地址")
	flag.StringVar(&cfg.DataDir, "data-dir", env("DATA_DIR", "./data"),
		"数据目录（SQLite 与本地 CA）")
	flag.StringVar(&cfg.WebDir, "web-dir", env("WEB_DIR", ""),
		"前端构建产物目录（默认自动查找 ./web/dist 或 ../web/dist）")
	flag.StringVar(&cfg.DOHURL, "doh-url", env("DOH_URL", "https://doh.pub/dns-query"),
		"腾讯云 DoH JSON 接口地址")
	flag.StringVar(&cfg.DOHSubnets, "doh-region-subnets", env("DOH_REGION_SUBNETS", ""),
		"分区域 EDNS 子网列表（逗号分隔），为空使用内置列表")
	flag.Parse()

	cfg.AdminUser = env("ADMIN_USERNAME", "admin")
	cfg.AdminPass = env("ADMIN_PASSWORD", "admin123")
	cfg.WebDir = resolveWebDir(cfg.WebDir)
	return cfg
}

func resolveWebDir(configured string) string {
	if configured != "" {
		return configured
	}
	candidates := []string{"./web/dist", "../web/dist"}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			abs, err := filepath.Abs(candidate)
			if err == nil {
				return abs
			}
			return candidate
		}
	}
	abs, err := filepath.Abs("./web/dist")
	if err != nil {
		return "./web/dist"
	}
	return abs
}
