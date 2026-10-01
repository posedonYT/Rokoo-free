package state

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

type Redis struct {
	client *redis.Client
	now    func() time.Time
}

func New(client *redis.Client) *Redis { return &Redis{client: client} }

func (r *Redis) Client() *redis.Client { return r.client }

const keyTTL = 2 * time.Minute

func member(userID, connID string) string { return userID + ":" + connID }

func userOf(m string) string {
	if i := strings.Index(m, ":"); i >= 0 {
		return m[:i]
	}
	return m
}

func (r *Redis) Subscribe(ctx context.Context, userID, connID string, guilds, dms []string) error {
	return r.touch(ctx, userID, connID, guilds, dms, 90*time.Second, false)
}

func (r *Redis) Touch(ctx context.Context, userID, connID string, guilds, dms []string, ttl time.Duration) error {
	return r.touch(ctx, userID, connID, guilds, dms, ttl, true)
}

func (r *Redis) touch(ctx context.Context, userID, connID string, guilds, dms []string, ttl time.Duration, presence bool) error {
	if ttl <= 0 {
		ttl = 90 * time.Second
	}
	now := r.clock()
	exp := float64(now.Add(ttl).UnixMilli())
	nowScore := formatScore(float64(now.UnixMilli()))
	m := member(userID, connID)
	pipe := r.client.Pipeline()
	add := func(key string) {
		pipe.ZAdd(ctx, key, redis.Z{Score: exp, Member: m})
		pipe.ZRemRangeByScore(ctx, key, "-inf", nowScore)
		pipe.Expire(ctx, key, keyTTL)
	}
	for _, guildID := range guilds {
		add("subs:guild:" + guildID)
	}
	for _, dmID := range dms {
		add("subs:dm:" + dmID)
	}
	if presence && connID != "" {
		pipe.ZAdd(ctx, "presence:"+userID, redis.Z{Score: exp, Member: connID})
		pipe.ZRemRangeByScore(ctx, "presence:"+userID, "-inf", nowScore)
		pipe.Expire(ctx, "presence:"+userID, ttl)
	}
	_, err := pipe.Exec(ctx)
	return err
}

func (r *Redis) Unsubscribe(ctx context.Context, userID, connID string, guilds, dms []string) error {
	m := member(userID, connID)
	pipe := r.client.Pipeline()
	for _, guildID := range guilds {
		pipe.ZRem(ctx, "subs:guild:"+guildID, m)
	}
	for _, dmID := range dms {
		pipe.ZRem(ctx, "subs:dm:"+dmID, m)
	}
	_, err := pipe.Exec(ctx)
	return err
}

func (r *Redis) GuildUsers(ctx context.Context, guildID string) ([]string, error) {
	return r.live(ctx, "subs:guild:"+guildID)
}

func (r *Redis) DMUsers(ctx context.Context, channelID string) ([]string, error) {
	return r.live(ctx, "subs:dm:"+channelID)
}

func (r *Redis) live(ctx context.Context, key string) ([]string, error) {
	now := float64(r.clock().UnixMilli())
	members, err := r.client.ZRangeByScore(ctx, key, &redis.ZRangeBy{Min: formatScore(now), Max: "+inf"}).Result()
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(members))
	out := make([]string, 0, len(members))
	for _, m := range members {
		u := userOf(m)
		if _, ok := seen[u]; ok {
			continue
		}
		seen[u] = struct{}{}
		out = append(out, u)
	}
	return out, nil
}

func (r *Redis) Sweep(ctx context.Context, keys []string) error {
	now := formatScore(float64(r.clock().UnixMilli()))
	pipe := r.client.Pipeline()
	for _, key := range keys {
		pipe.ZRemRangeByScore(ctx, key, "-inf", now)
	}
	_, err := pipe.Exec(ctx)
	return err
}

func (r *Redis) SweepAll(ctx context.Context) error {
	for _, pattern := range []string{"subs:guild:*", "subs:dm:*", "presence:*"} {
		var keys []string
		iter := r.client.Scan(ctx, 0, pattern, 200).Iterator()
		for iter.Next(ctx) {
			keys = append(keys, iter.Val())
		}
		if err := iter.Err(); err != nil {
			return err
		}
		for i := 0; i < len(keys); i += 200 {
			end := i + 200
			if end > len(keys) {
				end = len(keys)
			}
			if err := r.Sweep(ctx, keys[i:end]); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *Redis) SetOnline(ctx context.Context, userID, connID string, ttl time.Duration) error {
	return r.Touch(ctx, userID, connID, nil, nil, ttl)
}

func (r *Redis) SetOffline(ctx context.Context, userID, connID string) error {
	if err := r.client.ZRem(ctx, "presence:"+userID, connID).Err(); err != nil {
		return err
	}
	n, err := r.client.ZCard(ctx, "presence:"+userID).Result()
	if err != nil {
		return err
	}
	if n == 0 {
		return r.client.Del(ctx, "presence:"+userID).Err()
	}
	return nil
}

func (r *Redis) Online(ctx context.Context, ids []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(ids) == 0 {
		return out, nil
	}
	now := float64(r.clock().UnixMilli())
	pipe := r.client.Pipeline()
	cmds := make([]*redis.IntCmd, len(ids))
	for i, id := range ids {
		cmds[i] = pipe.ZCount(ctx, "presence:"+id, formatScore(now), "+inf")
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, err
	}
	for i, cmd := range cmds {
		n, err := cmd.Result()
		if err != nil {
			return nil, err
		}
		out[ids[i]] = n > 0
	}
	return out, nil
}

func (r *Redis) Publish(ctx context.Context, userID string, payload []byte) error {
	return r.client.Publish(ctx, "rt:user:"+userID, payload).Err()
}

func (r *Redis) Seen(ctx context.Context, eventID string) (bool, error) {
	if eventID == "" {
		return false, nil
	}
	n, err := r.client.Exists(ctx, "event:"+eventID).Result()
	return n > 0, err
}

func (r *Redis) Once(ctx context.Context, eventID string, ttl time.Duration) (bool, error) {
	if eventID == "" {
		return true, nil
	}
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	return r.client.SetNX(ctx, "event:"+eventID, "1", ttl).Result()
}

func (r *Redis) clock() time.Time {
	if r.now != nil {
		return r.now()
	}
	return time.Now()
}

func formatScore(v float64) string {
	return strconv.FormatInt(int64(v), 10)
}
