package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// ErrNotFound is returned when the requested record does not exist.
var ErrNotFound = errors.New("record not found")

// CountAdmins returns the number of management accounts.
func (s *Store) CountAdmins() (int, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM admins`).Scan(&count)
	return count, err
}

// SeedAdmin creates the initial administrator when none exists.
func (s *Store) SeedAdmin(username, password string) (created bool, err error) {
	count, err := s.CountAdmins()
	if err != nil {
		return false, err
	}
	if count > 0 {
		return false, nil
	}
	if username == "" {
		username = "admin"
	}
	if password == "" {
		password = "admin123"
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return false, fmt.Errorf("hash admin password: %w", err)
	}
	now := nowUnix()
	_, err = s.db.Exec(
		`INSERT INTO admins (username, password_hash, password_changed_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?)`,
		username, string(hash), now, now, now,
	)
	if err != nil {
		return false, err
	}
	return true, nil
}

// GetAdminByUsername returns an admin by username.
func (s *Store) GetAdminByUsername(username string) (*Admin, error) {
	row := s.db.QueryRow(
		`SELECT id, username, password_hash, password_changed_at, created_at, updated_at
		 FROM admins WHERE username = ?`, username)
	return scanAdmin(row)
}

// GetAdminByID returns an admin by id.
func (s *Store) GetAdminByID(id int64) (*Admin, error) {
	row := s.db.QueryRow(
		`SELECT id, username, password_hash, password_changed_at, created_at, updated_at
		 FROM admins WHERE id = ?`, id)
	return scanAdmin(row)
}

func scanAdmin(scanner interface{ Scan(...any) error }) (*Admin, error) {
	var a Admin
	err := scanner.Scan(&a.ID, &a.Username, &a.PasswordHash, &a.PasswordChangedAt, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// UpdateAdminPassword updates an administrator's password hash.
func (s *Store) UpdateAdminPassword(id int64, password string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	now := time.Now().Unix()
	res, err := s.db.Exec(
		`UPDATE admins SET password_hash = ?, password_changed_at = ?, updated_at = ? WHERE id = ?`,
		string(hash), now, now, id,
	)
	if err != nil {
		return err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}
