package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	"cdnproxy/internal/store"
)

const (
	loginWindow      = 10 * time.Minute
	loginMaxFailures = 10
)

type loginWindowEntry struct {
	count       int
	windowStart time.Time
}

type loginLimiter struct {
	mu      sync.Mutex
	entries map[string]*loginWindowEntry
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{entries: make(map[string]*loginWindowEntry)}
}

func (l *loginLimiter) allowed(ip string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	entry, ok := l.entries[ip]
	if !ok {
		return true, 0
	}
	now := time.Now()
	if now.Sub(entry.windowStart) >= loginWindow {
		delete(l.entries, ip)
		return true, 0
	}
	if entry.count >= loginMaxFailures {
		return false, time.Until(entry.windowStart.Add(loginWindow))
	}
	return true, 0
}

func (l *loginLimiter) failure(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	entry, ok := l.entries[ip]
	if !ok || now.Sub(entry.windowStart) >= loginWindow {
		l.entries[ip] = &loginWindowEntry{count: 1, windowStart: now}
		// Opportunistic cleanup of stale entries.
		if len(l.entries) > 4096 {
			for key, item := range l.entries {
				if now.Sub(item.windowStart) >= loginWindow {
					delete(l.entries, key)
				}
			}
		}
		return
	}
	entry.count++
}

func (l *loginLimiter) success(ip string) {
	l.mu.Lock()
	delete(l.entries, ip)
	l.mu.Unlock()
}

func (a *API) login(c *gin.Context) {
	var payload struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		fail(c, http.StatusBadRequest, "请求格式不正确")
		return
	}
	payload.Username = strings.TrimSpace(payload.Username)
	if payload.Username == "" || payload.Password == "" ||
		len(payload.Username) > 64 || len(payload.Password) > 128 {
		fail(c, http.StatusBadRequest, "请输入用户名和密码")
		return
	}

	ip := c.ClientIP()
	if ok, retryAfter := a.limiter.allowed(ip); !ok {
		c.Header("Retry-After", strconv.Itoa(int(retryAfter.Seconds())+1))
		fail(c, http.StatusTooManyRequests, "登录尝试次数过多，请稍后再试")
		return
	}

	admin, err := a.store.GetAdminByUsername(payload.Username)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte(payload.Password)) != nil {
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			a.logger.Printf("[api] 查询管理员失败: %v", err)
		}
		a.limiter.failure(ip)
		fail(c, http.StatusUnauthorized, "用户名或密码错误")
		return
	}
	a.limiter.success(ip)

	signed, expires, err := a.tokens.Issue(admin.ID, admin.Username)
	if err != nil {
		a.logger.Printf("[api] 签发登录令牌失败: %v", err)
		fail(c, http.StatusInternalServerError, "登录失败")
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{
		"token":      signed,
		"expires_at": expires.Unix(),
		"admin": gin.H{
			"id":       admin.ID,
			"username": admin.Username,
		},
	})
}

func (a *API) requireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		const prefix = "Bearer "
		if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
			fail(c, http.StatusUnauthorized, "未登录或登录已过期")
			return
		}
		raw := strings.TrimSpace(header[len(prefix):])
		claims, err := a.tokens.Parse(raw)
		if err != nil {
			fail(c, http.StatusUnauthorized, "未登录或登录已过期")
			return
		}
		adminID, err := claims.AdminID()
		if err != nil {
			fail(c, http.StatusUnauthorized, "未登录或登录已过期")
			return
		}
		admin, err := a.store.GetAdminByID(adminID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				fail(c, http.StatusUnauthorized, "账号不存在，请重新登录")
			} else {
				a.logger.Printf("[api] 查询管理员失败: %v", err)
				fail(c, http.StatusInternalServerError, "服务器内部错误")
			}
			return
		}
		if claims.IssuedAt == nil || claims.IssuedAt.Time.Unix() < admin.PasswordChangedAt {
			fail(c, http.StatusUnauthorized, "登录状态已失效，请重新登录")
			return
		}
		if claims.Username != admin.Username {
			fail(c, http.StatusUnauthorized, "登录状态已失效，请重新登录")
			return
		}
		c.Set("admin", admin)
		c.Next()
	}
}

func (a *API) me(c *gin.Context) {
	admin := currentAdmin(c)
	if admin == nil {
		fail(c, http.StatusUnauthorized, "未登录或登录已过期")
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"id":       admin.ID,
		"username": admin.Username,
	})
}

func (a *API) changePassword(c *gin.Context) {
	admin := currentAdmin(c)
	if admin == nil {
		fail(c, http.StatusUnauthorized, "未登录或登录已过期")
		return
	}
	var payload struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		fail(c, http.StatusBadRequest, "请求格式不正确")
		return
	}
	if payload.OldPassword == "" || payload.NewPassword == "" {
		fail(c, http.StatusBadRequest, "原密码和新密码不能为空")
		return
	}
	if len(payload.NewPassword) < 8 || len(payload.NewPassword) > 128 {
		fail(c, http.StatusBadRequest, "新密码长度需为 8-128 个字符")
		return
	}
	if payload.OldPassword == payload.NewPassword {
		fail(c, http.StatusBadRequest, "新密码不能与原密码相同")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte(payload.OldPassword)) != nil {
		fail(c, http.StatusBadRequest, "原密码不正确")
		return
	}
	if err := a.store.UpdateAdminPassword(admin.ID, payload.NewPassword); err != nil {
		a.logger.Printf("[api] 修改管理员密码失败: %v", err)
		fail(c, http.StatusInternalServerError, "修改密码失败")
		return
	}
	c.Status(http.StatusNoContent)
}

func currentAdmin(c *gin.Context) *store.Admin {
	value, ok := c.Get("admin")
	if !ok {
		return nil
	}
	admin, _ := value.(*store.Admin)
	return admin
}
