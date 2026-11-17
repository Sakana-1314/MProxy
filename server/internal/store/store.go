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

// Store wraps the SQLite database. Writes go through a single connection so
// SQLite never sees concurrent writers; reads use a small WAL-backed pool so
// list/dashboard/auth queries do not queue behind DoH persistence.
type Store struct {
	db   *sql.DB
	read *sql.DB
	path string
}

const (
	sqliteWriterParams = "_txlock=immediate&_busy_timeout=5000&_foreign_keys=on" +
		"&_journal_mode=WAL&_synchronous=NORMAL" +
		"&_pragma=cache_size(-4000)&_pragma=temp_store(MEMORY)"
	sqliteReaderParams = "_busy_timeout=5000&_query_only=1" +
		"&_pragma=cache_size(-4000)&_pragma=temp_store(MEMORY)"
	readPoolSize = 4
)

func sqliteDSN(path, query string) string {
	separator := "?"
	if strings.Contains(path, "?") {
		separator = "&"
	}
	return path + separator + query
}

// Open opens (and initialises) the SQLite database at path.
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create data directory: %w", err)
		}
	}

	writer, err := sql.Open("sqlite", sqliteDSN(path, sqliteWriterParams))
	if err != nil {
		return nil, fmt.Errorf("open sqlite writer: %w", err)
	}
	// One writer keeps SQLite writes deterministic; WAL readers run in parallel.
	writer.SetMaxOpenConns(1)
	writer.SetMaxIdleConns(1)
	if err := writer.Ping(); err != nil {
		_ = writer.Close()
		return nil, fmt.Errorf("ping sqlite writer: %w", err)
	}

	s := &Store{db: writer, path: path}
	if err := s.migrate(); err != nil {
		_ = writer.Close()
		return nil, fmt.Errorf("migrate database: %w", err)
	}

	reader, err := sql.Open("sqlite", sqliteDSN(path, sqliteReaderParams))
	if err != nil {
		_ = writer.Close()
		return nil, fmt.Errorf("open sqlite reader: %w", err)
	}
	reader.SetMaxOpenConns(readPoolSize)
	reader.SetMaxIdleConns(readPoolSize)
	reader.SetConnMaxIdleTime(10 * time.Minute)
	if err := reader.Ping(); err != nil {
		_ = reader.Close()
		_ = writer.Close()
		return nil, fmt.Errorf("ping sqlite reader: %w", err)
	}
	s.read = reader
	return s, nil
}

// Close closes the read pool and the writer.
func (s *Store) Close() error {
	var firstErr error
	if s.read != nil {
		firstErr = s.read.Close()
	}
	if s.db != nil {
		if err := s.db.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

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
	// version 11: the IP pool follows the MisakaStudy proxy_ip design. Rows keep
	// a stable id and an enabled flag, stale addresses stay in the table with
	// active = 0, and every region's DoH answer is stored separately so the
	// management UI can show the pool per region after a restart.
	{
		`CREATE TABLE upstream_ips_v11 (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			scheme TEXT NOT NULL,
			host TEXT NOT NULL COLLATE NOCASE,
			port INTEGER NOT NULL,
			ip TEXT NOT NULL,
			latency_ms INTEGER NOT NULL DEFAULT -1,
			reachable INTEGER NOT NULL DEFAULT 0,
			enabled INTEGER NOT NULL DEFAULT 1,
			active INTEGER NOT NULL DEFAULT 1,
			updated_at INTEGER NOT NULL,
			UNIQUE(scheme, host, port, ip)
		)`,
		`INSERT INTO upstream_ips_v11 (
			scheme, host, port, ip, latency_ms, reachable, enabled, active, updated_at
		)
		SELECT scheme, host, port, ip, latency_ms, reachable, 1, 1,
			CAST(strftime('%s', 'now') AS INTEGER)
		FROM upstream_ips`,
		`DROP TABLE upstream_ips`,
		`ALTER TABLE upstream_ips_v11 RENAME TO upstream_ips`,
		`CREATE TABLE IF NOT EXISTS upstream_regions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			scheme TEXT NOT NULL,
			host TEXT NOT NULL COLLATE NOCASE,
			port INTEGER NOT NULL,
			subnet TEXT NOT NULL,
			latency_ms INTEGER NOT NULL DEFAULT -1,
			ip_count INTEGER NOT NULL DEFAULT 0,
			error TEXT NOT NULL DEFAULT '',
			updated_at INTEGER NOT NULL,
			UNIQUE(scheme, host, port, subnet)
		)`,
		`CREATE TABLE IF NOT EXISTS upstream_region_ips (
			scheme TEXT NOT NULL,
			host TEXT NOT NULL COLLATE NOCASE,
			port INTEGER NOT NULL,
			subnet TEXT NOT NULL,
			ip TEXT NOT NULL,
			PRIMARY KEY (scheme, host, port, subnet, ip)
		)`,
	},
	// version 12: the active pool is loaded on every resolver cache fill. A
	// partial index covers the target filter and the latency ordering so the
	// query no longer sorts through a temp B-tree.
	{
		`CREATE INDEX IF NOT EXISTS idx_upstream_ips_pool
			ON upstream_ips(scheme, host, port, enabled DESC, reachable DESC, latency_ms ASC, ip ASC)
			WHERE active = 1`,
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
