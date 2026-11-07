package proxy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	stdlog "log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cdnproxy/internal/store"
)

type testEnv struct {
	store  *store.Store
	server *Server
	dir    string
}

func newTestEnv(t *testing.T) *testEnv {
	return newTestEnvWithOptions(t, nil)
}

func newTestEnvWithOptions(t *testing.T, mutate func(*Options)) *testEnv {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	options := Options{
		Addr:               "127.0.0.1:0",
		DataDir:            dir,
		Store:              st,
		Logger:             stdlog.New(io.Discard, "", 0),
		StatsFlushInterval: 50 * time.Millisecond,
	}
	if mutate != nil {
		mutate(&options)
	}
	srv, err := New(options)
	if err != nil {
		t.Fatalf("new proxy: %v", err)
	}
	if err := srv.Start(); err != nil {
		t.Fatalf("start proxy: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})
	return &testEnv{store: st, server: srv, dir: dir}
}

func (e *testEnv) addRule(t *testing.T, rule store.Rule) store.Rule {
	t.Helper()
	rule.Enabled = true
	store.NormalizeRule(&rule)
	if err := e.store.CreateRule(&rule); err != nil {
		t.Fatalf("create rule: %v", err)
	}
	if err := e.server.ReloadRules(); err != nil {
		t.Fatalf("reload rules: %v", err)
	}
	return rule
}

func (e *testEnv) addUser(t *testing.T, username, password string, quota int64) *store.ProxyUser {
	t.Helper()
	user := &store.ProxyUser{
		Username:   username,
		Enabled:    true,
		QuotaBytes: quota,
		Note:       "test account",
	}
	if err := e.store.CreateUser(user, password); err != nil {
		t.Fatalf("create user: %v", err)
	}
	return user
}

func (e *testEnv) client(t *testing.T, username, password string, insecure bool, roots *x509.CertPool) *http.Client {
	t.Helper()
	proxyURL := &url.URL{Scheme: "http", Host: e.server.Addr()}
	if username != "" {
		proxyURL.User = url.UserPassword(username, password)
	}
	transport := &http.Transport{
		Proxy:             http.ProxyURL(proxyURL),
		DisableKeepAlives: true,
		ForceAttemptHTTP2: false,
	}
	if insecure {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // #nosec G402 -- test client
	} else if roots != nil {
		transport.TLSClientConfig = &tls.Config{RootCAs: roots}
	}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport, Timeout: 10 * time.Second}
}

func newUpstream(t *testing.T, useTLS bool) (host string, port int) {
	t.Helper()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Test-Upstream", "yes")
		sni := ""
		if r.TLS != nil {
			sni = r.TLS.ServerName
		}
		_, _ = fmt.Fprintf(w, "method=%s host=%s path=%s tls=%t sni=%s",
			r.Method, r.Host, r.URL.RequestURI(), r.TLS != nil, sni)
	})
	var server *httptest.Server
	if useTLS {
		server = httptest.NewTLSServer(handler)
	} else {
		server = httptest.NewServer(handler)
	}
	t.Cleanup(server.Close)

	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse upstream url: %v", err)
	}
	port, err = strconv.Atoi(parsed.Port())
	if err != nil {
		t.Fatalf("parse upstream port: %v", err)
	}
	return parsed.Hostname(), port
}

func doGet(t *testing.T, client *http.Client, target string) (int, string, error) {
	t.Helper()
	response, err := client.Get(target)
	if err != nil {
		return 0, "", err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return response.StatusCode, "", err
	}
	return response.StatusCode, string(body), nil
}

func waitFor(t *testing.T, timeout time.Duration, condition func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

func (e *testEnv) waitUsage(t *testing.T, userID int64, minimum int64) {
	t.Helper()
	ok := waitFor(t, 5*time.Second, func() bool {
		user, err := e.store.GetUser(userID)
		return err == nil && user.TotalBytes >= minimum
	})
	if !ok {
		t.Fatalf("user %d usage was not updated", userID)
	}
}

func TestConnectTLSReencrypt(t *testing.T) {
	env := newTestEnv(t)
	upstreamHost, upstreamPort := newUpstream(t, true)
	rule := env.addRule(t, store.Rule{
		Name:           "target",
		TargetScheme:   "https",
		MatchHost:      "target.test",
		MatchPort:      443,
		UpstreamHost:   upstreamHost,
		UpstreamPort:   upstreamPort,
		UpstreamScheme: "https",
	})
	user := env.addUser(t, "alice", "password123", 0)

	client := env.client(t, "alice", "password123", true, nil)
	status, body, err := doGet(t, client, "https://target.test/hello?x=1")
	if err != nil {
		t.Fatalf("request through proxy failed: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	expectedHost := net.JoinHostPort(upstreamHost, strconv.Itoa(upstreamPort))
	if !strings.Contains(body, "host="+expectedHost) {
		t.Fatalf("upstream host = %q, want %q", body, expectedHost)
	}
	if !strings.Contains(body, "path=/hello?x=1") {
		t.Fatalf("upstream path missing: %q", body)
	}
	if !strings.Contains(body, "tls=true") {
		t.Fatalf("upstream connection should use TLS: %q", body)
	}

	// If the client trusts the local CA, normal certificate verification works.
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(env.server.ca.CertPEM()) {
		t.Fatal("failed to load proxy CA")
	}
	caClient := env.client(t, "alice", "password123", false, roots)
	status, body, err = doGet(t, caClient, "https://target.test/ca-verified")
	if err != nil {
		t.Fatalf("request with trusted CA failed: %v", err)
	}
	if status != http.StatusOK || !strings.Contains(body, "path=/ca-verified") {
		t.Fatalf("unexpected response: status=%d body=%q", status, body)
	}

	env.waitUsage(t, user.ID, 1)
	_ = rule

	// The traffic counters are buffered in memory and flushed periodically.
	ok := waitFor(t, 5*time.Second, func() bool {
		totals, err := env.store.TrafficTotalsSince(time.Now().Add(-time.Hour).Unix())
		return err == nil && totals.Requests > 0 && totals.Bytes > 0
	})
	if !ok {
		t.Fatal("aggregated traffic stats were not flushed")
	}

	result := env.server.TestRule(context.Background(), rule)
	if !result.OK || result.Status != http.StatusOK {
		t.Fatalf("rule test failed: %+v", result)
	}
	if result.TLS == nil {
		t.Fatalf("rule test should report upstream TLS info: %+v", result)
	}
}

func TestAuthAndRuleMatching(t *testing.T) {
	env := newTestEnv(t)
	upstreamHost, upstreamPort := newUpstream(t, true)
	env.addRule(t, store.Rule{
		Name:           "target",
		TargetScheme:   "https",
		MatchHost:      "target.test",
		MatchPort:      443,
		UpstreamHost:   upstreamHost,
		UpstreamPort:   upstreamPort,
		UpstreamScheme: "https",
	})
	env.addUser(t, "alice", "password123", 0)

	noAuth := env.client(t, "", "", true, nil)
	if _, _, err := doGet(t, noAuth, "https://target.test/"); err == nil {
		t.Fatal("request without credentials should fail")
	}

	wrongAuth := env.client(t, "alice", "wrong-password", true, nil)
	if _, _, err := doGet(t, wrongAuth, "https://target.test/"); err == nil {
		t.Fatal("request with invalid credentials should fail")
	}

	good := env.client(t, "alice", "password123", true, nil)
	if status, _, err := doGet(t, good, "https://target.test/ok"); err != nil || status != http.StatusOK {
		t.Fatalf("authenticated request failed: status=%d err=%v", status, err)
	}

	if _, _, err := doGet(t, good, "https://blocked.test/"); err == nil {
		t.Fatal("request to a host without a matching rule should fail")
	}

	env.addRule(t, store.Rule{
		Name:           "exact subdomain",
		TargetScheme:   "https",
		MatchHost:      "api.wild.test",
		MatchPort:      443,
		UpstreamHost:   upstreamHost,
		UpstreamPort:   upstreamPort,
		UpstreamScheme: "https",
	})
	if status, _, err := doGet(t, good, "https://api.wild.test/"); err != nil || status != http.StatusOK {
		t.Fatalf("exact rule did not match: status=%d err=%v", status, err)
	}
	if _, _, err := doGet(t, good, "https://other.wild.test/"); err == nil {
		t.Fatal("wildcards must not be supported")
	}

}

func TestPlainHTTPForward(t *testing.T) {
	env := newTestEnv(t)
	upstreamHost, upstreamPort := newUpstream(t, false)
	rule := env.addRule(t, store.Rule{
		Name:           "plain",
		TargetScheme:   "http",
		MatchHost:      "plain.test",
		MatchPort:      80,
		UpstreamHost:   upstreamHost,
		UpstreamPort:   upstreamPort,
		UpstreamScheme: "http",
	})
	user := env.addUser(t, "bob", "password123", 0)

	client := env.client(t, "bob", "password123", true, nil)
	status, body, err := doGet(t, client, "http://plain.test/hello?q=1")
	if err != nil {
		t.Fatalf("plain HTTP forwarding failed: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	expectedHost := net.JoinHostPort(upstreamHost, strconv.Itoa(upstreamPort))
	if !strings.Contains(body, "host="+expectedHost) || !strings.Contains(body, "path=/hello?q=1") {
		t.Fatalf("unexpected upstream request: %q", body)
	}
	if !strings.Contains(body, "tls=false") {
		t.Fatalf("plain upstream should not use TLS: %q", body)
	}

	env.waitUsage(t, user.ID, 1)
	_ = rule
}

func TestQuotaEnforcement(t *testing.T) {
	env := newTestEnv(t)
	upstreamHost, upstreamPort := newUpstream(t, true)
	env.addRule(t, store.Rule{
		Name:           "target",
		TargetScheme:   "https",
		MatchHost:      "target.test",
		MatchPort:      443,
		UpstreamHost:   upstreamHost,
		UpstreamPort:   upstreamPort,
		UpstreamScheme: "https",
	})
	user := env.addUser(t, "quota", "password123", 1)

	client := env.client(t, "quota", "password123", true, nil)
	if status, _, err := doGet(t, client, "https://target.test/first"); err != nil || status != http.StatusOK {
		t.Fatalf("first request should succeed: status=%d err=%v", status, err)
	}
	env.waitUsage(t, user.ID, 1)

	if _, _, err := doGet(t, client, "https://target.test/second"); err == nil {
		t.Fatal("second request should be rejected because the quota is exhausted")
	}
}

func TestDoHResolutionAndHostReplacement(t *testing.T) {
	upstreamHost, upstreamPort := newUpstream(t, true)

	var dohQueries atomic.Int64
	dohServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dohQueries.Add(1)
		if name := r.URL.Query().Get("name"); name != "upstream.test" {
			t.Errorf("DoH name = %q", name)
		}
		if subnet := r.URL.Query().Get("edns_client_subnet"); !strings.Contains(subnet, "/") {
			t.Errorf("DoH subnet = %q", subnet)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"Status": 0,
			"Answer": []map[string]any{
				{"type": 1, "TTL": 60, "data": upstreamHost},
			},
		})
	}))
	t.Cleanup(dohServer.Close)

	env := newTestEnvWithOptions(t, func(options *Options) {
		options.DOHURL = dohServer.URL
		options.DOHSubnets = []string{"1.2.3.0/24", "4.5.6.0/24"}
	})
	rule := env.addRule(t, store.Rule{
		Name:           "doh",
		TargetScheme:   "https",
		MatchHost:      "target.test",
		MatchPort:      443,
		UpstreamHost:   "upstream.test",
		UpstreamPort:   upstreamPort,
		UpstreamScheme: "https",
	})
	env.addUser(t, "doh-user", "password123", 0)

	client := env.client(t, "doh-user", "password123", true, nil)
	status, body, err := doGet(t, client, "https://target.test/doh-check")
	if err != nil {
		t.Fatalf("request through DoH-resolved proxy failed: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	expectedHost := net.JoinHostPort("upstream.test", strconv.Itoa(upstreamPort))
	if !strings.Contains(body, "host="+expectedHost) {
		t.Fatalf("Host was not replaced: %q", body)
	}
	if !strings.Contains(body, "path=/doh-check") {
		t.Fatalf("path missing: %q", body)
	}
	if !strings.Contains(body, "sni=upstream.test") {
		t.Fatalf("TLS SNI was not the upstream host: %q", body)
	}
	if dohQueries.Load() == 0 {
		t.Fatal("DoH was not queried")
	}

	result := env.server.TestRule(context.Background(), rule)
	if !result.OK || result.Resolution == nil {
		t.Fatalf("test result = %+v", result)
	}
	if len(result.Resolution.IPs) == 0 || result.Resolution.IPs[0].IP != upstreamHost {
		t.Fatalf("resolved IPs = %+v", result.Resolution.IPs)
	}
	if result.Resolution.IPs[0].LatencyMS < 0 || !result.Resolution.IPs[0].Reachable {
		t.Fatalf("resolved latency = %+v", result.Resolution.IPs[0])
	}
}

func TestMatcherExactSchemeHostPort(t *testing.T) {
	matcher := NewMatcher()
	matcher.Reload([]store.Rule{
		{ID: 1, TargetScheme: "https", MatchHost: "api.example.com", MatchPort: 443},
		{ID: 2, TargetScheme: "http", MatchHost: "api.example.com", MatchPort: 80},
		{ID: 3, TargetScheme: "https", MatchHost: "api.example.com", MatchPort: 8443},
		{ID: 4, TargetScheme: "https", MatchHost: "*.example.com", MatchPort: 443},
		{ID: 5, TargetScheme: "both", MatchHost: "dual.example.com", MatchPort: 443},
		{ID: 6, TargetScheme: "https", MatchHost: "dual.example.com", MatchPort: 443},
	})
	cases := []struct {
		scheme string
		host   string
		port   int
		want   int64
		ok     bool
	}{
		{"https", "api.example.com", 443, 1, true},
		{"http", "api.example.com", 80, 2, true},
		{"https", "api.example.com", 8443, 3, true},
		{"https", "api.example.com", 80, 0, false},
		{"http", "api.example.com", 443, 0, false},
		{"https", "other.example.com", 443, 0, false},
		{"https", "example.com", 443, 0, false},
		{"http", "dual.example.com", 443, 5, true},
		{"https", "dual.example.com", 443, 6, true},
	}
	for _, tc := range cases {
		rule, ok := matcher.Match(tc.scheme, tc.host, tc.port)
		if ok != tc.ok || (ok && rule.ID != tc.want) {
			t.Fatalf("Match(%q,%q,%d) = (%d,%v), want (%d,%v)",
				tc.scheme, tc.host, tc.port, rule.ID, ok, tc.want, tc.ok)
		}
	}
}

func TestSharedPortRoutesOriginFormToAdmin(t *testing.T) {
	env := newTestEnv(t)
	var called atomic.Bool
	env.server.SetAdminHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/admin" {
			t.Errorf("admin path = %q", r.URL.Path)
		}
		called.Store(true)
		w.WriteHeader(http.StatusNoContent)
	}))

	recorder := httptest.NewRecorder()
	env.server.handle(recorder, httptest.NewRequest(http.MethodGet, "/admin", nil))
	if recorder.Code != http.StatusNoContent || !called.Load() {
		t.Fatalf("origin-form request was not routed to admin: code=%d called=%v", recorder.Code, called.Load())
	}

	recorder = httptest.NewRecorder()
	env.server.handle(recorder, httptest.NewRequest(http.MethodGet, "http://target.test/", nil))
	if recorder.Code != http.StatusProxyAuthRequired {
		t.Fatalf("absolute-form request should use the proxy: code=%d", recorder.Code)
	}
}

func TestCompatibleTargetAcceptsHTTPAndHTTPS(t *testing.T) {
	env := newTestEnv(t)
	upstreamHost, upstreamPort := newUpstream(t, true)
	env.addRule(t, store.Rule{
		Name:           "compatible",
		TargetScheme:   "both",
		MatchHost:      "dual.test",
		MatchPort:      443,
		UpstreamHost:   upstreamHost,
		UpstreamPort:   upstreamPort,
		UpstreamScheme: "https",
	})
	env.addUser(t, "dual", "password123", 0)
	client := env.client(t, "dual", "password123", true, nil)

	status, body, err := doGet(t, client, "http://dual.test:443/plain-client")
	if err != nil || status != http.StatusOK {
		t.Fatalf("HTTP client through compatible rule failed: status=%d err=%v", status, err)
	}
	if !strings.Contains(body, "path=/plain-client") || !strings.Contains(body, "tls=true") {
		t.Fatalf("unexpected upstream request for HTTP client: %q", body)
	}

	status, body, err = doGet(t, client, "https://dual.test/tls-client")
	if err != nil || status != http.StatusOK {
		t.Fatalf("HTTPS client through compatible rule failed: status=%d err=%v", status, err)
	}
	if !strings.Contains(body, "path=/tls-client") || !strings.Contains(body, "tls=true") {
		t.Fatalf("unexpected upstream request for HTTPS client: %q", body)
	}
}

func TestFollowUpstreamProtocol(t *testing.T) {
	env := newTestEnv(t)
	plainHost, plainPort := newUpstream(t, false)
	tlsHost, tlsPort := newUpstream(t, true)
	env.addRule(t, store.Rule{
		Name:           "follow-http",
		TargetScheme:   "http",
		MatchHost:      "follow-http.test",
		MatchPort:      80,
		UpstreamHost:   plainHost,
		UpstreamPort:   plainPort,
		UpstreamScheme: "follow",
	})
	env.addRule(t, store.Rule{
		Name:           "follow-https",
		TargetScheme:   "https",
		MatchHost:      "follow-https.test",
		MatchPort:      443,
		UpstreamHost:   tlsHost,
		UpstreamPort:   tlsPort,
		UpstreamScheme: "follow",
	})
	env.addUser(t, "follow", "password123", 0)
	client := env.client(t, "follow", "password123", true, nil)

	status, body, err := doGet(t, client, "http://follow-http.test/follow-plain")
	if err != nil || status != http.StatusOK {
		t.Fatalf("HTTP follow request failed: status=%d err=%v", status, err)
	}
	if !strings.Contains(body, "tls=false") || !strings.Contains(body, "path=/follow-plain") {
		t.Fatalf("follow HTTP upstream = %q", body)
	}

	status, body, err = doGet(t, client, "https://follow-https.test/follow-tls")
	if err != nil || status != http.StatusOK {
		t.Fatalf("HTTPS follow request failed: status=%d err=%v", status, err)
	}
	if !strings.Contains(body, "tls=true") || !strings.Contains(body, "path=/follow-tls") {
		t.Fatalf("follow HTTPS upstream = %q", body)
	}
}

func TestUpstreamSchemesForFollow(t *testing.T) {
	cases := []struct {
		name   string
		rule   store.Rule
		scheme []string
	}{
		{"both follow", store.Rule{TargetScheme: "both", UpstreamScheme: "follow"}, []string{"http", "https"}},
		{"http follow", store.Rule{TargetScheme: "http", UpstreamScheme: "follow"}, []string{"http"}},
		{"https follow", store.Rule{TargetScheme: "https", UpstreamScheme: "follow"}, []string{"https"}},
		{"fixed http", store.Rule{TargetScheme: "both", UpstreamScheme: "http"}, []string{"http"}},
		{"fixed https", store.Rule{TargetScheme: "http", UpstreamScheme: "https"}, []string{"https"}},
	}
	for _, tc := range cases {
		got := upstreamSchemes(tc.rule)
		if strings.Join(got, ",") != strings.Join(tc.scheme, ",") {
			t.Fatalf("%s: schemes = %v, want %v", tc.name, got, tc.scheme)
		}
	}
}

// TestStreamingSSE verifies that each server-sent event reaches the client
// before the upstream finishes the response. The upstream waits for the test
// to release the second event only after the client has observed the first
// one, so a buffering proxy would deadlock (and fail via timeout).
func TestStreamingSSE(t *testing.T) {
	cases := []struct {
		name      string
		target    string
		matchPort int
		useTLS    bool
	}{
		{name: "plain http", target: "http://stream.test/events", matchPort: 80, useTLS: false},
		{name: "connect tls", target: "https://stream.test/events", matchPort: 443, useTLS: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t)
			release := make(chan struct{})

			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("Cache-Control", "no-cache")
				flusher, ok := w.(http.Flusher)
				if !ok {
					t.Error("upstream writer is not a flusher")
					return
				}
				fmt.Fprint(w, "data: first\n\n")
				flusher.Flush()

				// Only send the second event once the client has received the
				// first, proving the proxy flushed it downstream.
				select {
				case <-release:
				case <-time.After(5 * time.Second):
					return
				}
				fmt.Fprint(w, "data: second\n\n")
				flusher.Flush()
			})
			var upstream *httptest.Server
			if tc.useTLS {
				upstream = httptest.NewTLSServer(handler)
			} else {
				upstream = httptest.NewServer(handler)
			}
			t.Cleanup(upstream.Close)

			parsed, err := url.Parse(upstream.URL)
			if err != nil {
				t.Fatalf("parse upstream url: %v", err)
			}
			upstreamPort, err := strconv.Atoi(parsed.Port())
			if err != nil {
				t.Fatalf("parse upstream port: %v", err)
			}
			scheme := "http"
			if tc.useTLS {
				scheme = "https"
			}
			env.addRule(t, store.Rule{
				Name:           "stream",
				TargetScheme:   scheme,
				MatchHost:      "stream.test",
				MatchPort:      tc.matchPort,
				UpstreamHost:   parsed.Hostname(),
				UpstreamPort:   upstreamPort,
				UpstreamScheme: scheme,
			})
			env.addUser(t, "streamer", "password123", 0)

			client := env.client(t, "streamer", "password123", true, nil)
			response, err := client.Get(tc.target)
			if err != nil {
				t.Fatalf("streaming request failed: %v", err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200", response.StatusCode)
			}

			buffer := make([]byte, len("data: first\n\n"))
			firstRead := make(chan error, 1)
			go func() {
				_, err := io.ReadFull(response.Body, buffer)
				firstRead <- err
			}()
			select {
			case err := <-firstRead:
				if err != nil {
					t.Fatalf("read first event: %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("first SSE event was buffered; proxy did not flush it immediately")
			}
			if got := string(buffer); got != "data: first\n\n" {
				t.Fatalf("first event = %q", got)
			}
			close(release)

			rest, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatalf("read remaining stream: %v", err)
			}
			if !strings.Contains(string(rest), "data: second") {
				t.Fatalf("second event missing: %q", rest)
			}
		})
	}
}
