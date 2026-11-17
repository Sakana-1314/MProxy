package api

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"cdnproxy/internal/proxy"
	"cdnproxy/internal/store"
)

// Rule IP resolution errors are sanitised: the management UI shows that a
// query failed without exposing the resolver's implementation details.
const ruleIPsFailureMessage = "解析失败，请稍后重试"

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
			fail(c, http.StatusConflict, "相同访问地址的规则已存在")
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
			fail(c, http.StatusConflict, "相同访问地址的规则已存在")
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

type ruleIPRegion struct {
	Subnet    string   `json:"subnet"`
	LatencyMS int64    `json:"latency_ms"`
	IPCount   int      `json:"ip_count"`
	Failed    bool     `json:"failed"`
	IPs       []string `json:"ips"`
}

type ruleIPsResponse struct {
	Scheme       string             `json:"scheme"`
	Host         string             `json:"host"`
	Port         int                `json:"port"`
	DurationMS   int64              `json:"duration_ms"`
	ResolvedAt   int64              `json:"resolved_at"`
	TTLSeconds   int                `json:"ttl_seconds"`
	RegionOK     int                `json:"region_ok"`
	RegionFailed int                `json:"region_failed"`
	Error        string             `json:"error,omitempty"`
	IPs          []proxy.ResolvedIP `json:"ips"`
	Regions      []ruleIPRegion     `json:"regions"`
}

func (a *API) getRuleIPs(c *gin.Context) {
	rule, ok := a.loadRule(c)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	report, err := a.proxy.RuleResolution(ctx, *rule)
	a.writeRuleIPs(c, report, err)
}

func (a *API) refreshRuleIPs(c *gin.Context) {
	rule, ok := a.loadRule(c)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 45*time.Second)
	defer cancel()
	report, err := a.proxy.RefreshRuleResolution(ctx, *rule)
	a.writeRuleIPs(c, report, err)
}

func (a *API) setRuleIPEnabled(c *gin.Context) {
	rule, ok := a.loadRule(c)
	if !ok {
		return
	}
	var payload struct {
		IP      string `json:"ip"`
		Enabled *bool  `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil || payload.Enabled == nil {
		fail(c, http.StatusBadRequest, "请求格式不正确")
		return
	}
	ip := strings.TrimSpace(payload.IP)
	if net.ParseIP(ip) == nil {
		fail(c, http.StatusBadRequest, "IP 地址无效")
		return
	}
	if err := a.proxy.SetRuleIPEnabled(*rule, ip, *payload.Enabled); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			fail(c, http.StatusNotFound, "IP 不存在")
			return
		}
		a.logger.Printf("[api] 更新 IP 状态失败: %v", err)
		fail(c, http.StatusInternalServerError, "更新 IP 状态失败")
		return
	}
	c.JSON(http.StatusOK, gin.H{"ip": ip, "enabled": *payload.Enabled})
}

func (a *API) loadRule(c *gin.Context) (*store.Rule, bool) {
	id, ok := parseID(c)
	if !ok {
		return nil, false
	}
	rule, err := a.store.GetRule(id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			fail(c, http.StatusNotFound, "规则不存在")
		} else {
			a.logger.Printf("[api] 查询规则失败: %v", err)
			fail(c, http.StatusInternalServerError, "查询规则失败")
		}
		return nil, false
	}
	return rule, true
}

func (a *API) writeRuleIPs(c *gin.Context, report *proxy.ResolutionReport, err error) {
	if report == nil {
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			a.logger.Printf("[api] 解析规则 IP 失败: %v", err)
		}
		fail(c, http.StatusBadGateway, ruleIPsFailureMessage)
		return
	}
	response := ruleIPsResponse{
		Scheme:       report.Scheme,
		Host:         report.Host,
		Port:         report.Port,
		DurationMS:   report.DurationMS,
		ResolvedAt:   report.ResolvedAt,
		TTLSeconds:   report.TTLSeconds,
		RegionOK:     report.RegionOK,
		RegionFailed: report.RegionFailed,
		IPs:          report.IPs,
		Regions:      make([]ruleIPRegion, 0, len(report.Regions)),
	}
	if response.IPs == nil {
		response.IPs = []proxy.ResolvedIP{}
	}
	if report.Error != "" || err != nil {
		response.Error = ruleIPsFailureMessage
	}
	for _, region := range report.Regions {
		ips := append([]string{}, region.IPs...)
		response.Regions = append(response.Regions, ruleIPRegion{
			Subnet:    region.Subnet,
			LatencyMS: region.LatencyMS,
			IPCount:   region.IPCount,
			Failed:    region.Error != "",
			IPs:       ips,
		})
	}
	c.JSON(http.StatusOK, response)
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
		return "访问协议仅支持 HTTP、HTTPS 或兼容"
	}
	if err := proxy.ValidateTargetHost(rule.MatchHost); err != nil {
		return "目标地址无效：" + err.Error()
	}
	if rule.MatchPort < 1 || rule.MatchPort > 65535 {
		return "目标端口需在 1-65535 之间"
	}
	if err := proxy.ValidateUpstreamHost(rule.UpstreamHost); err != nil {
		return "转发地址无效：" + err.Error()
	}
	switch rule.UpstreamScheme {
	case store.SchemeHTTP, store.SchemeHTTPS, store.UpstreamSchemeFollow:
	default:
		return "转发协议仅支持 HTTP、HTTPS 或跟随"
	}
	if rule.UpstreamPort < 1 || rule.UpstreamPort > 65535 {
		return "转发端口需在 1-65535 之间"
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
