package proxy

import (
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"

	"cdnproxy/internal/store"
)

// Matcher holds an in-memory snapshot of enabled rules.
type Matcher struct {
	mu    sync.RWMutex
	rules []store.Rule
}

// NewMatcher creates an empty matcher.
func NewMatcher() *Matcher {
	return &Matcher{}
}

// Reload replaces the matcher snapshot.
func (m *Matcher) Reload(rules []store.Rule) {
	copied := make([]store.Rule, len(rules))
	copy(copied, rules)
	sort.SliceStable(copied, func(i, j int) bool { return copied[i].ID < copied[j].ID })
	m.mu.Lock()
	m.rules = copied
	m.mu.Unlock()
}

// Match returns the rule for a target scheme, host and port. An exact
// scheme match always wins over a "both" (兼容) rule on the same endpoint.
func (m *Matcher) Match(scheme, host string, port int) (store.Rule, bool) {
	scheme = strings.ToLower(strings.TrimSpace(scheme))
	host = NormalizeHost(host)
	if scheme == "" || host == "" {
		return store.Rule{}, false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	var compatible *store.Rule
	for i := range m.rules {
		rule := m.rules[i]
		if rule.MatchHost != host || rule.MatchPort != port {
			continue
		}
		if rule.TargetScheme == scheme {
			return rule, true
		}
		if rule.TargetScheme == store.TargetSchemeBoth && compatible == nil {
			matched := rule
			compatible = &matched
		}
	}
	if compatible != nil {
		return *compatible, true
	}
	return store.Rule{}, false
}

// Count returns the number of loaded rules.
func (m *Matcher) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.rules)
}

// NormalizeHost canonicalises a hostname for matching and logging.
func NormalizeHost(host string) string {
	host = strings.TrimSpace(strings.ToLower(host))
	host = strings.TrimSuffix(host, ".")
	host = strings.TrimPrefix(host, "[")
	host = strings.TrimSuffix(host, "]")
	return host
}

// ValidateTargetHost checks an exact target hostname or IP. Wildcards are not
// supported: every target must be explicitly allowlisted.
func ValidateTargetHost(host string) error {
	if host == "" {
		return fmt.Errorf("目标地址不能为空")
	}
	if len(host) > 253 {
		return fmt.Errorf("目标地址过长")
	}
	if strings.ContainsAny(host, "*/@?#: ") {
		if net.ParseIP(host) == nil {
			return fmt.Errorf("目标地址不能包含通配符、端口、路径或协议")
		}
	}
	if ip := net.ParseIP(host); ip != nil {
		return nil
	}
	return validateHostname(host)
}

// ValidateUpstreamHost checks an upstream hostname or IP.
func ValidateUpstreamHost(host string) error {
	if host == "" {
		return fmt.Errorf("转发地址不能为空")
	}
	if len(host) > 253 {
		return fmt.Errorf("转发地址过长")
	}
	if strings.ContainsAny(host, "*/@?#") {
		return fmt.Errorf("转发地址不能包含通配符、路径或协议")
	}
	if ip := net.ParseIP(host); ip != nil {
		return nil
	}
	return validateHostname(host)
}

func validateHostname(host string) error {
	if host == "" {
		return fmt.Errorf("域名不能为空")
	}
	if len(host) > 253 {
		return fmt.Errorf("域名过长")
	}
	labels := strings.Split(host, ".")
	for _, label := range labels {
		if label == "" {
			return fmt.Errorf("域名格式不正确")
		}
		if len(label) > 63 {
			return fmt.Errorf("域名标签过长")
		}
		for _, r := range label {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
				(r >= '0' && r <= '9') || r == '-' || r == '_' {
				continue
			}
			return fmt.Errorf("域名包含非法字符")
		}
		if strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return fmt.Errorf("域名标签不能以 - 开头或结尾")
		}
	}
	return nil
}
