package common

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"
)

func TestRedisHIncrByIsAtomicAtExpiry(t *testing.T) {
	addr := os.Getenv("REDIS_TEST_ADDR")
	if addr == "" {
		t.Skip("REDIS_TEST_ADDR is not set")
	}

	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: os.Getenv("REDIS_TEST_PASSWORD"),
	})
	require.NoError(t, client.Ping(context.Background()).Err())

	previousClient := RDB
	RDB = client
	t.Cleanup(func() {
		RDB = previousClient
		_ = client.Close()
	})

	key := fmt.Sprintf("test:redis-hincr-expiry:%d", time.Now().UnixNano())
	require.NoError(t, client.HSet(context.Background(), key, "Status", 1, "RemainQuota", 100).Err())
	require.NoError(t, client.PExpire(context.Background(), key, 2*time.Second).Err())
	require.NoError(t, client.Do(context.Background(), "CLIENT", "PAUSE", 2500, "WRITE").Err())

	require.NoError(t, RedisHIncrBy(key, "RemainQuota", -1))
	exists, err := client.Exists(context.Background(), key).Result()
	require.NoError(t, err)
	require.Zero(t, exists, "an expired hash must not be recreated with one field")
}

func TestRedisHIncrByPreservesExistingTTL(t *testing.T) {
	addr := os.Getenv("REDIS_TEST_ADDR")
	if addr == "" {
		t.Skip("REDIS_TEST_ADDR is not set")
	}

	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: os.Getenv("REDIS_TEST_PASSWORD"),
	})
	require.NoError(t, client.Ping(context.Background()).Err())

	previousClient := RDB
	RDB = client
	t.Cleanup(func() {
		RDB = previousClient
		_ = client.Close()
	})

	key := fmt.Sprintf("test:redis-hincr-ttl:%d", time.Now().UnixNano())
	require.NoError(t, client.HSet(context.Background(), key, "RemainQuota", 100).Err())
	require.NoError(t, client.PExpire(context.Background(), key, 10*time.Second).Err())
	before, err := client.PTTL(context.Background(), key).Result()
	require.NoError(t, err)

	require.NoError(t, RedisHIncrBy(key, "RemainQuota", -7))
	quota, err := client.HGet(context.Background(), key, "RemainQuota").Int64()
	require.NoError(t, err)
	after, err := client.PTTL(context.Background(), key).Result()
	require.NoError(t, err)

	require.Equal(t, int64(93), quota)
	require.Positive(t, after)
	require.LessOrEqual(t, after, before)
}
