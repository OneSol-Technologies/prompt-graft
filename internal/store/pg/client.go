package pg

import (
	"context"
	_ "embed"

	"github.com/jackc/pgx/v5/pgxpool"

	"promptguru/internal/logging"
)

//go:embed migrations/001_initial.sql
var migrationSQL string

// Client wraps a pgxpool connection pool.
type Client struct {
	pool *pgxpool.Pool
	log  *logging.Logger
}

// New connects to Postgres and returns a Client.
// The caller must call Close() when done.
func New(ctx context.Context, url string, log *logging.Logger) (*Client, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return &Client{pool: pool, log: log}, nil
}

// Migrate runs the embedded migration SQL idempotently.
// Uses a Postgres advisory lock (lock ID 8432659) so concurrent service
// startups serialise on the migration rather than racing on index creation.
func (c *Client) Migrate(ctx context.Context) error {
	conn, err := c.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock(8432659)"); err != nil {
		return err
	}
	defer conn.Exec(ctx, "SELECT pg_advisory_unlock(8432659)") //nolint:errcheck

	_, err = conn.Exec(ctx, migrationSQL)
	return err
}

// Close releases all pool connections.
func (c *Client) Close() {
	c.pool.Close()
}
