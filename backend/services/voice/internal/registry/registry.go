package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const claimScript = `
local cur = redis.call('GET', KEYS[1])
if not cur then
  redis.call('SET', KEYS[1], ARGV[1], 'PX', ARGV[2])
  return ARGV[1]
end
return cur
`

const refreshScript = `
if redis.call('GET', KEYS[1]) == ARGV[1] then
  redis.call('PEXPIRE', KEYS[1], ARGV[2])
  return 1
end
return 0
`

type Node struct {
	ID       string `json:"id"`
	URL      string `json:"url"`
	Load     int    `json:"load"`
	Draining bool   `json:"draining"`
}

type Registry struct {
	rdb *redis.Client
	ttl time.Duration
}

func New(rdb *redis.Client, ttl time.Duration) *Registry {
	if ttl <= 0 {
		ttl = 15 * time.Second
	}
	return &Registry{rdb: rdb, ttl: ttl}
}

func (r *Registry) Claim(ctx context.Context, room, nodeID string) (string, error) {
	owner, err := r.rdb.Eval(ctx, claimScript, []string{"voice:room:" + room}, nodeID, r.ttl.Milliseconds()).Result()
	if err != nil {
		return "", err
	}
	return fmt.Sprint(owner), nil
}

func (r *Registry) Refresh(ctx context.Context, room, nodeID string) (bool, error) {
	n, err := r.rdb.Eval(ctx, refreshScript, []string{"voice:room:" + room}, nodeID, r.ttl.Milliseconds()).Int()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

func (r *Registry) Release(ctx context.Context, room, nodeID string) error {
	ok, err := r.Refresh(ctx, room, nodeID)
	if err != nil || !ok {
		return err
	}
	return r.rdb.Del(ctx, "voice:room:"+room, "voice:participants:"+room).Err()
}

func (r *Registry) UpsertNode(ctx context.Context, n Node) error {
	raw, err := json.Marshal(n)
	if err != nil {
		return err
	}
	return r.rdb.Set(ctx, "voice:node:"+n.ID, raw, r.ttl).Err()
}

func (r *Registry) Nodes(ctx context.Context) ([]Node, error) {
	var cursor uint64
	var out []Node
	for {
		keys, next, err := r.rdb.Scan(ctx, cursor, "voice:node:*", 64).Result()
		if err != nil {
			return nil, err
		}
		if len(keys) > 0 {
			vals, err := r.rdb.MGet(ctx, keys...).Result()
			if err != nil {
				return nil, err
			}
			for _, val := range vals {
				s, ok := val.(string)
				if !ok || s == "" {
					continue
				}
				var n Node
				if json.Unmarshal([]byte(s), &n) == nil && n.ID != "" {
					out = append(out, n)
				}
			}
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	return out, nil
}

func (r *Registry) SetParticipants(ctx context.Context, room string, ids []string) error {
	key := "voice:participants:" + room
	pipe := r.rdb.Pipeline()
	pipe.Del(ctx, key)
	if len(ids) > 0 {
		members := make([]any, len(ids))
		for i, id := range ids {
			members[i] = id
		}
		pipe.SAdd(ctx, key, members...)
		pipe.Expire(ctx, key, r.ttl)
	}
	_, err := pipe.Exec(ctx)
	return err
}

func (r *Registry) Participants(ctx context.Context, room string) ([]string, error) {
	return r.rdb.SMembers(ctx, "voice:participants:"+room).Result()
}

func (r *Registry) Owner(ctx context.Context, room string) (string, error) {
	owner, err := r.rdb.Get(ctx, "voice:room:"+room).Result()
	if err == redis.Nil {
		return "", nil
	}
	return owner, err
}
