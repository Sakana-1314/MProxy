package proxy

import (
	"context"
	"errors"
	"net"
	"strings"

	"cdnproxy/internal/store"
)

// ErrInvalidUpstreamTarget is returned when a rule cannot be mapped to a
// resolvable upstream endpoint.
var ErrInvalidUpstreamTarget = errors.New("rule has no valid upstream target")

// ruleUpstreamTarget maps a rule to the DoH target used for its upstream
// requests. "follow" resolves to the client-facing protocol; when the rule
// accepts both protocols the tunnel protocol (HTTPS) is used.
func ruleUpstreamTarget(rule store.Rule) (upstreamTarget, error) {
	scheme := strings.ToLower(strings.TrimSpace(rule.UpstreamScheme))
	if scheme != store.SchemeHTTP && scheme != store.SchemeHTTPS {
		if rule.TargetScheme == store.SchemeHTTP {
			scheme = store.SchemeHTTP
		} else {
			scheme = store.SchemeHTTPS
		}
	}
	host := NormalizeHost(rule.UpstreamHost)
	if host == "" || (net.ParseIP(host) == nil && validateHostname(host) != nil) {
		return upstreamTarget{}, ErrInvalidUpstreamTarget
	}
	port := rule.UpstreamPort
	if port <= 0 {
		if scheme == store.SchemeHTTP {
			port = 80
		} else {
			port = 443
		}
	}
	return upstreamTarget{Scheme: scheme, Host: host, Port: port}, nil
}

// RuleResolution returns the cached (or freshly resolved) regional IP pool for
// a rule's upstream.
func (s *Server) RuleResolution(ctx context.Context, rule store.Rule) (*ResolutionReport, error) {
	target, err := ruleUpstreamTarget(rule)
	if err != nil {
		return nil, err
	}
	return s.resolver.Report(ctx, target)
}

// RefreshRuleResolution forces a new DoH resolution for a rule's upstream.
func (s *Server) RefreshRuleResolution(ctx context.Context, rule store.Rule) (*ResolutionReport, error) {
	target, err := ruleUpstreamTarget(rule)
	if err != nil {
		return nil, err
	}
	return s.resolver.Refresh(ctx, target)
}

// SetRuleIPEnabled enables or disables one IP in a rule's upstream pool.
func (s *Server) SetRuleIPEnabled(rule store.Rule, ip string, enabled bool) error {
	ip = strings.TrimSpace(ip)
	if net.ParseIP(ip) == nil {
		return errors.New("invalid IP address")
	}
	target, err := ruleUpstreamTarget(rule)
	if err != nil {
		return err
	}
	return s.resolver.SetIPEnabled(target, ip, enabled)
}
