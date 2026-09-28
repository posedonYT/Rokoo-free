package outbox

import (
	"context"
	"encoding/json"
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

func PublishPending(ctx context.Context, pool *pgxpool.Pool, w *kafka.Writer, limit int) (int, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, `
		SELECT id, topic, key, payload
		FROM outbox
		WHERE published_at IS NULL
		ORDER BY id
		LIMIT $1
		FOR UPDATE SKIP LOCKED
	`, limit)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	var batch []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.Topic, &m.Key, &m.Payload); err != nil {
			return 0, err
		}
		batch = append(batch, m)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(batch) == 0 {
		return 0, nil
	}

	msgs := make([]kafka.Message, 0, len(batch))
	ids := make([]int64, 0, len(batch))
	for _, m := range batch {
		msg := kafka.Message{Topic: m.Topic, Key: []byte(m.Key), Value: m.Payload, Headers: []kafka.Header{{Key: "event_id", Value: []byte(eventID(m.Payload))}}}
		msgs = append(msgs, msg)
		ids = append(ids, m.ID)
	}
	if err := w.WriteMessages(ctx, msgs...); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `UPDATE outbox SET published_at = now() WHERE id = ANY($1)`, ids); err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return len(batch), nil
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
	wait := 500 * time.Millisecond
	t := time.NewTimer(wait)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			n, err := PublishPending(ctx, pool, w, 100)
			if err != nil {
				log(err)
				if wait < 10*time.Second {
					wait *= 2
				}
			} else {
				wait = 500 * time.Millisecond
				if n == 0 {
					if _, cerr := Cleanup(ctx, pool, 7*24*time.Hour, 200); cerr != nil {
						log(cerr)
					}
				}
			}
			t.Reset(wait)
		}
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
