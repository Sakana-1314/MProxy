package proxy

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"cdnproxy/internal/store"
)

func upstreamAddress(rule store.Rule) string {
	return net.JoinHostPort(rule.UpstreamHost, strconv.Itoa(rule.UpstreamPort))
}

// effectiveUpstreamRule resolves "follow" to the protocol used by the client.
func effectiveUpstreamRule(rule store.Rule, targetScheme string) store.Rule {
	if rule.UpstreamScheme != store.UpstreamSchemeFollow {
		return rule
	}
	if targetScheme == store.SchemeHTTP {
		rule.UpstreamScheme = store.SchemeHTTP
	} else {
		rule.UpstreamScheme = store.SchemeHTTPS
	}
	return rule
}

// upstreamHostHeader always replaces the incoming Host with the CDN upstream
// host (adding the port only when it is not the scheme default).
func upstreamHostHeader(rule store.Rule) string {
	if isDefaultPort(rule.UpstreamScheme, rule.UpstreamPort) {
		return bracketHost(rule.UpstreamHost)
	}
	return net.JoinHostPort(rule.UpstreamHost, strconv.Itoa(rule.UpstreamPort))
}

func bracketHost(host string) string {
	if strings.Contains(host, ":") {
		return "[" + host + "]"
	}
	return host
}

func isDefaultPort(scheme string, port int) bool {
	return (scheme == "https" && port == 443) || (scheme == "http" && port == 80)
}

func upstreamTLSConfig(rule store.Rule) *tls.Config {
	return &tls.Config{
		MinVersion:         tls.VersionTLS12,
		ServerName:         rule.UpstreamHost,
		InsecureSkipVerify: true, // #nosec G402 -- upstream CDN certificates are intentionally not verified
	}
}

// transportFor returns a shared HTTP transport for a rule's upstream settings.
func (s *Server) transportFor(rule store.Rule) *http.Transport {
	key := fmt.Sprintf("%s|%s|%d", rule.UpstreamScheme, rule.UpstreamHost, rule.UpstreamPort)
	s.transportsMu.Lock()
	defer s.transportsMu.Unlock()
	if transport, ok := s.transports[key]; ok {
		return transport
	}
	target := upstreamTarget{
		Scheme: rule.UpstreamScheme,
		Host:   NormalizeHost(rule.UpstreamHost),
		Port:   rule.UpstreamPort,
	}
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return s.resolver.Dial(ctx, target)
		},
		ForceAttemptHTTP2: false,
		// A transparent proxy must not negotiate or decompress encodings on the
		// client's behalf: it would drop Content-Length, inflate counters and
		// could batch SSE data inside a gzip frame.
		DisableCompression: true,
		// Every upstream request opens a fresh connection so the dialer can
		// round-robin across all resolved IPs, not pin a keep-alive connection
		// to one CDN node.
		DisableKeepAlives:     true,
		MaxIdleConns:          128,
		MaxIdleConnsPerHost:   64,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
		// LLM upstreams may need a long time before the first byte (large
		// prompts, reasoning models). 60s would abort otherwise valid requests.
		ResponseHeaderTimeout: 5 * time.Minute,
	}
	if rule.UpstreamScheme == store.SchemeHTTPS {
		transport.TLSClientConfig = upstreamTLSConfig(rule)
	}
	s.transports[key] = transport
	return transport
}

// TLSInfo describes the upstream certificate observed during a rule test.
type TLSInfo struct {
	Subject  string   `json:"subject"`
	Issuer   string   `json:"issuer"`
	NotAfter int64    `json:"not_after"`
	DNSNames []string `json:"dns_names,omitempty"`
}

// TestResult is returned by the admin rule connectivity test.
type TestResult struct {
	OK         bool              `json:"ok"`
	Status     int               `json:"status"`
	LatencyMS  int64             `json:"latency_ms"`
	Upstream   string            `json:"upstream"`
	TLS        *TLSInfo          `json:"tls,omitempty"`
	Resolution *ResolutionReport `json:"resolution,omitempty"`
	Warning    string            `json:"warning,omitempty"`
	Error      string            `json:"error,omitempty"`
}

// TestRule performs a HEAD request against the rule's upstream.
func (s *Server) TestRule(ctx context.Context, rule store.Rule) TestResult {
	if rule.UpstreamScheme == "" {
		rule.UpstreamScheme = store.SchemeHTTPS
	}
	if rule.UpstreamPort == 0 {
		if rule.UpstreamScheme == store.SchemeHTTP ||
			(rule.UpstreamScheme == store.UpstreamSchemeFollow && rule.TargetScheme == store.SchemeHTTP) {
			rule.UpstreamPort = 80
		} else {
			rule.UpstreamPort = 443
		}
	}
	targetScheme := rule.TargetScheme
	if targetScheme == store.TargetSchemeBoth {
		targetScheme = store.SchemeHTTPS
	}
	rule = effectiveUpstreamRule(rule, targetScheme)
	result := TestResult{Upstream: fmt.Sprintf("%s://%s", rule.UpstreamScheme, upstreamAddress(rule))}
	dohTarget := upstreamTarget{
		Scheme: rule.UpstreamScheme,
		Host:   NormalizeHost(rule.UpstreamHost),
		Port:   rule.UpstreamPort,
	}
	resolution, resolveErr := s.resolver.Report(ctx, dohTarget)
	if resolution == nil {
		errorText := "upstream resolution failed"
		if resolveErr != nil {
			errorText = resolveErr.Error()
		}
		resolution = &ResolutionReport{
			Host:   dohTarget.Host,
			Scheme: dohTarget.Scheme,
			Port:   dohTarget.Port,
			Error:  errorText,
		}
	}
	result.Resolution = resolution
	if len(resolution.IPs) == 0 && resolution.Error != "" {
		result.Error = resolution.Error
		return result
	}

	target := rule.UpstreamScheme + "://" + upstreamAddress(rule) + "/"
	request, err := http.NewRequestWithContext(ctx, http.MethodHead, target, nil)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	request.Host = upstreamHostHeader(rule)
	request.Header.Set("User-Agent", "cdn-proxy-test/1.0")
	request.Header.Set("Accept", "*/*")

	client := &http.Client{
		Transport: s.transportFor(rule),
		Timeout:   20 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	start := time.Now()
	response, err := client.Do(request)
	result.LatencyMS = time.Since(start).Milliseconds()
	if err != nil {
		result.Error = err.Error()
		return result
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 32<<10))

	result.OK = true
	result.Status = response.StatusCode
	if response.StatusCode >= 400 {
		result.Warning = fmt.Sprintf("上游返回 HTTP %d，请检查 Host、协议或路径配置", response.StatusCode)
	}
	if response.TLS != nil && len(response.TLS.PeerCertificates) > 0 {
		cert := response.TLS.PeerCertificates[0]
		result.TLS = &TLSInfo{
			Subject:  cert.Subject.String(),
			Issuer:   cert.Issuer.String(),
			NotAfter: cert.NotAfter.Unix(),
			DNSNames: cert.DNSNames,
		}
	}
	return result
}
