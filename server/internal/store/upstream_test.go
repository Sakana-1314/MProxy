package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestUpstreamPoolRegionsAndEnabledState(t *testing.T) {
	st := openTestStore(t)
	target := struct {
		scheme string
		host   string
		port   int
	}{"https", "upstream.test", 443}

	save := func(ips []UpstreamIP, regions []UpstreamRegion) {
		t.Helper()
		if err := st.SaveUpstreamResolution(&UpstreamResolution{
			Scheme:       target.scheme,
			Host:         target.host,
			Port:         target.port,
			DurationMS:   120,
			TTLSeconds:   60,
			RegionOK:     2,
			RegionFailed: 1,
			IPs:          ips,
			Regions:      regions,
		}); err != nil {
			t.Fatalf("save resolution: %v", err)
		}
	}

	save(
		[]UpstreamIP{
			{IP: "203.0.113.1", LatencyMS: 10, Reachable: true},
			{IP: "203.0.113.2", LatencyMS: 20, Reachable: true},
			{IP: "203.0.113.3", LatencyMS: -1, Reachable: false},
		},
		[]UpstreamRegion{
			{Subnet: "1.2.3.0/24", LatencyMS: 30, IPs: []string{"203.0.113.1", "203.0.113.2"}},
			{Subnet: "4.5.6.0/24", LatencyMS: 40, IPs: []string{"203.0.113.2", "203.0.113.3"}},
			{Subnet: "7.8.9.0/24", LatencyMS: 50, Error: "query failed"},
		},
	)

	loaded, err := st.LoadUpstreamResolution(target.scheme, target.host, target.port)
	if err != nil {
		t.Fatalf("load resolution: %v", err)
	}
	if loaded == nil || len(loaded.IPs) != 3 {
		t.Fatalf("loaded = %+v", loaded)
	}
	for _, item := range loaded.IPs {
		if !item.Enabled {
			t.Fatalf("new IP %s should be enabled", item.IP)
		}
	}
	if len(loaded.Regions) != 3 {
		t.Fatalf("regions = %+v", loaded.Regions)
	}
	regionIPs := map[string][]string{}
	for _, region := range loaded.Regions {
		regionIPs[region.Subnet] = region.IPs
	}
	if len(regionIPs["1.2.3.0/24"]) != 2 || regionIPs["1.2.3.0/24"][0] != "203.0.113.1" {
		t.Fatalf("region 1 ips = %+v", regionIPs["1.2.3.0/24"])
	}
	if len(regionIPs["4.5.6.0/24"]) != 2 || regionIPs["4.5.6.0/24"][1] != "203.0.113.3" {
		t.Fatalf("region 2 ips = %+v", regionIPs["4.5.6.0/24"])
	}
	if len(loaded.Regions[2].IPs) != 0 || loaded.Regions[2].Error != "query failed" {
		t.Fatalf("failed region = %+v", loaded.Regions[2])
	}

	if err := st.SetUpstreamIPEnabled(target.scheme, target.host, target.port, "203.0.113.2", false); err != nil {
		t.Fatalf("disable ip: %v", err)
	}

	// A later resolution drops .3. It must become inactive, while the disabled
	// .2 stays in the active pool with enabled = 0.
	save(
		[]UpstreamIP{
			{IP: "203.0.113.1", LatencyMS: 12, Reachable: true},
			{IP: "203.0.113.2", LatencyMS: 22, Reachable: true},
		},
		[]UpstreamRegion{
			{Subnet: "1.2.3.0/24", LatencyMS: 31, IPs: []string{"203.0.113.1", "203.0.113.2"}},
		},
	)
	loaded, err = st.LoadUpstreamResolution(target.scheme, target.host, target.port)
	if err != nil {
		t.Fatalf("load after refresh: %v", err)
	}
	if len(loaded.IPs) != 2 || len(loaded.Regions) != 1 {
		t.Fatalf("active pool = %+v", loaded)
	}
	enabled := map[string]bool{}
	for _, item := range loaded.IPs {
		enabled[item.IP] = item.Enabled
	}
	if enabled["203.0.113.1"] != true || enabled["203.0.113.2"] != false {
		t.Fatalf("enabled state after refresh = %+v", enabled)
	}

	var active int
	if err := st.db.QueryRow(
		`SELECT active FROM upstream_ips WHERE scheme = ? AND host = ? AND port = ? AND ip = ?`,
		target.scheme, target.host, target.port, "203.0.113.3",
	).Scan(&active); err != nil {
		t.Fatalf("query inactive row: %v", err)
	}
	if active != 0 {
		t.Fatalf("203.0.113.3 active = %d, want 0", active)
	}

	// A reappearing IP is active again but keeps the admin's disabled choice.
	save(
		[]UpstreamIP{
			{IP: "203.0.113.1", LatencyMS: 12, Reachable: true},
			{IP: "203.0.113.2", LatencyMS: 22, Reachable: true},
			{IP: "203.0.113.3", LatencyMS: 25, Reachable: true},
		},
		nil,
	)
	loaded, err = st.LoadUpstreamResolution(target.scheme, target.host, target.port)
	if err != nil {
		t.Fatalf("load after reappear: %v", err)
	}
	if len(loaded.IPs) != 3 || len(loaded.Regions) != 0 {
		t.Fatalf("pool after reappear = %+v", loaded)
	}
	for _, item := range loaded.IPs {
		if item.IP == "203.0.113.2" && item.Enabled {
			t.Fatal("reappearing IP lost its disabled state")
		}
		if item.IP == "203.0.113.3" && !item.Enabled {
			t.Fatal("reappearing IP should be enabled by default")
		}
	}

	if err := st.SetUpstreamIPEnabled(target.scheme, target.host, target.port, "203.0.113.99", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown ip error = %v, want ErrNotFound", err)
	}

	if missing, err := st.LoadUpstreamResolution("https", "missing.test", 443); err != nil || missing != nil {
		t.Fatalf("missing resolution = %+v, %v", missing, err)
	}
}

func TestStoreReadPoolIsReadOnly(t *testing.T) {
	st := openTestStore(t)
	if _, err := st.read.Exec(
		`INSERT INTO settings (key, value) VALUES ('read_only_probe', 'x')`,
	); err == nil {
		t.Fatal("read pool accepted a write")
	}
}

func TestUpstreamInactiveIPsArePrunedAfterRetention(t *testing.T) {
	st := openTestStore(t)
	target := struct {
		scheme string
		host   string
		port   int
	}{"https", "prune.test", 443}
	save := func(ips []UpstreamIP) {
		t.Helper()
		if err := st.SaveUpstreamResolution(&UpstreamResolution{
			Scheme:     target.scheme,
			Host:       target.host,
			Port:       target.port,
			ResolvedAt: nowUnix(),
			TTLSeconds: 60,
			IPs:        ips,
		}); err != nil {
			t.Fatalf("save resolution: %v", err)
		}
	}

	save([]UpstreamIP{
		{IP: "203.0.113.1", LatencyMS: 10, Reachable: true},
		{IP: "203.0.113.2", LatencyMS: 20, Reachable: true},
	})
	// Simulate an address that dropped out of DNS more than the retention
	// window ago; the next successful refresh must remove it.
	if _, err := st.db.Exec(
		`UPDATE upstream_ips SET active = 0, updated_at = ?
		 WHERE scheme = ? AND host = ? AND port = ? AND ip = ?`,
		nowUnix()-ipRetentionSeconds-1, target.scheme, target.host, target.port, "203.0.113.2",
	); err != nil {
		t.Fatalf("age inactive ip: %v", err)
	}
	save([]UpstreamIP{{IP: "203.0.113.1", LatencyMS: 11, Reachable: true}})

	var count int
	if err := st.db.QueryRow(
		`SELECT COUNT(*) FROM upstream_ips WHERE scheme = ? AND host = ? AND port = ? AND ip = ?`,
		target.scheme, target.host, target.port, "203.0.113.2",
	).Scan(&count); err != nil {
		t.Fatalf("count pruned ip: %v", err)
	}
	if count != 0 {
		t.Fatalf("inactive ip was not pruned, count = %d", count)
	}
}

func TestUpstreamIPMigrationPreservesPool(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open legacy database: %v", err)
	}
	legacy := []string{
		`CREATE TABLE upstream_resolutions (
			scheme TEXT NOT NULL,
			host TEXT NOT NULL COLLATE NOCASE,
			port INTEGER NOT NULL,
			duration_ms INTEGER NOT NULL DEFAULT 0,
			resolved_at INTEGER NOT NULL,
			ttl_seconds INTEGER NOT NULL DEFAULT 300,
			region_ok INTEGER NOT NULL DEFAULT 0,
			region_failed INTEGER NOT NULL DEFAULT 0,
			error TEXT NOT NULL DEFAULT '',
			updated_at INTEGER NOT NULL,
			PRIMARY KEY (scheme, host, port)
		)`,
		`CREATE TABLE upstream_ips (
			scheme TEXT NOT NULL,
			host TEXT NOT NULL COLLATE NOCASE,
			port INTEGER NOT NULL,
			ip TEXT NOT NULL,
			latency_ms INTEGER NOT NULL DEFAULT -1,
			reachable INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (scheme, host, port, ip)
		)`,
		`INSERT INTO upstream_resolutions
			(scheme, host, port, duration_ms, resolved_at, ttl_seconds, region_ok, region_failed, error, updated_at)
		 VALUES ('https', 'legacy.test', 443, 42, 1700000000, 300, 1, 0, '', 1700000000)`,
		`INSERT INTO upstream_ips (scheme, host, port, ip, latency_ms, reachable)
		 VALUES ('https', 'legacy.test', 443, '203.0.113.1', 10, 1)`,
		`INSERT INTO upstream_ips (scheme, host, port, ip, latency_ms, reachable)
		 VALUES ('https', 'legacy.test', 443, '203.0.113.2', -1, 0)`,
		`PRAGMA user_version = 10`,
	}
	for _, statement := range legacy {
		if _, err := db.Exec(statement); err != nil {
			_ = db.Close()
			t.Fatalf("seed legacy database: %v", err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy database: %v", err)
	}

	st, err := Open(path)
	if err != nil {
		t.Fatalf("migrate legacy database: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	loaded, err := st.LoadUpstreamResolution("https", "legacy.test", 443)
	if err != nil {
		t.Fatalf("load migrated pool: %v", err)
	}
	if loaded == nil || len(loaded.IPs) != 2 {
		t.Fatalf("migrated pool = %+v", loaded)
	}
	for _, item := range loaded.IPs {
		if !item.Enabled {
			t.Fatalf("migrated IP %s should be enabled", item.IP)
		}
	}
	if len(loaded.Regions) != 0 {
		t.Fatalf("legacy rows should have no regions: %+v", loaded.Regions)
	}
}
