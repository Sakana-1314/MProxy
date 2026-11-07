package doh

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestQueryAParsesAAndTTL(t *testing.T) {
	var seenQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenQuery = r.URL.RawQuery
		if accept := r.Header.Get("Accept"); accept != "application/dns-json" {
			t.Errorf("accept = %q", accept)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"Status": 0,
			"Answer": []any{
				map[string]any{"type": 1, "TTL": 60, "data": "203.0.113.1"},
				map[string]any{"type": 1, "TTL": 120, "data": "203.0.113.2"},
				map[string]any{"type": 5, "TTL": 999, "data": "cname.example"},
			},
		})
	}))
	defer server.Close()

	ips, ttl, err := New(server.URL, 2*time.Second).QueryA(context.Background(), "cdn.example.com", "1.2.3.0/24")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(ips) != 2 || ips[0] != "203.0.113.1" || ips[1] != "203.0.113.2" {
		t.Fatalf("ips = %v", ips)
	}
	if ttl != 120 {
		t.Fatalf("ttl = %d, want 120", ttl)
	}
	if !strings.Contains(seenQuery, "edns_client_subnet=1.2.3.0/24") {
		t.Fatalf("subnet slash was encoded: %q", seenQuery)
	}
	if !strings.Contains(seenQuery, "type=A") || !strings.Contains(seenQuery, "name=cdn.example.com") {
		t.Fatalf("unexpected query: %q", seenQuery)
	}
}

func TestQueryAErrors(t *testing.T) {
	cases := map[string]http.HandlerFunc{
		"http 500": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) },
		"bad json": func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("not json")) },
		"dns status": func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"Status": 3})
		},
	}
	for name, handler := range cases {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(handler)
			defer server.Close()
			if _, _, err := New(server.URL, 2*time.Second).QueryA(context.Background(), "x.example", "1.2.3.0/24"); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestQueryAEmptyAnswer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"Status": 0, "Answer": []any{}})
	}))
	defer server.Close()
	ips, ttl, err := New(server.URL, 2*time.Second).QueryA(context.Background(), "x.example", "1.2.3.0/24")
	if err != nil || len(ips) != 0 || ttl != 0 {
		t.Fatalf("ips=%v ttl=%d err=%v", ips, ttl, err)
	}
}

func TestQuoteSubnet(t *testing.T) {
	cases := map[string]string{
		"1.2.3.0/24":    "1.2.3.0/24",
		"2001:db8::/32": "2001%3Adb8%3A%3A/32",
		"":              "",
	}
	for input, want := range cases {
		if got := quoteSubnet(input); got != want {
			t.Fatalf("quoteSubnet(%q) = %q, want %q", input, got, want)
		}
	}
}
