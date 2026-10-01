package outbox

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/segmentio/kafka-go"
)

type Message struct {
	ID      int64
	Topic   string
	Key     string
	Payload []byte
}

type Lag struct {
	Count int
	Age   time.Duration
}

func Insert(ctx context.Context, tx pgx.Tx, id int64, topic, key string, payload []byte) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO outbox (id, topic, key, payload)
		VALUES ($1, $2, $3, $4)
	`, id, topic, key, payload)
	return err
}

const (
	leaseDuration  = 30 * time.Second
	writeTimeout   = 10 * time.Second
	cleanupEvery   = time.Minute
	lockRetryEvery = 5 * time.Second
	relayLockKey int64 = 0x6f757462
)

type messageWriter interface {
	WriteMessages(ctx context.Context, msgs ...kafka.Message) error
}

func claimBatch(ctx context.Context, pool *pgxpool.Pool, limit int) ([]Message, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, `
		UPDATE outbox SET locked_until = now() + make_interval(secs => $2)
		WHERE id IN (
			SELECT id FROM outbox
			WHERE published_at IS NULL AND (locked_until IS NULL OR locked_until < now())
			ORDER BY id
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		)
		RETURNING id, topic, key, payload
	`, limit, leaseDuration.Seconds())
	if err != nil {
		return nil, err
	}
	var batch []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.Topic, &m.Key, &m.Payload); err != nil {
			rows.Close()
			return nil, err
		}
		batch = append(batch, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	sort.Slice(batch, func(i, j int) bool { return batch[i].ID < batch[j].ID })
	return batch, nil
}

func toKafka(batch []Message) ([]kafka.Message, []int64) {
	msgs := make([]kafka.Message, 0, len(batch))
	ids := make([]int64, 0, len(batch))
	for _, m := range batch {
		msgs = append(msgs, kafka.Message{
			Topic:   m.Topic,
			Key:     []byte(m.Key),
			Value:   m.Payload,
			Headers: []kafka.Header{{Key: "event_id", Value: []byte(eventID(m.Payload))}},
		})
		ids = append(ids, m.ID)
	}
	return msgs, ids
}

func writeBatch(ctx context.Context, w messageWriter, batch []Message) ([]int64, error) {
	msgs, ids := toKafka(batch)
	wctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	if err := w.WriteMessages(wctx, msgs...); err != nil {
		return nil, err
	}
	return ids, nil
}

func PublishPending(ctx context.Context, pool *pgxpool.Pool, w *kafka.Writer, limit int) (int, error) {
	return publish(ctx, pool, w, limit)
}

func publish(ctx context.Context, pool *pgxpool.Pool, w messageWriter, limit int) (int, error) {
	batch, err := claimBatch(ctx, pool, limit)
	if err != nil {
		return 0, err
	}
	if len(batch) == 0 {
		return 0, nil
	}
	ids, err := writeBatch(ctx, w, batch)
	if err != nil {
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_, _ = pool.Exec(rctx, `UPDATE outbox SET locked_until = NULL WHERE id = ANY($1) AND published_at IS NULL`, claimedIDs(batch))
		return 0, err
	}
	uctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if _, err := pool.Exec(uctx, `UPDATE outbox SET published_at = now() WHERE id = ANY($1)`, ids); err != nil {
		return 0, err
	}
	return len(batch), nil
}

func claimedIDs(batch []Message) []int64 {
	ids := make([]int64, len(batch))
	for i, m := range batch {
		ids[i] = m.ID
	}
	return ids
}

func LagStats(ctx context.Context, pool *pgxpool.Pool) (Lag, error) {
	var count int
	var oldest *time.Time
	err := pool.QueryRow(ctx, `
		SELECT count(*), min(created_at)
		FROM outbox
		WHERE published_at IS NULL
	`).Scan(&count, &oldest)
	if err != nil {
		return Lag{}, err
	}
	lag := Lag{Count: count}
	if oldest != nil {
		lag.Age = time.Since(*oldest)
	}
	return lag, nil
}

func Cleanup(ctx context.Context, pool *pgxpool.Pool, retention time.Duration, limit int) (int, error) {
	if retention <= 0 {
		retention = 7 * 24 * time.Hour
	}
	if limit <= 0 {
		limit = 500
	}
	tag, err := pool.Exec(ctx, `
		DELETE FROM outbox
		WHERE id IN (
			SELECT id FROM outbox
			WHERE published_at IS NOT NULL AND published_at < now() - make_interval(secs => $1)
			LIMIT $2
		)
	`, int(retention.Seconds()), limit)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

func RunRelay(ctx context.Context, pool *pgxpool.Pool, w *kafka.Writer) {
	RunRelayLogged(ctx, pool, w, func(error) {})
}

func RunRelayLogged(ctx context.Context, pool *pgxpool.Pool, w *kafka.Writer, log func(error)) {
	if log == nil {
		log = func(error) {}
	}
	for ctx.Err() == nil {
		conn, err := pool.Acquire(ctx)
		if err != nil {
			if ctx.Err() == nil {
				log(err)
			}
			sleepCtx(ctx, lockRetryEvery)
			continue
		}
		var locked bool
		if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, relayLockKey).Scan(&locked); err != nil || !locked {
			conn.Release()
			if err != nil && ctx.Err() == nil {
				log(err)
			}
			sleepCtx(ctx, lockRetryEvery)
			continue
		}
		runLocked(ctx, pool, conn, w, log)
		unlockCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		if _, err := conn.Exec(unlockCtx, `SELECT pg_advisory_unlock($1)`, relayLockKey); err != nil {
			_ = conn.Conn().Close(unlockCtx)
		}
		cancel()
		conn.Release()
	}
}

func runLocked(ctx context.Context, pool *pgxpool.Pool, lockConn *pgxpool.Conn, w messageWriter, log func(error)) {
	const minWait = 500 * time.Millisecond
	wait := minWait
	t := time.NewTimer(wait)
	defer t.Stop()
	cleanup := time.NewTicker(cleanupEvery)
	defer cleanup.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-cleanup.C:
			if _, err := Cleanup(ctx, pool, 7*24*time.Hour, 200); err != nil && ctx.Err() == nil {
				log(err)
			}
		case <-t.C:
			if err := lockConn.Ping(ctx); err != nil {
				if ctx.Err() == nil {
					log(err)
				}
				return
			}
			_, err := publish(ctx, pool, w, 100)
			if err != nil {
				if ctx.Err() == nil {
					log(err)
				}
				if wait < 10*time.Second {
					wait *= 2
				}
			} else {
				wait = minWait
			}
			t.Reset(wait)
		}
	}
}

func sleepCtx(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

func eventID(payload []byte) string {
	var env struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(payload, &env) != nil {
		return ""
	}
	return env.ID
}
