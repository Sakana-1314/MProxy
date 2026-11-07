package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	stdlog "log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"cdnproxy/internal/proxy"
	"cdnproxy/internal/store"
)

func newTestAPI(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.SeedAdmin("admin", "admin123"); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	proxyServer, err := proxy.New(proxy.Options{
		Addr:    "127.0.0.1:0",
		DataDir: dir,
		Store:   st,
		Logger:  stdlog.New(io.Discard, "", 0),
	})
	if err != nil {
		t.Fatalf("new proxy: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = proxyServer.Shutdown(ctx)
	})

	apiServer, err := New(Options{
		Store:     st,
		Proxy:     proxyServer,
		WebDir:    "",
		ProxyAddr: "127.0.0.1:8088",
		Version:   "test",
		Logger:    stdlog.New(io.Discard, "", 0),
	})
	if err != nil {
		t.Fatalf("new api: %v", err)
	}
	server := httptest.NewServer(apiServer.Router())
	t.Cleanup(server.Close)
	return server, st
}

func request(t *testing.T, server *httptest.Server, method, path, token string, payload any) (*http.Response, []byte) {
	t.Helper()
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, server.URL+path, body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	return resp, data
}

func TestAdminAPIFlow(t *testing.T) {
	server, st := newTestAPI(t)

	resp, data := request(t, server, http.MethodPost, "/api/auth/login", "", map[string]string{
		"username": "admin",
		"password": "admin123",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d, body=%s", resp.StatusCode, data)
	}
	var login struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(data, &login); err != nil || login.Token == "" {
		t.Fatalf("invalid login response: %v %s", err, data)
	}
	token := login.Token

	resp, _ = request(t, server, http.MethodGet, "/api/rules", "", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated rules status = %d, want 401", resp.StatusCode)
	}

	resp, data = request(t, server, http.MethodPost, "/api/rules", token, map[string]any{
		"name":            "demo",
		"target_scheme":   "https",
		"match_host":      "API.Example.com",
		"match_port":      443,
		"upstream_host":   "127.0.0.1",
		"upstream_port":   443,
		"upstream_scheme": "https",
		"enabled":         true,
		"remark":          "demo rule",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create rule status = %d, body=%s", resp.StatusCode, data)
	}
	var rule store.Rule
	if err := json.Unmarshal(data, &rule); err != nil {
		t.Fatalf("decode rule: %v", err)
	}
	if rule.MatchHost != "api.example.com" {
		t.Fatalf("match_host = %q, want normalized api.example.com", rule.MatchHost)
	}
	if rule.UpstreamPort != 443 || !rule.Enabled {
		t.Fatalf("unexpected rule: %+v", rule)
	}

	resp, data = request(t, server, http.MethodGet, "/api/rules?page=1&page_size=10", token, nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(data), `"total":1`) {
		t.Fatalf("list rules failed: %d %s", resp.StatusCode, data)
	}

	resp, data = request(t, server, http.MethodPost, "/api/users", token, map[string]any{
		"username":    "alice",
		"password":    "password123",
		"enabled":     true,
		"quota_bytes": int64(1) << 30,
		"note":        "test user",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create user status = %d, body=%s", resp.StatusCode, data)
	}
	if strings.Contains(string(data), "password_hash") {
		t.Fatal("user response leaks password hash")
	}
	var user store.ProxyUser
	if err := json.Unmarshal(data, &user); err != nil {
		t.Fatalf("decode user: %v", err)
	}
	resp, data = request(t, server, http.MethodPatch, "/api/users/"+strconv.FormatInt(user.ID, 10)+"/enabled", token, map[string]bool{"enabled": false})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("disable user failed: %d %s", resp.StatusCode, data)
	}

	resp, data = request(t, server, http.MethodPost, "/api/rules", token, map[string]any{
		"name":            "wildcard must fail",
		"target_scheme":   "https",
		"match_host":      "*.example.com",
		"match_port":      443,
		"upstream_host":   "127.0.0.1",
		"upstream_port":   443,
		"upstream_scheme": "https",
		"enabled":         true,
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("wildcard rule status = %d, want 400", resp.StatusCode)
	}

	resp, data = request(t, server, http.MethodPost, "/api/users/"+strconv.FormatInt(user.ID, 10)+"/quota", token, map[string]int64{
		"add_bytes": 5 << 30,
	})
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(data), `"quota_bytes":6442450944`) {
		t.Fatalf("add quota failed: %d %s", resp.StatusCode, data)
	}

	localNow := time.Now().In(time.Local)
	bucket := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), localNow.Hour(), 0, 0, 0, time.Local).Unix()
	if err := st.FlushTraffic(
		map[int64]store.TrafficDelta{bucket: {Requests: 7, Bytes: 4096}},
		map[int64]int64{user.ID: 4096},
	); err != nil {
		t.Fatalf("flush traffic: %v", err)
	}

	resp, data = request(t, server, http.MethodGet, "/api/dashboard?range=24h", token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("dashboard failed: %d %s", resp.StatusCode, data)
	}
	var dashboard struct {
		Series  []dashboardPoint `json:"series"`
		Summary map[string]any   `json:"summary"`
	}
	if err := json.Unmarshal(data, &dashboard); err != nil {
		t.Fatalf("decode dashboard: %v", err)
	}
	if len(dashboard.Series) != 24 {
		t.Fatalf("series length = %d, want 24", len(dashboard.Series))
	}
	if dashboard.Summary["enabled_rules"].(float64) != 1 {
		t.Fatalf("enabled_rules = %v, want 1", dashboard.Summary["enabled_rules"])
	}
	if dashboard.Summary["today_requests"].(float64) != 7 || dashboard.Summary["today_bytes"].(float64) != 4096 {
		t.Fatalf("today totals = %+v", dashboard.Summary)
	}
	if dashboard.Summary["total_users"].(float64) != 1 {
		t.Fatalf("total_users = %v, want 1", dashboard.Summary["total_users"])
	}
	foundBucket := false
	for _, point := range dashboard.Series {
		if point.TS == bucket {
			foundBucket = true
			if point.Requests != 7 || point.Bytes != 4096 {
				t.Fatalf("bucket = %+v", point)
			}
		}
	}
	if !foundBucket {
		t.Fatalf("current hour bucket %d missing from series", bucket)
	}

	resp, data = request(t, server, http.MethodGet, "/api/settings", token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get settings failed: %d %s", resp.StatusCode, data)
	}
	if !strings.Contains(string(data), `"proxy_listen":"127.0.0.1:8088"`) {
		t.Fatalf("settings missing proxy_listen: %s", data)
	}
	for _, removed := range []string{"site_name", "allow_anonymous", "admin_listen", "ca_available", "public_proxy_addr"} {
		if strings.Contains(string(data), removed) {
			t.Fatalf("settings response still contains %q: %s", removed, data)
		}
	}
	resp, _ = request(t, server, http.MethodPut, "/api/settings", token, map[string]any{
		"site_name": "我的 CDN",
	})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("settings update route status = %d, want 404", resp.StatusCode)
	}
	resp, _ = request(t, server, http.MethodGet, "/api/public/info", "", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("public info route status = %d, want 404", resp.StatusCode)
	}

	resp, _ = request(t, server, http.MethodGet, "/api/public/ca.crt", "", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("CA download route status = %d, want 404", resp.StatusCode)
	}

	resp, _ = request(t, server, http.MethodPost, "/api/auth/login", "", map[string]string{
		"username": "admin",
		"password": "wrong-password",
	})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad login status = %d, want 401", resp.StatusCode)
	}
}

func TestRuleProtocolModes(t *testing.T) {
	server, _ := newTestAPI(t)
	resp, data := request(t, server, http.MethodPost, "/api/auth/login", "", map[string]string{
		"username": "admin",
		"password": "admin123",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d, body=%s", resp.StatusCode, data)
	}
	var login struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(data, &login); err != nil || login.Token == "" {
		t.Fatalf("invalid login response: %v %s", err, data)
	}

	resp, data = request(t, server, http.MethodPost, "/api/rules", login.Token, map[string]any{
		"name":            "dual",
		"target_scheme":   "both",
		"match_host":      "dual.example.com",
		"match_port":      0,
		"upstream_host":   "origin.example.com",
		"upstream_port":   0,
		"upstream_scheme": "follow",
		"enabled":         true,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create dual rule status = %d, body=%s", resp.StatusCode, data)
	}
	var rule store.Rule
	if err := json.Unmarshal(data, &rule); err != nil {
		t.Fatalf("decode rule: %v", err)
	}
	if rule.TargetScheme != "both" || rule.UpstreamScheme != "follow" {
		t.Fatalf("scheme modes = %q/%q", rule.TargetScheme, rule.UpstreamScheme)
	}
	if rule.MatchPort != 443 || rule.UpstreamPort != 443 {
		t.Fatalf("default ports = %d/%d, want 443/443", rule.MatchPort, rule.UpstreamPort)
	}

	resp, data = request(t, server, http.MethodPost, "/api/rules", login.Token, map[string]any{
		"name":            "plain follow",
		"target_scheme":   "http",
		"match_host":      "plain.example.com",
		"match_port":      0,
		"upstream_host":   "origin.example.com",
		"upstream_port":   0,
		"upstream_scheme": "follow",
		"enabled":         true,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create plain follow rule status = %d, body=%s", resp.StatusCode, data)
	}
	if err := json.Unmarshal(data, &rule); err != nil {
		t.Fatalf("decode plain rule: %v", err)
	}
	if rule.MatchPort != 80 || rule.UpstreamPort != 80 {
		t.Fatalf("plain follow ports = %d/%d, want 80/80", rule.MatchPort, rule.UpstreamPort)
	}

	resp, data = request(t, server, http.MethodPost, "/api/rules", login.Token, map[string]any{
		"name":            "bad target",
		"target_scheme":   "compatible",
		"match_host":      "bad-target.example.com",
		"match_port":      443,
		"upstream_host":   "origin.example.com",
		"upstream_port":   443,
		"upstream_scheme": "follow",
		"enabled":         true,
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid target scheme status = %d, body=%s", resp.StatusCode, data)
	}

	resp, data = request(t, server, http.MethodPost, "/api/rules", login.Token, map[string]any{
		"name":            "bad upstream",
		"target_scheme":   "https",
		"match_host":      "bad-upstream.example.com",
		"match_port":      443,
		"upstream_host":   "origin.example.com",
		"upstream_port":   443,
		"upstream_scheme": "auto",
		"enabled":         true,
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid upstream scheme status = %d, body=%s", resp.StatusCode, data)
	}
}
