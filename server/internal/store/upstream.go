package store

import (
	"database/sql"
	"errors"
	"strings"
)

// UpstreamIP is one persisted DoH A record and its probe latency.
type UpstreamIP struct {
	IP        string `json:"ip"`
	LatencyMS int64  `json:"latency_ms"`
	Reachable bool   `json:"reachable"`
}

// UpstreamResolution is the persisted resolution of one upstream target.
type UpstreamResolution struct {
	Scheme       string       `json:"scheme"`
	Host         string       `json:"host"`
	Port         int          `json:"port"`
	DurationMS   int64        `json:"duration_ms"`
	ResolvedAt   int64        `json:"resolved_at"`
	TTLSeconds   int          `json:"ttl_seconds"`
	RegionOK     int          `json:"region_ok"`
	RegionFailed int          `json:"region_failed"`
	Error        string       `json:"error,omitempty"`
	IPs          []UpstreamIP `json:"ips"`
}

func normalizeUpstream(key string) string {
	return strings.ToLower(strings.TrimSpace(key))
}

// SaveUpstreamResolution replaces the persisted IP pool for a target. Only
// successful resolutions with at least one IP should be saved.
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
	if _, err := tx.Exec(
		`DELETE FROM upstream_ips WHERE scheme = ? AND host = ? AND port = ?`,
		scheme, host, res.Port,
	); err != nil {
		return err
	}
	stmt, err := tx.Prepare(
		`INSERT INTO upstream_ips (scheme, host, port, ip, latency_ms, reachable)
		 VALUES (?, ?, ?, ?, ?, ?)`,
	)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, item := range res.IPs {
		if item.IP == "" {
			continue
		}
		if _, err := stmt.Exec(scheme, host, res.Port, item.IP, item.LatencyMS, boolInt(item.Reachable)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// LoadUpstreamResolution loads the last successful persisted resolution.
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
		`SELECT ip, latency_ms, reachable FROM upstream_ips
		 WHERE scheme = ? AND host = ? AND port = ?`,
		scheme, host, port,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			item      UpstreamIP
			reachable int
		)
		if err := rows.Scan(&item.IP, &item.LatencyMS, &reachable); err != nil {
			return nil, err
		}
		item.Reachable = reachable != 0
		res.IPs = append(res.IPs, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return res, nil
}
