package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"cdnproxy/internal/proxy"
	"cdnproxy/internal/store"
)

type rulePayload struct {
	Name           string `json:"name"`
	TargetScheme   string `json:"target_scheme"`
	MatchHost      string `json:"match_host"`
	MatchPort      int    `json:"match_port"`
	UpstreamHost   string `json:"upstream_host"`
	UpstreamPort   int    `json:"upstream_port"`
	UpstreamScheme string `json:"upstream_scheme"`
	Enabled        *bool  `json:"enabled"`
	Remark         string `json:"remark"`
}

func (a *API) listRules(c *gin.Context) {
	page, pageSize := parsePagination(c)
	keyword := strings.TrimSpace(c.Query("keyword"))
	rules, total, err := a.store.ListRules(keyword, page, pageSize)
	if err != nil {
		a.logger.Printf("[api] 查询规则失败: %v", err)
		fail(c, http.StatusInternalServerError, "查询规则失败")
		return
	}
	c.JSON(http.StatusOK, listResponse(rules, total, page, pageSize))
}

func (a *API) getRule(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	rule, err := a.store.GetRule(id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			fail(c, http.StatusNotFound, "规则不存在")
		} else {
			a.logger.Printf("[api] 查询规则失败: %v", err)
			fail(c, http.StatusInternalServerError, "查询规则失败")
		}
		return
	}
	c.JSON(http.StatusOK, rule)
}

func (a *API) createRule(c *gin.Context) {
	var payload rulePayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		fail(c, http.StatusBadRequest, "请求格式不正确")
		return
	}
	rule := store.Rule{Enabled: true}
	if payload.Enabled != nil {
		rule.Enabled = *payload.Enabled
	}
	applyRulePayload(&rule, payload)
	store.NormalizeRule(&rule)
	if message := validateRule(rule); message != "" {
		fail(c, http.StatusBadRequest, message)
		return
	}
	if err := a.store.CreateRule(&rule); err != nil {
		if errors.Is(err, store.ErrDuplicate) {
			fail(c, http.StatusConflict, "相同代理配置、Host 和端口的规则已存在")
			return
		}
		a.logger.Printf("[api] 创建规则失败: %v", err)
		fail(c, http.StatusInternalServerError, "创建规则失败")
		return
	}
	if !a.reloadRules(c) {
		return
	}
	c.JSON(http.StatusCreated, rule)
}

func (a *API) updateRule(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	existing, err := a.store.GetRule(id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			fail(c, http.StatusNotFound, "规则不存在")
		} else {
			a.logger.Printf("[api] 查询规则失败: %v", err)
			fail(c, http.StatusInternalServerError, "查询规则失败")
		}
		return
	}
	var payload rulePayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		fail(c, http.StatusBadRequest, "请求格式不正确")
		return
	}
	rule := *existing
	if payload.Enabled != nil {
		rule.Enabled = *payload.Enabled
	}
	applyRulePayload(&rule, payload)
	store.NormalizeRule(&rule)
	if message := validateRule(rule); message != "" {
		fail(c, http.StatusBadRequest, message)
		return
	}
	if err := a.store.UpdateRule(&rule); err != nil {
		if errors.Is(err, store.ErrDuplicate) {
			fail(c, http.StatusConflict, "相同代理配置、Host 和端口的规则已存在")
			return
		}
		if errors.Is(err, store.ErrNotFound) {
			fail(c, http.StatusNotFound, "规则不存在")
			return
		}
		a.logger.Printf("[api] 更新规则失败: %v", err)
		fail(c, http.StatusInternalServerError, "更新规则失败")
		return
	}
	if !a.reloadRules(c) {
		return
	}
	c.JSON(http.StatusOK, rule)
}

func (a *API) setRuleEnabled(c *gin.Context) {
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
	if err := a.store.SetRuleEnabled(id, *payload.Enabled); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			fail(c, http.StatusNotFound, "规则不存在")
			return
		}
		a.logger.Printf("[api] 切换规则状态失败: %v", err)
		fail(c, http.StatusInternalServerError, "切换规则状态失败")
		return
	}
	if !a.reloadRules(c) {
		return
	}
	c.JSON(http.StatusOK, gin.H{"enabled": *payload.Enabled})
}

func (a *API) deleteRule(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	if err := a.store.DeleteRule(id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			fail(c, http.StatusNotFound, "规则不存在")
			return
		}
		a.logger.Printf("[api] 删除规则失败: %v", err)
		fail(c, http.StatusInternalServerError, "删除规则失败")
		return
	}
	if !a.reloadRules(c) {
		return
	}
	c.Status(http.StatusNoContent)
}

func applyRulePayload(rule *store.Rule, payload rulePayload) {
	rule.Name = payload.Name
	rule.TargetScheme = payload.TargetScheme
	rule.MatchHost = payload.MatchHost
	rule.MatchPort = payload.MatchPort
	rule.UpstreamHost = payload.UpstreamHost
	rule.UpstreamPort = payload.UpstreamPort
	rule.UpstreamScheme = payload.UpstreamScheme
	rule.Remark = payload.Remark
}

func validateRule(rule store.Rule) string {
	if rule.Name == "" {
		return "规则名称不能为空"
	}
	if len(rule.Name) > 64 {
		return "规则名称不能超过 64 个字符"
	}
	switch rule.TargetScheme {
	case store.SchemeHTTP, store.SchemeHTTPS, store.TargetSchemeBoth:
	default:
		return "代理配置仅支持 HTTP、HTTPS 或兼容"
	}
	if err := proxy.ValidateTargetHost(rule.MatchHost); err != nil {
		return "目标 Host 无效：" + err.Error()
	}
	if rule.MatchPort < 1 || rule.MatchPort > 65535 {
		return "目标端口需在 1-65535 之间"
	}
	if err := proxy.ValidateUpstreamHost(rule.UpstreamHost); err != nil {
		return "CDN 上游地址无效：" + err.Error()
	}
	switch rule.UpstreamScheme {
	case store.SchemeHTTP, store.SchemeHTTPS, store.UpstreamSchemeFollow:
	default:
		return "CDN 协议仅支持 HTTP、HTTPS 或跟随"
	}
	if rule.UpstreamPort < 1 || rule.UpstreamPort > 65535 {
		return "上游端口需在 1-65535 之间"
	}
	if len(rule.Remark) > 500 {
		return "备注不能超过 500 个字符"
	}
	return ""
}

func (a *API) reloadRules(c *gin.Context) bool {
	if err := a.proxy.ReloadRules(); err != nil {
		a.logger.Printf("[api] 刷新规则缓存失败: %v", err)
		fail(c, http.StatusInternalServerError, "规则已保存，但代理缓存刷新失败，请检查服务日志或重启服务")
		return false
	}
	return true
}
