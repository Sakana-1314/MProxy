package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

const (
	// SchemeHTTP and SchemeHTTPS are the only protocols spoken by the proxy.
	SchemeHTTP  = "http"
	SchemeHTTPS = "https"
	// TargetSchemeBoth accepts both HTTP and HTTPS clients on the same port.
	TargetSchemeBoth = "both"
	// UpstreamSchemeFollow uses the client's protocol for the upstream request.
	UpstreamSchemeFollow = "follow"
)

// ListRules returns a filtered, paginated rule list.
func (s *Store) ListRules(keyword string, page, pageSize int) ([]Rule, int, error) {
	where := ""
	args := []any{}
	if keyword != "" {
		where = " WHERE name LIKE ? OR match_host LIKE ? OR upstream_host LIKE ?"
		like := "%" + keyword + "%"
		args = append(args, like, like, like)
	}

	var total int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM rules"+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	args = append(args, pageSize, (page-1)*pageSize)
	rows, err := s.db.Query(
		`SELECT id, name, target_scheme, match_host, match_port, upstream_host, upstream_port,
		        upstream_scheme, enabled, remark, created_at, updated_at
		 FROM rules`+where+` ORDER BY id DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	rules := make([]Rule, 0, pageSize)
	for rows.Next() {
		rule, err := scanRule(rows)
		if err != nil {
			return nil, 0, err
		}
		rules = append(rules, *rule)
	}
	return rules, total, rows.Err()
}

// AllEnabledRules returns every enabled rule for the proxy matcher.
func (s *Store) AllEnabledRules() ([]Rule, error) {
	rows, err := s.db.Query(
		`SELECT id, name, target_scheme, match_host, match_port, upstream_host, upstream_port,
		        upstream_scheme, enabled, remark, created_at, updated_at
		 FROM rules WHERE enabled = 1 ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	rules := make([]Rule, 0, 16)
	for rows.Next() {
		rule, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		rules = append(rules, *rule)
	}
	return rules, rows.Err()
}

// GetRule returns a rule by id.
func (s *Store) GetRule(id int64) (*Rule, error) {
	row := s.db.QueryRow(
		`SELECT id, name, target_scheme, match_host, match_port, upstream_host, upstream_port,
		        upstream_scheme, enabled, remark, created_at, updated_at
		 FROM rules WHERE id = ?`, id)
	return scanRule(row)
}

// CreateRule inserts a rule and fills its id/timestamps.
func (s *Store) CreateRule(rule *Rule) error {
	now := nowUnix()
	result, err := s.db.Exec(
		`INSERT INTO rules (name, target_scheme, match_host, match_port, upstream_host, upstream_port,
		                    upstream_scheme, enabled, remark, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rule.Name, rule.TargetScheme, rule.MatchHost, rule.MatchPort, rule.UpstreamHost,
		rule.UpstreamPort, rule.UpstreamScheme, boolInt(rule.Enabled), rule.Remark, now, now,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return ErrDuplicate
		}
		return err
	}
	rule.ID, _ = result.LastInsertId()
	rule.CreatedAt, rule.UpdatedAt = now, now
	return nil
}

// UpdateRule updates an existing rule.
func (s *Store) UpdateRule(rule *Rule) error {
	now := nowUnix()
	result, err := s.db.Exec(
		`UPDATE rules SET name = ?, target_scheme = ?, match_host = ?, match_port = ?,
		       upstream_host = ?, upstream_port = ?, upstream_scheme = ?,
		       enabled = ?, remark = ?, updated_at = ?
		 WHERE id = ?`,
		rule.Name, rule.TargetScheme, rule.MatchHost, rule.MatchPort, rule.UpstreamHost,
		rule.UpstreamPort, rule.UpstreamScheme, boolInt(rule.Enabled), rule.Remark, now, rule.ID,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return ErrDuplicate
		}
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}
	rule.UpdatedAt = now
	return nil
}

// SetRuleEnabled enables or disables a rule.
func (s *Store) SetRuleEnabled(id int64, enabled bool) error {
	result, err := s.db.Exec(`UPDATE rules SET enabled = ?, updated_at = ? WHERE id = ?`,
		boolInt(enabled), nowUnix(), id)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteRule removes a rule.
func (s *Store) DeleteRule(id int64) error {
	result, err := s.db.Exec(`DELETE FROM rules WHERE id = ?`, id)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// CountEnabledRules returns the number of active rules.
func (s *Store) CountEnabledRules() (int, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM rules WHERE enabled = 1`).Scan(&count)
	return count, err
}

// ErrDuplicate is returned when a unique constraint is violated.
var ErrDuplicate = errors.New("duplicate record")

func scanRule(scanner interface{ Scan(...any) error }) (*Rule, error) {
	var (
		r       Rule
		enabled int
	)
	err := scanner.Scan(&r.ID, &r.Name, &r.TargetScheme, &r.MatchHost, &r.MatchPort,
		&r.UpstreamHost, &r.UpstreamPort, &r.UpstreamScheme,
		&enabled, &r.Remark, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	r.Enabled = enabled != 0
	return &r, nil
}

// NormalizeRule trims whitespace, lowercases hosts/schemes and applies default
// ports. Target scheme + host + port and upstream scheme + host + port form
// explicit URLs.
func NormalizeRule(rule *Rule) {
	rule.Name = strings.TrimSpace(rule.Name)
	rule.TargetScheme = strings.ToLower(strings.TrimSpace(rule.TargetScheme))
	rule.MatchHost = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(rule.MatchHost, ".")))
	rule.UpstreamHost = strings.ToLower(strings.TrimSpace(rule.UpstreamHost))
	rule.UpstreamHost = strings.TrimPrefix(strings.TrimSuffix(rule.UpstreamHost, "]"), "[")
	rule.UpstreamScheme = strings.ToLower(strings.TrimSpace(rule.UpstreamScheme))
	rule.Remark = strings.TrimSpace(rule.Remark)

	if rule.TargetScheme == "" {
		rule.TargetScheme = SchemeHTTPS
	}
	if rule.MatchPort == 0 {
		if rule.TargetScheme == SchemeHTTP {
			rule.MatchPort = 80
		} else {
			rule.MatchPort = 443
		}
	}
	if rule.UpstreamScheme == "" {
		rule.UpstreamScheme = SchemeHTTPS
	}
	if rule.UpstreamPort == 0 {
		switch rule.UpstreamScheme {
		case SchemeHTTP:
			rule.UpstreamPort = 80
		case UpstreamSchemeFollow:
			if rule.TargetScheme == SchemeHTTP {
				rule.UpstreamPort = 80
			} else {
				rule.UpstreamPort = 443
			}
		default:
			rule.UpstreamPort = 443
		}
	}
}

// DescribeRule is a helper for logs.
func DescribeRule(rule Rule) string {
	if rule.UpstreamScheme == "" {
		rule.UpstreamScheme = SchemeHTTPS
	}
	return fmt.Sprintf("%s://%s:%d", rule.UpstreamScheme, rule.UpstreamHost, rule.UpstreamPort)
}
