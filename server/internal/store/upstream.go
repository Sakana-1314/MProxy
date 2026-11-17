package store

import (
	"database/sql"
	"errors"
	"strings"
)

// UpstreamIP is one DoH A record with its probe latency and the admin
// controlled enabled flag. Rows that the latest answer no longer contains stay
// in the table with active = 0 and are not returned as part of the pool.
type UpstreamIP struct {
	ID        int64  `json:"id"`
	IP        string `json:"ip"`
	LatencyMS int64  `json:"latency_ms"`
	Reachable bool   `json:"reachable"`
	Enabled   bool   `json:"enabled"`
}

// UpstreamRegion is the persisted answer of one regional DoH query.
type UpstreamRegion struct {
	Subnet    string   `json:"subnet"`
	LatencyMS int64    `json:"latency_ms"`
	IPCount   int      `json:"ip_count"`
	Error     string   `json:"error,omitempty"`
	IPs       []string `json:"ips"`
}

// UpstreamResolution is the persisted resolution of one upstream target.
type UpstreamResolution struct {
	Scheme       string           `json:"scheme"`
	Host         string           `json:"host"`
	Port         int              `json:"port"`
	DurationMS   int64            `json:"duration_ms"`
	ResolvedAt   int64            `json:"resolved_at"`
	TTLSeconds   int              `json:"ttl_seconds"`
	RegionOK     int              `json:"region_ok"`
	RegionFailed int              `json:"region_failed"`
	Error        string           `json:"error,omitempty"`
	IPs          []UpstreamIP     `json:"ips"`
	Regions      []UpstreamRegion `json:"regions,omitempty"`
}

func normalizeUpstream(key string) string {
	return strings.ToLower(strings.TrimSpace(key))
}

const (
	ipUpsertBatch     = 100
	regionInsertBatch = 50
	regionIPBatch     = 150
)

// SaveUpstreamResolution refreshes the persisted IP pool for a target. Only
// successful resolutions with at least one IP should be saved.
//
// IPs missing from the new answer are soft-deleted (active = 0) and never
// physically removed: DNS answers rotate, so an address can disappear for
// weeks and come back. Keeping the row preserves the admin's enabled choice
// and the address history across those rotations. Region answers are replaced
// as a whole because they describe the latest query only.
func (s *Store) SaveUpstreamResolution(res *UpstreamResolution) error {
	if res == nil {
		return errors.New("nil upstream resolution")
	}
	scheme := normalizeUpstream(res.Scheme)
	host := normalizeUpstream(res.Host)
	if scheme == "" || host == "" || res.Port <= 0 {
		return errors.New("invalid upstream target")
	}
	now := nowUnix()
	if res.ResolvedAt == 0 {
		res.ResolvedAt = now
	}
	if res.TTLSeconds <= 0 {
		res.TTLSeconds = 300
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(
		`INSERT INTO upstream_resolutions
			(scheme, host, port, duration_ms, resolved_at, ttl_seconds, region_ok, region_failed, error, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(scheme, host, port) DO UPDATE SET
			duration_ms = excluded.duration_ms,
			resolved_at = excluded.resolved_at,
			ttl_seconds = excluded.ttl_seconds,
			region_ok = excluded.region_ok,
			region_failed = excluded.region_failed,
			error = excluded.error,
			updated_at = excluded.updated_at`,
		scheme, host, res.Port, res.DurationMS, res.ResolvedAt, res.TTLSeconds,
		res.RegionOK, res.RegionFailed, res.Error, now,
	); err != nil {
		return err
	}

	// Deactivate only rows that are currently active; already inactive rows
	// keep their old updated_at as the last-seen timestamp.
	if _, err := tx.Exec(
		`UPDATE upstream_ips SET active = 0, updated_at = ?
		 WHERE scheme = ? AND host = ? AND port = ? AND active = 1`,
		now, scheme, host, res.Port,
	); err != nil {
		return err
	}
	if err := upsertUpstreamIPs(tx, scheme, host, res.Port, now, res.IPs); err != nil {
		return err
	}

	if _, err := tx.Exec(
		`DELETE FROM upstream_region_ips WHERE scheme = ? AND host = ? AND port = ?`,
		scheme, host, res.Port,
	); err != nil {
		return err
	}
	if _, err := tx.Exec(
		`DELETE FROM upstream_regions WHERE scheme = ? AND host = ? AND port = ?`,
		scheme, host, res.Port,
	); err != nil {
		return err
	}
	if err := insertUpstreamRegions(tx, scheme, host, res.Port, now, res.Regions); err != nil {
		return err
	}
	if err := insertUpstreamRegionIPs(tx, scheme, host, res.Port, res.Regions); err != nil {
		return err
	}
	return tx.Commit()
}

func upsertUpstreamIPs(tx *sql.Tx, scheme, host string, port int, now int64, items []UpstreamIP) error {
	seen := make(map[string]struct{}, len(items))
	filtered := make([]UpstreamIP, 0, len(items))
	for _, item := range items {
		item.IP = strings.TrimSpace(item.IP)
		if item.IP == "" {
			continue
		}
		if _, exists := seen[item.IP]; exists {
			continue
		}
		seen[item.IP] = struct{}{}
		filtered = append(filtered, item)
	}
	for start := 0; start < len(filtered); start += ipUpsertBatch {
		end := min(start+ipUpsertBatch, len(filtered))
		statement := `INSERT INTO upstream_ips
			(scheme, host, port, ip, latency_ms, reachable, enabled, active, updated_at)
		 VALUES ` + ipValuePlaceholders(end-start) + `
		 ON CONFLICT(scheme, host, port, ip) DO UPDATE SET
			latency_ms = excluded.latency_ms,
			reachable = excluded.reachable,
			active = 1,
			updated_at = excluded.updated_at`
		args := make([]any, 0, (end-start)*7)
		for _, item := range filtered[start:end] {
			args = append(args, scheme, host, port, item.IP, item.LatencyMS, boolInt(item.Reachable), now)
		}
		if _, err := tx.Exec(statement, args...); err != nil {
			return err
		}
	}
	return nil
}

type regionRow struct {
	subnet    string
	latencyMS int64
	error     string
	ips       []string
}

func prepareRegionRows(regions []UpstreamRegion) []regionRow {
	index := make(map[string]int, len(regions))
	rows := make([]regionRow, 0, len(regions))
	for _, region := range regions {
		subnet := strings.TrimSpace(region.Subnet)
		if subnet == "" {
			continue
		}
		ips := dedupeIPStrings(region.IPs)
		if position, exists := index[subnet]; exists {
			rows[position] = regionRow{subnet: subnet, latencyMS: region.LatencyMS, error: region.Error, ips: ips}
			continue
		}
		index[subnet] = len(rows)
		rows = append(rows, regionRow{subnet: subnet, latencyMS: region.LatencyMS, error: region.Error, ips: ips})
	}
	return rows
}

func insertUpstreamRegions(tx *sql.Tx, scheme, host string, port int, now int64, regions []UpstreamRegion) error {
	rows := prepareRegionRows(regions)
	for start := 0; start < len(rows); start += regionInsertBatch {
		end := min(start+regionInsertBatch, len(rows))
		statement := `INSERT INTO upstream_regions
			(scheme, host, port, subnet, latency_ms, ip_count, error, updated_at)
		 VALUES ` + valuePlaceholders(end-start, 8)
		args := make([]any, 0, (end-start)*8)
		for _, row := range rows[start:end] {
			args = append(args, scheme, host, port, row.subnet, row.latencyMS, len(row.ips), row.error, now)
		}
		if _, err := tx.Exec(statement, args...); err != nil {
			return err
		}
	}
	return nil
}

func insertUpstreamRegionIPs(tx *sql.Tx, scheme, host string, port int, regions []UpstreamRegion) error {
	rows := prepareRegionRows(regions)
	type regionIP struct {
		subnet string
		ip     string
	}
	pairs := make([]regionIP, 0, len(rows)*3)
	for _, row := range rows {
		for _, ip := range row.ips {
			pairs = append(pairs, regionIP{subnet: row.subnet, ip: ip})
		}
	}
	for start := 0; start < len(pairs); start += regionIPBatch {
		end := min(start+regionIPBatch, len(pairs))
		statement := `INSERT OR IGNORE INTO upstream_region_ips
			(scheme, host, port, subnet, ip)
		 VALUES ` + valuePlaceholders(end-start, 5)
		args := make([]any, 0, (end-start)*5)
		for _, pair := range pairs[start:end] {
			args = append(args, scheme, host, port, pair.subnet, pair.ip)
		}
		if _, err := tx.Exec(statement, args...); err != nil {
			return err
		}
	}
	return nil
}

func valuePlaceholders(rows, columns int) string {
	row := "(" + strings.TrimSuffix(strings.Repeat("?,", columns), ",") + ")"
	return strings.TrimSuffix(strings.Repeat(row+",", rows), ",")
}

func ipValuePlaceholders(rows int) string {
	row := "(?, ?, ?, ?, ?, ?, 1, 1, ?)"
	return strings.TrimSuffix(strings.Repeat(row+",", rows), ",")
}

// LoadUpstreamIPs loads only the active IP rows for a target. Resolution
// refreshes use this instead of loading the whole region snapshot just to
// re-apply the admin's enabled choices.
func (s *Store) LoadUpstreamIPs(scheme, host string, port int) ([]UpstreamIP, error) {
	scheme = normalizeUpstream(scheme)
	host = normalizeUpstream(host)
	rows, err := s.read.Query(
		`SELECT id, ip, latency_ms, reachable, enabled FROM upstream_ips
		 WHERE scheme = ? AND host = ? AND port = ? AND active = 1
		 ORDER BY enabled DESC, reachable DESC, latency_ms ASC, ip ASC`,
		scheme, host, port,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]UpstreamIP, 0, 16)
	for rows.Next() {
		var (
			item    UpstreamIP
			enabled int
			reach   int
		)
		if err := rows.Scan(&item.ID, &item.IP, &item.LatencyMS, &reach, &enabled); err != nil {
			return nil, err
		}
		item.Reachable = reach != 0
		item.Enabled = enabled != 0
		out = append(out, item)
	}
	return out, rows.Err()
}

// LoadUpstreamResolution loads the last persisted, active IP pool.
func (s *Store) LoadUpstreamResolution(scheme, host string, port int) (*UpstreamResolution, error) {
	scheme = normalizeUpstream(scheme)
	host = normalizeUpstream(host)
	res := &UpstreamResolution{Scheme: scheme, Host: host, Port: port}
	err := s.read.QueryRow(
		`SELECT duration_ms, resolved_at, ttl_seconds, region_ok, region_failed, error
		 FROM upstream_resolutions WHERE scheme = ? AND host = ? AND port = ?`,
		scheme, host, port,
	).Scan(&res.DurationMS, &res.ResolvedAt, &res.TTLSeconds, &res.RegionOK, &res.RegionFailed, &res.Error)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	ips, err := s.LoadUpstreamIPs(scheme, host, port)
	if err != nil {
		return nil, err
	}
	res.IPs = ips

	regions, err := s.loadUpstreamRegions(scheme, host, port)
	if err != nil {
		return nil, err
	}
	res.Regions = regions
	return res, nil
}

func (s *Store) loadUpstreamRegions(scheme, host string, port int) ([]UpstreamRegion, error) {
	rows, err := s.read.Query(
		`SELECT subnet, latency_ms, ip_count, error FROM upstream_regions
		 WHERE scheme = ? AND host = ? AND port = ? ORDER BY id ASC`,
		scheme, host, port,
	)
	if err != nil {
		return nil, err
	}
	regions := make([]UpstreamRegion, 0, 16)
	for rows.Next() {
		var region UpstreamRegion
		if err := rows.Scan(&region.Subnet, &region.LatencyMS, &region.IPCount, &region.Error); err != nil {
			rows.Close()
			return nil, err
		}
		regions = append(regions, region)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	if len(regions) == 0 {
		return nil, nil
	}

	index := make(map[string]int, len(regions))
	for position := range regions {
		index[regions[position].Subnet] = position
	}
	ipRows, err := s.read.Query(
		`SELECT subnet, ip FROM upstream_region_ips
		 WHERE scheme = ? AND host = ? AND port = ?`,
		scheme, host, port,
	)
	if err != nil {
		return nil, err
	}
	defer ipRows.Close()
	for ipRows.Next() {
		var subnet, ip string
		if err := ipRows.Scan(&subnet, &ip); err != nil {
			return nil, err
		}
		if position, ok := index[subnet]; ok {
			regions[position].IPs = append(regions[position].IPs, ip)
		}
	}
	if err := ipRows.Err(); err != nil {
		return nil, err
	}
	return regions, nil
}

// SetUpstreamIPEnabled toggles one IP in the pool without changing its
// membership in the latest resolution.
func (s *Store) SetUpstreamIPEnabled(scheme, host string, port int, ip string, enabled bool) error {
	scheme = normalizeUpstream(scheme)
	host = normalizeUpstream(host)
	ip = strings.TrimSpace(ip)
	if scheme == "" || host == "" || port <= 0 || ip == "" {
		return ErrNotFound
	}
	result, err := s.db.Exec(
		`UPDATE upstream_ips SET enabled = ?, updated_at = ?
		 WHERE scheme = ? AND host = ? AND port = ? AND ip = ?`,
		boolInt(enabled), nowUnix(), scheme, host, port, ip,
	)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func dedupeIPStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
