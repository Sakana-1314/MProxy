package store

import (
	"database/sql"
	"errors"
	"strings"
)

// UpstreamIP is one active DoH A record with its probe latency and the admin
// controlled enabled flag.
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

// SaveUpstreamResolution replaces the persisted IP pool for a target. Only
// successful resolutions with at least one IP should be saved.
//
// Rows are never deleted: IPs missing from the new answer are marked inactive,
// while reappearing IPs keep the admin's enabled choice. Region answers are
// replaced as a whole because they describe the latest query only.
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

	stmt, err := tx.Prepare(
		`INSERT INTO upstream_ips
			(scheme, host, port, ip, latency_ms, reachable, enabled, active, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, 1, 1, ?)
		 ON CONFLICT(scheme, host, port, ip) DO UPDATE SET
			latency_ms = excluded.latency_ms,
			reachable = excluded.reachable,
			active = 1,
			updated_at = excluded.updated_at`,
	)
	if err != nil {
		return err
	}
	defer stmt.Close()

	active := make([]string, 0, len(res.IPs))
	for _, item := range res.IPs {
		if item.IP == "" {
			continue
		}
		active = append(active, item.IP)
		if _, err := stmt.Exec(scheme, host, res.Port, item.IP, item.LatencyMS, boolInt(item.Reachable), now); err != nil {
			return err
		}
	}
	if err := deactivateMissingIPs(tx, scheme, host, res.Port, now, active); err != nil {
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

	regionStmt, err := tx.Prepare(
		`INSERT INTO upstream_regions
			(scheme, host, port, subnet, latency_ms, ip_count, error, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
	)
	if err != nil {
		return err
	}
	defer regionStmt.Close()
	regionIPStmt, err := tx.Prepare(
		`INSERT OR IGNORE INTO upstream_region_ips (scheme, host, port, subnet, ip)
		 VALUES (?, ?, ?, ?, ?)`,
	)
	if err != nil {
		return err
	}
	defer regionIPStmt.Close()

	for _, region := range res.Regions {
		subnet := strings.TrimSpace(region.Subnet)
		if subnet == "" {
			continue
		}
		ips := dedupeIPStrings(region.IPs)
		if _, err := regionStmt.Exec(scheme, host, res.Port, subnet, region.LatencyMS, len(ips), region.Error, now); err != nil {
			return err
		}
		for _, ip := range ips {
			if _, err := regionIPStmt.Exec(scheme, host, res.Port, subnet, ip); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func deactivateMissingIPs(tx *sql.Tx, scheme, host string, port int, now int64, active []string) error {
	if len(active) == 0 {
		_, err := tx.Exec(
			`UPDATE upstream_ips SET active = 0, updated_at = ?
			 WHERE scheme = ? AND host = ? AND port = ?`,
			now, scheme, host, port,
		)
		return err
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(active)), ",")
	args := make([]any, 0, len(active)+4)
	args = append(args, now, scheme, host, port)
	for _, ip := range active {
		args = append(args, ip)
	}
	_, err := tx.Exec(
		`UPDATE upstream_ips SET active = 0, updated_at = ?
		 WHERE scheme = ? AND host = ? AND port = ? AND ip NOT IN (`+placeholders+`)`,
		args...,
	)
	return err
}

// LoadUpstreamResolution loads the last persisted, active IP pool.
func (s *Store) LoadUpstreamResolution(scheme, host string, port int) (*UpstreamResolution, error) {
	scheme = normalizeUpstream(scheme)
	host = normalizeUpstream(host)
	res := &UpstreamResolution{Scheme: scheme, Host: host, Port: port}
	err := s.db.QueryRow(
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

	rows, err := s.db.Query(
		`SELECT id, ip, latency_ms, reachable, enabled FROM upstream_ips
		 WHERE scheme = ? AND host = ? AND port = ? AND active = 1
		 ORDER BY enabled DESC, reachable DESC, latency_ms ASC, ip ASC`,
		scheme, host, port,
	)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var (
			item    UpstreamIP
			enabled int
			reach   int
		)
		if err := rows.Scan(&item.ID, &item.IP, &item.LatencyMS, &reach, &enabled); err != nil {
			rows.Close()
			return nil, err
		}
		item.Reachable = reach != 0
		item.Enabled = enabled != 0
		res.IPs = append(res.IPs, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	regions, err := s.loadUpstreamRegions(scheme, host, port)
	if err != nil {
		return nil, err
	}
	res.Regions = regions
	return res, nil
}

func (s *Store) loadUpstreamRegions(scheme, host string, port int) ([]UpstreamRegion, error) {
	rows, err := s.db.Query(
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
	ipRows, err := s.db.Query(
		`SELECT subnet, ip FROM upstream_region_ips
		 WHERE scheme = ? AND host = ? AND port = ? ORDER BY rowid ASC`,
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
