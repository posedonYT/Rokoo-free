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
	userPart, rest, ok := strings.Cut(raw, "|")
	if !ok {
		return 0, "", apperr.Unauthorized("invalid refresh token")
	}
	username, _, _ := strings.Cut(rest, "|")
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
}

func NewRedisLimiter(client *redis.Client, limit int64, window time.Duration) *RedisLimiter {
	return &RedisLimiter{client: client, limit: limit, window: window}
}

func (l *RedisLimiter) Allow(ctx context.Context, key string) (bool, error) {
	return redisx.Allow(ctx, l.client, "limit:"+key, l.limit, l.window)
}

func (l *RedisLimiter) Reset(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	full := make([]string, len(keys))
	for i, k := range keys {
		full[i] = "limit:" + k
	}
	return l.client.Del(ctx, full...).Err()
}

func (r *RedisRefresh) SaveFamily(ctx context.Context, hash string, userID int64, username, family string, ttl time.Duration) error {
	pipe := r.client.TxPipeline()
	pipe.Set(ctx, "refresh:"+hash, fmt.Sprintf("%d|%s|%s", userID, username, family), ttl)
	pipe.SAdd(ctx, "refresh_family:"+family, hash)
	pipe.Expire(ctx, "refresh_family:"+family, ttl)
	pipe.SAdd(ctx, fmt.Sprintf("user_sessions:%d", userID), family)
	pipe.Expire(ctx, fmt.Sprintf("user_sessions:%d", userID), ttl)
	_, err := pipe.Exec(ctx)
	return err
}

const RefreshGrace = 10 * time.Second

const (
	rotateOK      = 0
	rotateGrace   = 1
	rotateReused  = 2
	rotateInvalid = 3
)

const rotateScript = `
local raw = redis.call('GET', KEYS[1])
if not raw then
  local grace = redis.call('GET', KEYS[3])
  if grace then
    local _, _, _, uid, uname, fam = string.find(grace, '^([^|]*)|([^|]*)|([^|]*)|(.*)$')
    if fam then
      local payload = uid .. '|' .. uname .. '|' .. fam
      redis.call('SET', KEYS[4], payload, 'PX', ARGV[3])
      redis.call('SADD', 'refresh_family:' .. fam, ARGV[2])
      redis.call('PEXPIRE', 'refresh_family:' .. fam, ARGV[3])
      return {1, uid, uname, fam}
    end
  end
  local used = redis.call('GET', KEYS[2])
  if used then
    return {2, '', '', used}
  end
  return {3, '', '', ''}
end
local _, _, uid, rest = string.find(raw, '^([^|]*)|(.*)$')
if not uid then
  return {3, '', '', ''}
end
local uname = rest
local fam = ''
local p = string.find(rest, '|', 1, true)
if p then
  uname = string.sub(rest, 1, p - 1)
  fam = string.sub(rest, p + 1)
end
if fam == '' then
  fam = uid
end
local payload = uid .. '|' .. uname .. '|' .. fam
redis.call('DEL', KEYS[1])
redis.call('SET', KEYS[2], fam, 'PX', ARGV[3])
redis.call('SET', KEYS[4], payload, 'PX', ARGV[3])
redis.call('SREM', 'refresh_family:' .. fam, ARGV[1])
redis.call('SADD', 'refresh_family:' .. fam, ARGV[2])
redis.call('PEXPIRE', 'refresh_family:' .. fam, ARGV[3])
redis.call('SADD', 'user_sessions:' .. uid, fam)
redis.call('PEXPIRE', 'user_sessions:' .. uid, ARGV[3])
redis.call('SET', KEYS[3], ARGV[2] .. '|' .. payload, 'PX', ARGV[4])
return {0, uid, uname, fam}
`

func (r *RedisRefresh) Rotate(ctx context.Context, oldHash, newHash string, ttl time.Duration) (int64, string, string, bool, error) {
	keys := []string{"refresh:" + oldHash, "refresh_used:" + oldHash, "refresh_grace:" + oldHash, "refresh:" + newHash}
	res, err := r.client.Eval(ctx, rotateScript, keys, oldHash, newHash, ttl.Milliseconds(), RefreshGrace.Milliseconds()).Slice()
	if err != nil {
		return 0, "", "", false, err
	}
	if len(res) != 4 {
		return 0, "", "", false, fmt.Errorf("unexpected rotate result")
	}
	status, _ := res[0].(int64)
	uidStr, _ := res[1].(string)
	username, _ := res[2].(string)
	family, _ := res[3].(string)
	switch status {
	case rotateOK, rotateGrace:
		userID, err := strconv.ParseInt(uidStr, 10, 64)
		if err != nil {
			return 0, "", "", false, apperr.Unauthorized("invalid refresh token")
		}
		return userID, username, family, false, nil
	case rotateReused:
		_ = r.revokeFamily(ctx, family)
		return 0, "", family, true, apperr.Unauthorized("refresh reused")
	default:
		return 0, "", "", false, apperr.Unauthorized("invalid refresh token")
	}
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
