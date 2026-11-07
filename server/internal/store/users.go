package store

import (
	"database/sql"
	"errors"
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

// ErrUnlimitedQuota is returned when trying to add quota to an unlimited user.
var ErrUnlimitedQuota = errors.New("unlimited quota account")

// CountUsers returns the number of proxy users.
func (s *Store) CountUsers() (int, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM proxy_users`).Scan(&count)
	return count, err
}

// ListUsers returns a filtered, paginated proxy user list.
func (s *Store) ListUsers(keyword string, page, pageSize int) ([]ProxyUser, int, error) {
	where := ""
	args := []any{}
	if keyword != "" {
		where = " WHERE username LIKE ? OR note LIKE ?"
		like := "%" + keyword + "%"
		args = append(args, like, like)
	}

	var total int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM proxy_users"+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	args = append(args, pageSize, (page-1)*pageSize)
	rows, err := s.db.Query(
		`SELECT id, username, password_hash, enabled, quota_bytes, total_bytes,
		        expires_at, note, created_at, updated_at
		 FROM proxy_users`+where+` ORDER BY id DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	users := make([]ProxyUser, 0, pageSize)
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return nil, 0, err
		}
		users = append(users, *user)
	}
	return users, total, rows.Err()
}

// GetUserByUsername returns a proxy user by username.
func (s *Store) GetUserByUsername(username string) (*ProxyUser, error) {
	row := s.db.QueryRow(
		`SELECT id, username, password_hash, enabled, quota_bytes, total_bytes,
		        expires_at, note, created_at, updated_at
		 FROM proxy_users WHERE username = ?`, username)
	return scanUser(row)
}

// GetUser returns a proxy user by id.
func (s *Store) GetUser(id int64) (*ProxyUser, error) {
	row := s.db.QueryRow(
		`SELECT id, username, password_hash, enabled, quota_bytes, total_bytes,
		        expires_at, note, created_at, updated_at
		 FROM proxy_users WHERE id = ?`, id)
	return scanUser(row)
}

// CreateUser hashes the password and inserts a proxy user. QuotaBytes is the
// permanent initial allowance (0 means unlimited).
func (s *Store) CreateUser(user *ProxyUser, password string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	now := nowUnix()
	result, err := s.db.Exec(
		`INSERT INTO proxy_users (username, password_hash, enabled, quota_bytes, total_bytes,
		                          expires_at, note, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		user.Username, string(hash), boolInt(user.Enabled), user.QuotaBytes, user.TotalBytes,
		user.ExpiresAt, user.Note, now, now,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return ErrDuplicate
		}
		return err
	}
	user.ID, _ = result.LastInsertId()
	user.PasswordHash = string(hash)
	user.CreatedAt, user.UpdatedAt = now, now
	return nil
}

// UpdateUser updates editable profile fields. Quotas are intentionally
// excluded: they can only be increased through AddUserQuota.
func (s *Store) UpdateUser(user *ProxyUser) error {
	now := nowUnix()
	result, err := s.db.Exec(
		`UPDATE proxy_users SET enabled = ?, expires_at = ?, note = ?,
		       password_hash = ?, updated_at = ?
		 WHERE id = ?`,
		boolInt(user.Enabled), user.ExpiresAt, user.Note,
		user.PasswordHash, now, user.ID,
	)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}
	user.UpdatedAt = now
	return nil
}

// SetUserEnabled enables or disables a proxy user.
func (s *Store) SetUserEnabled(id int64, enabled bool) error {
	result, err := s.db.Exec(`UPDATE proxy_users SET enabled = ?, updated_at = ? WHERE id = ?`,
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

// DeleteUser removes a proxy user. Historical logs are retained.
func (s *Store) DeleteUser(id int64) error {
	result, err := s.db.Exec(`DELETE FROM proxy_users WHERE id = ?`, id)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// AddUserQuota permanently increases a user's traffic quota. Unlimited users
// (quota 0) cannot be given a finite quota, because that would be a reduction.
func (s *Store) AddUserQuota(id int64, addBytes int64) error {
	if addBytes <= 0 {
		return errors.New("quota increment must be positive")
	}
	result, err := s.db.Exec(
		`UPDATE proxy_users SET quota_bytes = quota_bytes + ?, updated_at = ?
		 WHERE id = ? AND quota_bytes > 0`,
		addBytes, nowUnix(), id,
	)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		user, lookupErr := s.GetUser(id)
		if lookupErr != nil {
			return lookupErr
		}
		if user.QuotaBytes == 0 {
			return ErrUnlimitedQuota
		}
		return ErrNotFound
	}
	return nil
}

func scanUser(scanner interface{ Scan(...any) error }) (*ProxyUser, error) {
	var (
		u         ProxyUser
		enabled   int
		expiresAt sql.NullInt64
	)
	err := scanner.Scan(&u.ID, &u.Username, &u.PasswordHash, &enabled, &u.QuotaBytes,
		&u.TotalBytes, &expiresAt, &u.Note, &u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	u.Enabled = enabled != 0
	if expiresAt.Valid {
		v := expiresAt.Int64
		u.ExpiresAt = &v
	}
	return &u, nil
}
