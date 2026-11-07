package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
)

const (
	SettingJWTSecret = "jwt_secret"
)

// GetSetting returns one setting value.
func (s *Store) GetSetting(key string) (string, bool, error) {
	var value string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}

// SetSetting upserts a single setting.
func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(
		`INSERT INTO settings (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// EnsureJWTSecret loads or generates the JWT signing secret.
func (s *Store) EnsureJWTSecret() (string, error) {
	value, ok, err := s.GetSetting(SettingJWTSecret)
	if err != nil {
		return "", err
	}
	if ok && len(value) >= 32 {
		return value, nil
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	secret := hex.EncodeToString(buf)
	if err := s.SetSetting(SettingJWTSecret, secret); err != nil {
		return "", err
	}
	return secret, nil
}
