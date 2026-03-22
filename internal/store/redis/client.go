package redis

import (
    "context"
    "time"

    goredis "github.com/redis/go-redis/v9"

    "promptguru/internal/logging"
)

type Client struct {
    rdb *goredis.Client
    log *logging.Logger
}

func New(url string, log *logging.Logger) (*Client, error) {
    opts, err := goredis.ParseURL(url)
    if err != nil {
        return nil, err
    }
    return &Client{rdb: goredis.NewClient(opts), log: log}, nil
}

func (c *Client) Ping(ctx context.Context) error {
    return c.rdb.Ping(ctx).Err()
}

func (c *Client) Close() error {
    return c.rdb.Close()
}

func (c *Client) WithTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
    return context.WithTimeout(ctx, timeout)
}
