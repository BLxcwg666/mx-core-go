package redis

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Client wraps go-redis for the application.
type Client struct {
	rdb *redis.Client
}

var Default *Client

// Connect creates a Redis client and verifies connectivity.
func Connect(url string) (*Client, error) {
	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("invalid redis url: %w", err)
	}

	rdb := redis.NewClient(opts)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis ping failed: %w", err)
	}

	c := &Client{rdb: rdb}
	Default = c
	return c, nil
}

// Raw returns the underlying redis.Client for advanced usage.
func (c *Client) Raw() *redis.Client { return c.rdb }

// Set stores a value with optional TTL (0 = no expiry).
func (c *Client) Set(ctx context.Context, key string, value interface{}, ttl time.Duration) error {
	return c.rdb.Set(ctx, key, value, ttl).Err()
}

// Get retrieves a string value. Returns ("", nil) if key does not exist.
func (c *Client) Get(ctx context.Context, key string) (string, error) {
	val, err := c.rdb.Get(ctx, key).Result()
	if err == redis.Nil {
		return "", nil
	}
	return val, err
}

// Del deletes one or more keys.
func (c *Client) Del(ctx context.Context, keys ...string) error {
	return c.rdb.Del(ctx, keys...).Err()
}

// Exists reports whether a key exists.
func (c *Client) Exists(ctx context.Context, key string) (bool, error) {
	n, err := c.rdb.Exists(ctx, key).Result()
	return n > 0, err
}

// Publish sends a message to a Redis pub/sub channel.
func (c *Client) Publish(ctx context.Context, channel string, message interface{}) error {
	return c.rdb.Publish(ctx, channel, message).Err()
}

// Subscribe returns a pub/sub subscription for the given channels.
func (c *Client) Subscribe(ctx context.Context, channels ...string) *redis.PubSub {
	return c.rdb.Subscribe(ctx, channels...)
}
