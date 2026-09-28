package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"rokko.dev/pkg/apperr"
	"rokko.dev/pkg/eventx"
	"rokko.dev/pkg/id"
	"rokko.dev/pkg/outbox"
	"rokko.dev/services/media/internal/domain"
)

type Postgres struct {
	pool *pgxpool.Pool
	ids  *id.Generator
}

func NewPostgres(pool *pgxpool.Pool) *Postgres {
	return &Postgres{pool: pool, ids: id.New(32)}
}

func (p *Postgres) Insert(ctx context.Context, obj domain.Object) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
		INSERT INTO media (id, owner_id, bucket, object_key, filename, content_type, size, status)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
	`, obj.ID, obj.OwnerID, obj.Bucket, obj.ObjectKey, obj.Filename, obj.ContentType, obj.Size, obj.Status); err != nil {
		return err
	}
	if err := p.emit(ctx, tx, obj.ID, "media.created"); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (p *Postgres) Get(ctx context.Context, mediaID int64) (domain.Object, error) {
	var obj domain.Object
	err := p.pool.QueryRow(ctx, `
		SELECT id, owner_id, bucket, object_key, filename, content_type, size, status
		FROM media WHERE id=$1
	`, mediaID).Scan(&obj.ID, &obj.OwnerID, &obj.Bucket, &obj.ObjectKey, &obj.Filename, &obj.ContentType, &obj.Size, &obj.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Object{}, apperr.NotFound("media not found")
	}
	return obj, err
}

func (p *Postgres) MarkReady(ctx context.Context, mediaID int64) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `UPDATE media SET status=$2 WHERE id=$1`, mediaID, domain.StatusReady); err != nil {
		return err
	}
	if err := p.emit(ctx, tx, mediaID, "media.ready"); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (p *Postgres) emit(ctx context.Context, tx pgx.Tx, mediaID int64, typ string) error {
	eventID := p.ids.Next()
	env, err := eventx.New(id.Format(eventID), typ, map[string]string{"media_id": id.Format(mediaID)})
	if err != nil {
		return err
	}
	raw, err := env.Bytes()
	if err != nil {
		return err
	}
	return outbox.Insert(ctx, tx, eventID, "media.events", id.Format(mediaID), raw)
}
