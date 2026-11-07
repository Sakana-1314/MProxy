package api

import (
	"fmt"
	stdlog "log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"cdnproxy/internal/proxy"
	"cdnproxy/internal/store"
	"cdnproxy/internal/token"
)

// Options configures the admin API.
type Options struct {
	Store     *store.Store
	Proxy     *proxy.Server
	WebDir    string
	ProxyAddr string
	Version   string
	Logger    *stdlog.Logger
}

// API serves the admin console API and the built Vue application.
type API struct {
	store     *store.Store
	proxy     *proxy.Server
	tokens    *token.Manager
	webDir    string
	proxyAddr string
	version   string
	startedAt time.Time
	logger    *stdlog.Logger
	limiter   *loginLimiter
	router    *gin.Engine
}

// New creates the API router.
func New(opts Options) (*API, error) {
	if os.Getenv(gin.EnvGinMode) == "" {
		gin.SetMode(gin.ReleaseMode)
	}
	secret, err := opts.Store.EnsureJWTSecret()
	if err != nil {
		return nil, fmt.Errorf("load JWT secret: %w", err)
	}
	a := &API{
		store:     opts.Store,
		proxy:     opts.Proxy,
		tokens:    token.New(secret, 12*time.Hour),
		webDir:    opts.WebDir,
		proxyAddr: opts.ProxyAddr,
		version:   opts.Version,
		startedAt: time.Now(),
		logger:    opts.Logger,
		limiter:   newLoginLimiter(),
	}
	if a.logger == nil {
		a.logger = stdlog.Default()
	}

	a.router = gin.New()
	a.router.SetTrustedProxies(nil)
	a.router.Use(gin.Recovery())
	a.router.Use(a.securityHeaders())
	a.router.Use(a.logFailures())

	a.routes()
	a.mountWeb()
	return a, nil
}

// Router returns the underlying Gin engine.
func (a *API) Router() *gin.Engine { return a.router }

func (a *API) routes() {
	api := a.router.Group("/api")
	api.Use(a.bodyLimit(1 << 20))
	api.Use(a.noStore())

	api.GET("/health", a.health)
	api.POST("/auth/login", a.login)

	protected := api.Group("")
	protected.Use(a.requireAdmin())
	protected.GET("/auth/me", a.me)
	protected.POST("/auth/password", a.changePassword)

	protected.GET("/dashboard", a.dashboard)

	protected.GET("/rules", a.listRules)
	protected.POST("/rules", a.createRule)
	protected.GET("/rules/:id", a.getRule)
	protected.PUT("/rules/:id", a.updateRule)
	protected.PATCH("/rules/:id/enabled", a.setRuleEnabled)
	protected.DELETE("/rules/:id", a.deleteRule)

	protected.GET("/users", a.listUsers)
	protected.POST("/users", a.createUser)
	protected.PUT("/users/:id", a.updateUser)
	protected.PATCH("/users/:id/enabled", a.setUserEnabled)
	protected.POST("/users/:id/quota", a.addUserQuota)
	protected.DELETE("/users/:id", a.deleteUser)

	protected.GET("/settings", a.getSettings)
}

func (a *API) mountWeb() {
	if a.webDir != "" {
		assetsDir := filepath.Join(a.webDir, "assets")
		if info, err := os.Stat(assetsDir); err == nil && info.IsDir() {
			a.router.Static("/assets", assetsDir)
		}
		logo := filepath.Join(a.webDir, "logo.png")
		if _, err := os.Stat(logo); err == nil {
			a.router.StaticFile("/logo.png", logo)
		}
		if _, err := os.Stat(filepath.Join(a.webDir, "index.html")); err != nil {
			a.logger.Printf("[api] 警告：未找到前端构建产物 %s，管理界面不可用", filepath.Join(a.webDir, "index.html"))
		}
	} else {
		a.logger.Printf("[api] 警告：未配置前端目录，管理界面不可用")
	}

	a.router.NoRoute(a.serveWeb)
}

func (a *API) serveWeb(c *gin.Context) {
	path := c.Request.URL.Path
	if path == "/api" || strings.HasPrefix(path, "/api/") {
		c.JSON(http.StatusNotFound, gin.H{"error": "接口不存在"})
		return
	}
	if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
		c.Status(http.StatusNotFound)
		return
	}
	index := filepath.Join(a.webDir, "index.html")
	if a.webDir == "" {
		c.String(http.StatusServiceUnavailable, "管理前端尚未构建。")
		return
	}
	if _, err := os.Stat(index); err != nil {
		c.String(http.StatusServiceUnavailable, "管理前端尚未构建，请在 web 目录执行 npm install && npm run build。")
		return
	}
	c.Header("Cache-Control", "no-cache")
	c.File(index)
}

func (a *API) health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"ok":             true,
		"version":        a.version,
		"uptime_seconds": int64(time.Since(a.startedAt).Seconds()),
		"time":           time.Now().Unix(),
	})
}

func (a *API) securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
		path := c.Request.URL.Path
		switch {
		case strings.HasPrefix(path, "/assets/"):
			h.Set("Cache-Control", "public, max-age=31536000, immutable")
		case strings.HasPrefix(path, "/api/"):
			h.Set("Cache-Control", "no-store")
		default:
			h.Set("Cache-Control", "no-cache")
		}
		c.Next()
	}
}

func (a *API) bodyLimit(maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		}
		c.Next()
	}
}

func (a *API) noStore() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Next()
	}
}

func (a *API) logFailures() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		status := c.Writer.Status()
		if status >= 400 {
			a.logger.Printf("[api] %s %s -> %d (%s)", c.Request.Method, c.Request.URL.Path, status,
				time.Since(start).Round(time.Millisecond))
		}
	}
}
