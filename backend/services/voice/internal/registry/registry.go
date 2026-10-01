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

const releaseScript = `
if redis.call('GET', KEYS[1]) == ARGV[1] then
  redis.call('DEL', KEYS[1], KEYS[2])
  return 1
end
return 0
`

const upsertNodeScript = `
redis.call('SET', KEYS[1], ARGV[1], 'PX', ARGV[2])
redis.call('ZADD', KEYS[2], ARGV[3], ARGV[4])
return 1
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
	_, err := r.rdb.Eval(ctx, releaseScript, []string{"voice:room:" + room, "voice:participants:" + room}, nodeID).Result()
	return err
}

func (r *Registry) UpsertNode(ctx context.Context, n Node) error {
	raw, err := json.Marshal(n)
	if err != nil {
		return err
	}
	exp := time.Now().Add(r.ttl).UnixMilli()
	_, err = r.rdb.Eval(ctx, upsertNodeScript,
		[]string{"voice:node:" + n.ID, "voice:nodes"},
		string(raw), r.ttl.Milliseconds(), exp, n.ID,
	).Result()
	return err
}

func (r *Registry) Nodes(ctx context.Context) ([]Node, error) {
	now := time.Now().UnixMilli()
	_ = r.rdb.ZRemRangeByScore(ctx, "voice:nodes", "-inf", fmt.Sprintf("%d", now-1)).Err()
	ids, err := r.rdb.ZRangeByScore(ctx, "voice:nodes", &redis.ZRangeBy{
		Min: fmt.Sprintf("%d", now),
		Max: "+inf",
	}).Result()
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil
	}
	keys := make([]string, len(ids))
	for i, id := range ids {
		keys[i] = "voice:node:" + id
	}
	vals, err := r.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}
	out := make([]Node, 0, len(vals))
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
	return out, nil
}

func (r *Registry) SetParticipants(ctx context.Context, room string, ids []string) error {
	key := "voice:participants:" + room
	pipe := r.rdb.TxPipeline()
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
