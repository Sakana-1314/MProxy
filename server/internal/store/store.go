package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Store wraps the SQLite database.
type Store struct {
	db   *sql.DB
	path string
}

// Open opens (and initialises) the SQLite database at path.
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create data directory: %w", err)
		}
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// A single connection keeps SQLite writes deterministic and avoids
	// SQLITE_BUSY under the small concurrency this service needs.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	pragmas := []string{
		"PRAGMA journal_mode = WAL",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA synchronous = NORMAL",
		"PRAGMA foreign_keys = ON",
	}
	for _, pragma := range pragmas {
		if _, err := db.Exec(pragma); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("%s: %w", pragma, err)
		}
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	s := &Store{db: db, path: path}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate database: %w", err)
	}
	return s, nil
}

// Close closes the underlying database.
func (s *Store) Close() error { return s.db.Close() }

// Path returns the database file path.
func (s *Store) Path() string { return s.path }

var migrations = [][]string{
	// version 1
	{
		`CREATE TABLE IF NOT EXISTS admins (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT NOT NULL COLLATE NOCASE UNIQUE,
			password_hash TEXT NOT NULL,
			password_changed_at INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS proxy_users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT NOT NULL COLLATE NOCASE UNIQUE,
			password_hash TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1,
			quota_bytes INTEGER NOT NULL DEFAULT 0,
			used_bytes INTEGER NOT NULL DEFAULT 0,
			total_bytes INTEGER NOT NULL DEFAULT 0,
			period_start INTEGER NOT NULL DEFAULT 0,
			expires_at INTEGER,
			note TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS rules (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			match_host TEXT NOT NULL COLLATE NOCASE,
			match_port INTEGER NOT NULL DEFAULT 0,
			upstream_host TEXT NOT NULL,
			upstream_port INTEGER NOT NULL DEFAULT 443,
			upstream_scheme TEXT NOT NULL DEFAULT 'https',
			upstream_sni TEXT NOT NULL DEFAULT '',
			upstream_verify INTEGER NOT NULL DEFAULT 0,
			host_mode TEXT NOT NULL DEFAULT 'upstream',
			host_value TEXT NOT NULL DEFAULT '',
			enabled INTEGER NOT NULL DEFAULT 1,
			remark TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			UNIQUE(match_host, match_port)
		)`,
		`CREATE TABLE IF NOT EXISTS request_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			created_at INTEGER NOT NULL,
			user_id INTEGER NOT NULL DEFAULT 0,
			username TEXT NOT NULL DEFAULT '-',
			client_ip TEXT NOT NULL DEFAULT '',
			method TEXT NOT NULL DEFAULT '',
			scheme TEXT NOT NULL DEFAULT '',
			path TEXT NOT NULL DEFAULT '',
			target_host TEXT NOT NULL DEFAULT '',
			target_port INTEGER NOT NULL DEFAULT 0,
			upstream TEXT NOT NULL DEFAULT '',
			status INTEGER NOT NULL DEFAULT 0,
			bytes_up INTEGER NOT NULL DEFAULT 0,
			bytes_down INTEGER NOT NULL DEFAULT 0,
			duration_ms INTEGER NOT NULL DEFAULT 0,
			error TEXT NOT NULL DEFAULT '',
			rule_id INTEGER NOT NULL DEFAULT 0,
			rule_name TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_logs_created_at ON request_logs(created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_logs_username ON request_logs(username)`,
		`CREATE INDEX IF NOT EXISTS idx_logs_target_host ON request_logs(target_host)`,
		`CREATE INDEX IF NOT EXISTS idx_logs_user_id ON request_logs(user_id)`,
		`CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
	},
	// version 2: rules match an explicit target scheme, and the upstream Host is
	// always used. Wildcards and configurable Host modes are removed.
	{
		`CREATE TABLE rules_v2 (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			target_scheme TEXT NOT NULL DEFAULT 'https',
			match_host TEXT NOT NULL COLLATE NOCASE,
			match_port INTEGER NOT NULL DEFAULT 443,
			upstream_host TEXT NOT NULL,
			upstream_port INTEGER NOT NULL DEFAULT 443,
			upstream_scheme TEXT NOT NULL DEFAULT 'https',
			upstream_sni TEXT NOT NULL DEFAULT '',
			upstream_verify INTEGER NOT NULL DEFAULT 0,
			enabled INTEGER NOT NULL DEFAULT 1,
			remark TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			UNIQUE(target_scheme, match_host, match_port)
		)`,
		`INSERT INTO rules_v2 (
			id, name, target_scheme, match_host, match_port, upstream_host, upstream_port,
			upstream_scheme, upstream_sni, upstream_verify, enabled, remark, created_at, updated_at
		)
		SELECT id, name,
			CASE WHEN match_port = 80 THEN 'http' ELSE 'https' END,
			match_host,
			CASE WHEN match_port = 0 THEN (CASE WHEN upstream_scheme = 'http' THEN 80 ELSE 443 END) ELSE match_port END,
			upstream_host, upstream_port, upstream_scheme, upstream_sni, upstream_verify,
			enabled, remark, created_at, updated_at
		FROM rules`,
		`DROP TABLE rules`,
		`ALTER TABLE rules_v2 RENAME TO rules`,
	},
	// version 3: quota is a permanent lifetime limit. Monthly usage/reset state
	// is removed; total_bytes becomes the single cumulative counter.
	{
		`CREATE TABLE proxy_users_v2 (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT NOT NULL COLLATE NOCASE UNIQUE,
			password_hash TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1,
			quota_bytes INTEGER NOT NULL DEFAULT 0,
			total_bytes INTEGER NOT NULL DEFAULT 0,
			expires_at INTEGER,
			note TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`INSERT INTO proxy_users_v2 (
			id, username, password_hash, enabled, quota_bytes, total_bytes,
			expires_at, note, created_at, updated_at
		)
		SELECT id, username, password_hash, enabled, quota_bytes, total_bytes,
			expires_at, note, created_at, updated_at
		FROM proxy_users`,
		`DROP TABLE proxy_users`,
		`ALTER TABLE proxy_users_v2 RENAME TO proxy_users`,
	},
	// version 4: upstream SNI is no longer configurable; the upstream host is
	// always used as both the Host header and TLS SNI.
	{
		`CREATE TABLE rules_v4 (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			target_scheme TEXT NOT NULL DEFAULT 'https',
			match_host TEXT NOT NULL COLLATE NOCASE,
			match_port INTEGER NOT NULL DEFAULT 443,
			upstream_host TEXT NOT NULL,
			upstream_port INTEGER NOT NULL DEFAULT 443,
			upstream_scheme TEXT NOT NULL DEFAULT 'https',
			upstream_verify INTEGER NOT NULL DEFAULT 0,
			enabled INTEGER NOT NULL DEFAULT 1,
			remark TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			UNIQUE(target_scheme, match_host, match_port)
		)`,
		`INSERT INTO rules_v4 (
			id, name, target_scheme, match_host, match_port, upstream_host, upstream_port,
			upstream_scheme, upstream_verify, enabled, remark, created_at, updated_at
		)
		SELECT id, name, target_scheme, match_host, match_port, upstream_host, upstream_port,
			upstream_scheme, upstream_verify, enabled, remark, created_at, updated_at
		FROM rules`,
		`DROP TABLE rules`,
		`ALTER TABLE rules_v4 RENAME TO rules`,
	},
	// version 5: upstream TLS certificates are never verified; the per-rule
	// verification switch is removed.
	{
		`CREATE TABLE rules_v5 (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			target_scheme TEXT NOT NULL DEFAULT 'https',
			match_host TEXT NOT NULL COLLATE NOCASE,
			match_port INTEGER NOT NULL DEFAULT 443,
			upstream_host TEXT NOT NULL,
			upstream_port INTEGER NOT NULL DEFAULT 443,
			upstream_scheme TEXT NOT NULL DEFAULT 'https',
			enabled INTEGER NOT NULL DEFAULT 1,
			remark TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			UNIQUE(target_scheme, match_host, match_port)
		)`,
		`INSERT INTO rules_v5 (
			id, name, target_scheme, match_host, match_port, upstream_host, upstream_port,
			upstream_scheme, enabled, remark, created_at, updated_at
		)
		SELECT id, name, target_scheme, match_host, match_port, upstream_host, upstream_port,
			upstream_scheme, enabled, remark, created_at, updated_at
		FROM rules`,
		`DROP TABLE rules`,
		`ALTER TABLE rules_v5 RENAME TO rules`,
	},
	// version 6: persist upstream DoH resolution results and every resolved IP
	// (with probe latency) so the pool survives restarts.
	{
		`CREATE TABLE IF NOT EXISTS upstream_resolutions (
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
		`CREATE TABLE IF NOT EXISTS upstream_ips (
			scheme TEXT NOT NULL,
			host TEXT NOT NULL COLLATE NOCASE,
			port INTEGER NOT NULL,
			ip TEXT NOT NULL,
			latency_ms INTEGER NOT NULL DEFAULT -1,
			reachable INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (scheme, host, port, ip)
		)`,
	},
	// version 7: request logs are removed. Traffic is aggregated in memory
	// and flushed into hourly buckets; per-request detail is intentionally lost.
	{
		`DROP TABLE IF EXISTS request_logs`,
		`CREATE TABLE IF NOT EXISTS traffic_stats (
			bucket INTEGER PRIMARY KEY,
			requests INTEGER NOT NULL DEFAULT 0,
			bytes INTEGER NOT NULL DEFAULT 0
		)`,
		`DELETE FROM settings WHERE key = 'log_retention_days'`,
	},
	// version 8: anonymous proxy access is no longer supported.
	{
		`DELETE FROM settings WHERE key = 'allow_anonymous'`,
	},
	// version 9: the public proxy address is no longer configurable.
	{
		`DELETE FROM settings WHERE key = 'public_proxy_addr'`,
	},
	// version 10: the site name is a frontend constant.
	{
		`DELETE FROM settings WHERE key = 'site_name'`,
	},
}

func (s *Store) migrate() error {
	var version int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if version >= len(migrations) {
		return nil
	}
	for i := version; i < len(migrations); i++ {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		for _, statement := range migrations[i] {
			if _, err := tx.Exec(statement); err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("migration %d: %w", i+1, err)
			}
		}
		if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", i+1)); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func nowUnix() int64 { return time.Now().Unix() }

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

// StartOfDay returns local midnight for t.
func StartOfDay(t time.Time) time.Time {
	local := t.In(time.Local)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.Local)
}
