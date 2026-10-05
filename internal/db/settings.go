// Package db — app_settings key/value store (§20 setup state).
package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// GetSetting returns a raw app_settings value, or ErrNotFound when
// the key is absent (§20).
func (d *DB) GetSetting(ctx context.Context, key string) (string, error) {
	var v string
	const q = "SELECT value FROM app_settings WHERE key = $1"
	err := d.Pool.QueryRow(ctx, q, key).Scan(&v)
	if err != nil {
		if err == pgx.ErrNoRows {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("db: get setting %s: %w", key, err)
	}
	return v, nil
}

// SetSetting upserts a raw app_settings value (§20).
func (d *DB) SetSetting(ctx context.Context, key, value string) error {
	const q = "INSERT INTO app_settings (key, value, updated_at) VALUES ($1, $2, now()) " +
		"ON CONFLICT (key) DO UPDATE SET value = $2, updated_at = now()"
	if _, err := d.Pool.Exec(ctx, q, key, value); err != nil {
		return fmt.Errorf("db: set setting %s: %w", key, err)
	}
	return nil
}
