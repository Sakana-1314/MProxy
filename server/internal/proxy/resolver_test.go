package proxy

import (
	"context"
	"encoding/json"
	"io"
	stdlog "log"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"cdnproxy/internal/store"
)

func newTestResolver(t *testing.T, dohURL string, subnets []string) *upstreamResolver {
	t.Helper()
	resolver := newUpstreamResolver(resolverOptions{
		DOHURL:  dohURL,
		Subnets: subnets,
		Logger:  stdlog.New(io.Discard, "", 0),
	})
	t.Cleanup(resolver.Close)
	return resolver
}

func TestResolverRegionalDoHAndLatency(t *testing.T) {
	var queries atomic.Int64
	dohServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries.Add(1)
		subnet := r.URL.Query().Get("edns_client_subnet")
		var answer []map[string]any
		switch subnet {
		case "1.2.3.0/24":
			answer = []map[string]any{
				{"type": 1, "TTL": 60, "data": "203.0.113.1"},
				{"type": 1, "TTL": 60, "data": "203.0.113.2"},
			}
		case "4.5.6.0/24":
			answer = []map[string]any{
				{"type": 1, "TTL": 90, "data": "203.0.113.2"},
				{"type": 1, "TTL": 90, "data": "203.0.113.3"},
			}
		default:
			http.Error(w, "unexpected subnet", 400)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"Status": 0, "Answer": answer})
	}))
	defer dohServer.Close()

	resolver := newTestResolver(t, dohServer.URL, []string{"1.2.3.0/24", "4.5.6.0/24"})
	resolver.probeFn = func(_ context.Context, _ upstreamTarget, ip string) (int64, bool) {
		switch ip {
		case "203.0.113.1":
			return 50, true
		case "203.0.113.2":
			return 10, true
		default:
			return -1, false
		}
	}

	report, err := resolver.Report(context.Background(), upstreamTarget{Scheme: "https", Host: "upstream.test", Port: 443})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(report.IPs) != 3 {
		t.Fatalf("ips = %+v, want 3 unique", report.IPs)
	}
	if report.IPs[0].IP != "203.0.113.2" || report.IPs[0].LatencyMS != 10 || !report.IPs[0].Reachable {
		t.Fatalf("first IP = %+v", report.IPs[0])
	}
	if report.IPs[1].IP != "203.0.113.1" || report.IPs[1].LatencyMS != 50 {
		t.Fatalf("second IP = %+v", report.IPs[1])
	}
	if report.IPs[2].IP != "203.0.113.3" || report.IPs[2].Reachable {
		t.Fatalf("third IP = %+v", report.IPs[2])
	}
	if report.TTLSeconds != 90 {
		t.Fatalf("ttl = %d, want 90", report.TTLSeconds)
	}
	if report.RegionOK != 2 || report.RegionFailed != 0 {
		t.Fatalf("regions = %+v", report)
	}
	if queries.Load() != 2 {
		t.Fatalf("DoH queries = %d, want 2", queries.Load())
	}

	// A fresh cache hit must not hit DoH again.
	if _, err := resolver.Report(context.Background(), upstreamTarget{Scheme: "https", Host: "upstream.test", Port: 443}); err != nil {
		t.Fatalf("cached report: %v", err)
	}
	if queries.Load() != 2 {
		t.Fatalf("cache miss caused a DoH query: %d", queries.Load())
	}
}

func TestResolverBoundsDoHConcurrency(t *testing.T) {
	const subnetCount = 24
	var inFlight atomic.Int64
	var maxInFlight atomic.Int64
	subnets := make([]string, subnetCount)
	for index := range subnets {
		subnets[index] = "10." + strconv.Itoa(index) + ".0.0/24"
	}

	dohServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			previous := maxInFlight.Load()
			if current <= previous || maxInFlight.CompareAndSwap(previous, current) {
				break
			}
		}
		time.Sleep(25 * time.Millisecond)
		subnet := r.URL.Query().Get("edns_client_subnet")
		ip := "198.51.100." + strconv.Itoa(len(subnet)%200+1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"Status": 0,
			"Answer": []map[string]any{{"type": 1, "TTL": 60, "data": ip}},
		})
	}))
	defer dohServer.Close()

	resolver := newTestResolver(t, dohServer.URL, subnets)
	resolver.probeFn = func(context.Context, upstreamTarget, string) (int64, bool) { return 1, true }
	if _, err := resolver.Report(context.Background(), upstreamTarget{Scheme: "https", Host: "upstream.test", Port: 443}); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if maxInFlight.Load() > dohQueryConcurrency {
		t.Fatalf("DoH concurrency = %d, limit %d", maxInFlight.Load(), dohQueryConcurrency)
	}
}

func TestResolverFailureAndFailureCache(t *testing.T) {
	var queries atomic.Int64
	dohServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		queries.Add(1)
		http.Error(w, "doh down", 500)
	}))
	defer dohServer.Close()

	resolver := newTestResolver(t, dohServer.URL, []string{"1.2.3.0/24", "4.5.6.0/24"})
	report, err := resolver.Report(context.Background(), upstreamTarget{Scheme: "https", Host: "upstream.test", Port: 443})
	if err == nil {
		t.Fatal("expected resolution error")
	}
	if report == nil || report.Error == "" || len(report.IPs) != 0 {
		t.Fatalf("report = %+v", report)
	}
	firstQueries := queries.Load()
	if firstQueries < 2 {
		t.Fatalf("queries = %d, want at least one per subnet", firstQueries)
	}
	// Failure entries are cached briefly so a broken DoH endpoint is not hit for
	// every new connection.
	if _, err := resolver.Report(context.Background(), upstreamTarget{Scheme: "https", Host: "upstream.test", Port: 443}); err == nil {
		t.Fatal("expected cached failure")
	}
	if queries.Load() != firstQueries {
		t.Fatalf("failure cache missed: queries %d -> %d", firstQueries, queries.Load())
	}
}

func TestResolverDirectIPSkipsDoH(t *testing.T) {
	resolver := newTestResolver(t, "http://127.0.0.1:1/dns-query", []string{"1.2.3.0/24"})
	var probed string
	resolver.probeFn = func(_ context.Context, _ upstreamTarget, ip string) (int64, bool) {
		probed = ip
		return 7, true
	}
	report, err := resolver.Report(context.Background(), upstreamTarget{Scheme: "https", Host: "127.0.0.1", Port: 8443})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if probed != "127.0.0.1" || len(report.IPs) != 1 || report.IPs[0].LatencyMS != 7 {
		t.Fatalf("report = %+v, probed=%q", report, probed)
	}
	if report.Error != "" {
		t.Fatalf("unexpected error: %s", report.Error)
	}
}

func TestResolverPersistsIPsAndLoadsFromDatabase(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "resolver.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	var queries atomic.Int64
	dohServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"Status": 0,
			"Answer": []map[string]any{
				{"type": 1, "TTL": 60, "data": "203.0.113.10"},
				{"type": 1, "TTL": 60, "data": "203.0.113.11"},
			},
		})
	}))
	defer dohServer.Close()

	newResolver := func() *upstreamResolver {
		r := newUpstreamResolver(resolverOptions{
			DOHURL:  dohServer.URL,
			Subnets: []string{"1.2.3.0/24", "4.5.6.0/24"},
			Logger:  stdlog.New(io.Discard, "", 0),
			Store:   st,
		})
		r.probeFn = func(_ context.Context, _ upstreamTarget, ip string) (int64, bool) {
			if ip == "203.0.113.10" {
				return 23, true
			}
			return 57, true
		}
		return r
	}

	first := newResolver()
	t.Cleanup(first.Close)
	target := upstreamTarget{Scheme: "https", Host: "upstream.test", Port: 443}
	report, err := first.Report(context.Background(), target)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(report.IPs) != 2 {
		t.Fatalf("ips = %+v", report.IPs)
	}

	saved, err := st.LoadUpstreamResolution(target.Scheme, target.Host, target.Port)
	if err != nil {
		t.Fatalf("load persisted: %v", err)
	}
	if saved == nil || len(saved.IPs) != 2 {
		t.Fatalf("persisted resolution = %+v", saved)
	}
	latencies := map[string]int64{}
	reachable := map[string]bool{}
	for _, item := range saved.IPs {
		latencies[item.IP] = item.LatencyMS
		reachable[item.IP] = item.Reachable
	}
	if latencies["203.0.113.10"] != 23 || latencies["203.0.113.11"] != 57 {
		t.Fatalf("persisted latencies = %+v", latencies)
	}
	if !reachable["203.0.113.10"] || !reachable["203.0.113.11"] {
		t.Fatalf("persisted reachable = %+v", reachable)
	}
	if saved.RegionOK != 2 || saved.TTLSeconds != 60 {
		t.Fatalf("persisted metadata = %+v", saved)
	}

	// A fresh resolver must use the database pool without querying DoH again.
	before := queries.Load()
	second := newResolver()
	t.Cleanup(second.Close)
	loaded, err := second.Report(context.Background(), target)
	if err != nil {
		t.Fatalf("report from database: %v", err)
	}
	if len(loaded.IPs) != 2 {
		t.Fatalf("loaded ips = %+v", loaded.IPs)
	}
	if queries.Load() != before {
		t.Fatalf("database pool was not used: DoH queries %d -> %d", before, queries.Load())
	}
}

func TestResolverDialRoundRobin(t *testing.T) {
	resolver := newTestResolver(t, "http://127.0.0.1:1/dns-query", []string{"1.2.3.0/24"})
	target := upstreamTarget{Scheme: "https", Host: "upstream.test", Port: 443}
	key := target.key()
	resolver.mu.Lock()
	resolver.cache[key] = &cachedResolution{
		report: ResolutionReport{
			Host:   target.Host,
			Scheme: target.Scheme,
			Port:   target.Port,
			IPs: []ResolvedIP{
				{IP: "203.0.113.1", LatencyMS: 50, Reachable: true},
				{IP: "203.0.113.2", LatencyMS: 10, Reachable: true},
				{IP: "203.0.113.3", LatencyMS: 30, Reachable: true},
			},
		},
		expiresAt: time.Now().Add(time.Hour),
	}
	resolver.mu.Unlock()

	var addresses []string
	resolver.dialFn = func(_ context.Context, _, address string) (net.Conn, error) {
		addresses = append(addresses, address)
		client, server := net.Pipe()
		_ = server.Close()
		return client, nil
	}

	for index := 0; index < 4; index++ {
		conn, err := resolver.Dial(context.Background(), target)
		if err != nil {
			t.Fatalf("dial %d: %v", index, err)
		}
		_ = conn.Close()
	}
	want := []string{"203.0.113.1", "203.0.113.2", "203.0.113.3", "203.0.113.1"}
	if len(addresses) != len(want) {
		t.Fatalf("addresses = %v", addresses)
	}
	for index, expected := range want {
		host, _, err := net.SplitHostPort(addresses[index])
		if err != nil {
			t.Fatalf("split %q: %v", addresses[index], err)
		}
		if host != expected {
			t.Fatalf("dial %d used %s, want %s (all: %v)", index, host, expected, addresses)
		}
	}
}
