package repository

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"rokko.dev/pkg/apperr"
	"rokko.dev/pkg/redisx"
)

type RedisRefresh struct{ client *redis.Client }

func NewRedisRefresh(client *redis.Client) *RedisRefresh { return &RedisRefresh{client: client} }

func (r *RedisRefresh) Save(ctx context.Context, hash string, userID int64, username string, ttl time.Duration) error {
	return r.client.Set(ctx, "refresh:"+hash, fmt.Sprintf("%d|%s", userID, username), ttl).Err()
}

func (r *RedisRefresh) Get(ctx context.Context, hash string) (int64, string, error) {
	raw, err := r.client.Get(ctx, "refresh:"+hash).Result()
	if err == redis.Nil {
		return 0, "", apperr.Unauthorized("invalid refresh token")
	}
	if err != nil {
		return 0, "", err
	}
	userPart, username, ok := strings.Cut(raw, "|")
	if !ok {
		return 0, "", apperr.Unauthorized("invalid refresh token")
	}
	userID, err := strconv.ParseInt(userPart, 10, 64)
	if err != nil {
		return 0, "", apperr.Unauthorized("invalid refresh token")
	}
	return userID, username, nil
}

func (r *RedisRefresh) Delete(ctx context.Context, hash string) error {
	return r.client.Del(ctx, "refresh:"+hash).Err()
}

type RedisLimiter struct {
	client *redis.Client
	limit  int64
	window time.Duration
	allow  func(ctx context.Context, key string) (bool, error)
}

func NewRedisLimiter(client *redis.Client, limit int64, window time.Duration) *RedisLimiter {
	return &RedisLimiter{
		client: client,
		limit:  limit,
		window: window,
		allow: func(ctx context.Context, key string) (bool, error) {
			return redisx.Allow(ctx, client, "limit:"+key, limit, window)
		},
	}
}

func (r *RedisRefresh) SaveFamily(ctx context.Context, hash string, userID int64, username, family string, ttl time.Duration) error {
	pipe := r.client.Pipeline()
	pipe.Set(ctx, "refresh:"+hash, fmt.Sprintf("%d|%s|%s", userID, username, family), ttl)
	pipe.SAdd(ctx, "refresh_family:"+family, hash)
	pipe.Expire(ctx, "refresh_family:"+family, ttl)
	pipe.SAdd(ctx, fmt.Sprintf("user_sessions:%d", userID), family)
	pipe.Expire(ctx, fmt.Sprintf("user_sessions:%d", userID), ttl)
	_, err := pipe.Exec(ctx)
	return err
}

func (r *RedisRefresh) Rotate(ctx context.Context, oldHash, newHash string, ttl time.Duration) (int64, string, string, bool, error) {
	raw, err := r.client.GetDel(ctx, "refresh:"+oldHash).Result()
	if err == redis.Nil {
		family, ferr := r.client.Get(ctx, "refresh_used:"+oldHash).Result()
		if ferr == nil {
			_ = r.revokeFamily(ctx, family)
			return 0, "", family, true, apperr.Unauthorized("refresh reused")
		}
		return 0, "", "", false, apperr.Unauthorized("invalid refresh token")
	}
	if err != nil {
		return 0, "", "", false, err
	}
	userID, username, family, err := parseRefresh(raw)
	if err != nil {
		return 0, "", "", false, err
	}
	if family == "" {
		family = fmt.Sprintf("%d", userID)
	}
	if err := r.client.Set(ctx, "refresh_used:"+oldHash, family, ttl).Err(); err != nil {
		return 0, "", "", false, err
	}
	_ = r.client.SRem(ctx, "refresh_family:"+family, oldHash).Err()
	if err := r.SaveFamily(ctx, newHash, userID, username, family, ttl); err != nil {
		return 0, "", "", false, err
	}
	return userID, username, family, false, nil
}

func (r *RedisRefresh) RevokeUser(ctx context.Context, userID int64) error {
	families, err := r.client.SMembers(ctx, fmt.Sprintf("user_sessions:%d", userID)).Result()
	if err != nil {
		return err
	}
	for _, family := range families {
		if err := r.revokeFamily(ctx, family); err != nil {
			return err
		}
	}
	return r.client.Del(ctx, fmt.Sprintf("user_sessions:%d", userID)).Err()
}

func (r *RedisRefresh) revokeFamily(ctx context.Context, family string) error {
	hashes, err := r.client.SMembers(ctx, "refresh_family:"+family).Result()
	if err != nil {
		return err
	}
	keys := make([]string, 0, len(hashes)+1)
	keys = append(keys, "refresh_family:"+family)
	for _, hash := range hashes {
		keys = append(keys, "refresh:"+hash)
	}
	return r.client.Del(ctx, keys...).Err()
}

func parseRefresh(raw string) (int64, string, string, error) {
	userPart, rest, ok := strings.Cut(raw, "|")
	if !ok {
		return 0, "", "", apperr.Unauthorized("invalid refresh token")
	}
	username, family, found := strings.Cut(rest, "|")
	if !found {
		username = rest
	}
	userID, err := strconv.ParseInt(userPart, 10, 64)
	if err != nil {
		return 0, "", "", apperr.Unauthorized("invalid refresh token")
	}
	return userID, username, family, nil
}

func (l *RedisLimiter) Allow(ctx context.Context, key string) (bool, error) {
	if l.allow != nil {
		return l.allow(ctx, key)
	}
	n, err := l.client.Incr(ctx, "limit:"+key).Result()
	if err != nil {
		return false, err
	}
	if n == 1 {
		if err := l.client.Expire(ctx, "limit:"+key, l.window).Err(); err != nil {
			return false, err
		}
	}
	return n <= l.limit, nil
}
