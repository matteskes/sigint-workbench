// Package db provides PostgreSQL/PostGIS database access.
package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DB wraps a Postgres connection pool.
type DB struct {
	Pool *pgxpool.Pool
}

// New creates a new DB connection.
func New(ctx context.Context, url string) (*DB, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("db: connect: %w", err)
	}
	// Verify connection
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("db: ping: %w", err)
	}
	return &DB{Pool: pool}, nil
}

// Close closes the connection pool.
func (d *DB) Close() {
	d.Pool.Close()
}