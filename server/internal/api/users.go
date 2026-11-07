package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	"cdnproxy/internal/store"
)

type userPayload struct {
	Username   string `json:"username"`
	Password   string `json:"password"`
	Enabled    *bool  `json:"enabled"`
	QuotaBytes int64  `json:"quota_bytes"`
	ExpiresAt  *int64 `json:"expires_at"`
	Note       string `json:"note"`
}

func (a *API) listUsers(c *gin.Context) {
	page, pageSize := parsePagination(c)
	keyword := strings.TrimSpace(c.Query("keyword"))
	users, total, err := a.store.ListUsers(keyword, page, pageSize)
	if err != nil {
		a.logger.Printf("[api] 查询代理用户失败: %v", err)
		fail(c, http.StatusInternalServerError, "查询用户失败")
		return
	}
	c.JSON(http.StatusOK, listResponse(users, total, page, pageSize))
}

func (a *API) createUser(c *gin.Context) {
	var payload userPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		fail(c, http.StatusBadRequest, "请求格式不正确")
		return
	}
	payload.Username = strings.TrimSpace(payload.Username)
	if message := validateProxyUsername(payload.Username); message != "" {
		fail(c, http.StatusBadRequest, message)
		return
	}
	if message := validateProxyPassword(payload.Password); message != "" {
		fail(c, http.StatusBadRequest, message)
		return
	}
	if message := validateUserFields(payload.QuotaBytes, payload.Note); message != "" {
		fail(c, http.StatusBadRequest, message)
		return
	}

	user := store.ProxyUser{
		Username:   payload.Username,
		Enabled:    true,
		QuotaBytes: payload.QuotaBytes,
		ExpiresAt:  normalizeExpiry(payload.ExpiresAt),
		Note:       strings.TrimSpace(payload.Note),
	}
	if payload.Enabled != nil {
		user.Enabled = *payload.Enabled
	}
	if err := a.store.CreateUser(&user, payload.Password); err != nil {
		if errors.Is(err, store.ErrDuplicate) {
			fail(c, http.StatusConflict, "该用户名已存在")
			return
		}
		a.logger.Printf("[api] 创建代理用户失败: %v", err)
		fail(c, http.StatusInternalServerError, "创建用户失败")
		return
	}
	a.proxy.InvalidateAuthCache()
	c.JSON(http.StatusCreated, user)
}

func (a *API) updateUser(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	existing, err := a.store.GetUser(id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			fail(c, http.StatusNotFound, "用户不存在")
		} else {
			a.logger.Printf("[api] 查询代理用户失败: %v", err)
			fail(c, http.StatusInternalServerError, "查询用户失败")
		}
		return
	}
	var payload userPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		fail(c, http.StatusBadRequest, "请求格式不正确")
		return
	}
	payload.Username = strings.TrimSpace(payload.Username)
	if payload.Username != "" && payload.Username != existing.Username {
		fail(c, http.StatusBadRequest, "用户名不可修改")
		return
	}
	if message := validateUserFields(0, payload.Note); message != "" {
		fail(c, http.StatusBadRequest, message)
		return
	}
	if payload.Password != "" {
		if message := validateProxyPassword(payload.Password); message != "" {
			fail(c, http.StatusBadRequest, message)
			return
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(payload.Password), bcrypt.DefaultCost)
		if err != nil {
			a.logger.Printf("[api] 生成密码哈希失败: %v", err)
			fail(c, http.StatusInternalServerError, "更新用户失败")
			return
		}
		existing.PasswordHash = string(hash)
	}
	existing.ExpiresAt = normalizeExpiry(payload.ExpiresAt)
	existing.Note = strings.TrimSpace(payload.Note)
	if payload.Enabled != nil {
		existing.Enabled = *payload.Enabled
	}
	if err := a.store.UpdateUser(existing); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			fail(c, http.StatusNotFound, "用户不存在")
			return
		}
		a.logger.Printf("[api] 更新代理用户失败: %v", err)
		fail(c, http.StatusInternalServerError, "更新用户失败")
		return
	}
	a.proxy.InvalidateAuthCache()
	c.JSON(http.StatusOK, existing)
}

func (a *API) setUserEnabled(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	var payload struct {
		Enabled *bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil || payload.Enabled == nil {
		fail(c, http.StatusBadRequest, "缺少 enabled 参数")
		return
	}
	if err := a.store.SetUserEnabled(id, *payload.Enabled); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			fail(c, http.StatusNotFound, "用户不存在")
			return
		}
		a.logger.Printf("[api] 切换用户状态失败: %v", err)
		fail(c, http.StatusInternalServerError, "切换用户状态失败")
		return
	}
	a.proxy.InvalidateAuthCache()
	c.JSON(http.StatusOK, gin.H{"enabled": *payload.Enabled})
}

func (a *API) addUserQuota(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	var payload struct {
		AddBytes int64 `json:"add_bytes"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		fail(c, http.StatusBadRequest, "请求格式不正确")
		return
	}
	if payload.AddBytes <= 0 || payload.AddBytes > (1<<50) {
		fail(c, http.StatusBadRequest, "增加的配额必须大于 0")
		return
	}
	if err := a.store.AddUserQuota(id, payload.AddBytes); err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			fail(c, http.StatusNotFound, "用户不存在")
		case errors.Is(err, store.ErrUnlimitedQuota):
			fail(c, http.StatusBadRequest, "不限流量账号无需增加配额")
		default:
			a.logger.Printf("[api] 增加用户配额失败: %v", err)
			fail(c, http.StatusInternalServerError, "增加配额失败")
		}
		return
	}
	a.proxy.InvalidateAuthCache()
	user, err := a.store.GetUser(id)
	if err != nil {
		fail(c, http.StatusInternalServerError, "配额已更新，但读取用户失败")
		return
	}
	c.JSON(http.StatusOK, user)
}

func (a *API) deleteUser(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	if err := a.store.DeleteUser(id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			fail(c, http.StatusNotFound, "用户不存在")
			return
		}
		a.logger.Printf("[api] 删除代理用户失败: %v", err)
		fail(c, http.StatusInternalServerError, "删除用户失败")
		return
	}
	a.proxy.InvalidateAuthCache()
	c.Status(http.StatusNoContent)
}

func validateProxyUsername(username string) string {
	if len(username) < 2 || len(username) > 32 {
		return "用户名长度需为 2-32 个字符"
	}
	for _, r := range username {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			continue
		}
		return "用户名仅支持字母、数字、点、下划线和短横线"
	}
	return ""
}

func validateProxyPassword(password string) string {
	if len(password) < 8 || len(password) > 128 {
		return "密码长度需为 8-128 个字符"
	}
	return ""
}

func validateUserFields(quota int64, note string) string {
	if quota < 0 {
		return "流量配额不能为负数"
	}
	if quota > (1 << 50) {
		return "流量配额过大"
	}
	if len(strings.TrimSpace(note)) > 200 {
		return "备注不能超过 200 个字符"
	}
	return ""
}

func normalizeExpiry(value *int64) *int64 {
	if value == nil || *value <= 0 {
		return nil
	}
	v := *value
	return &v
}
