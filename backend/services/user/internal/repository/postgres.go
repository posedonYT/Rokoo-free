package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"rokko.dev/pkg/apperr"
	"rokko.dev/pkg/eventx"
	"rokko.dev/pkg/id"
	"rokko.dev/pkg/outbox"
	"rokko.dev/services/user/internal/domain"
)

type Postgres struct {
	pool *pgxpool.Pool
	ids  *id.Generator
}

func NewPostgres(pool *pgxpool.Pool) *Postgres {
	return &Postgres{pool: pool, ids: id.New(31)}
}

func (p *Postgres) InTx(ctx context.Context, fn func(Tx) error) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := fn(&pgTx{tx: tx}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (p *Postgres) InsertProfile(ctx context.Context, profile domain.Profile) error {
	_, err := p.pool.Exec(ctx, `
		INSERT INTO profiles (id, username, discriminator, display_name)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (id) DO NOTHING
	`, profile.ID, profile.Username, profile.Discriminator, profile.DisplayName)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return apperr.Conflict("discriminator taken")
		}
		return err
	}
	return nil
}

func (p *Postgres) UpdateProfile(ctx context.Context, profile domain.Profile) (domain.Profile, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return domain.Profile{}, err
	}
	defer tx.Rollback(ctx)
	var out domain.Profile
	err = tx.QueryRow(ctx, `
		UPDATE profiles SET display_name=$2, avatar_url=$3, status_text=$4
		WHERE id=$1
		RETURNING id, username, discriminator, display_name, avatar_url, status_text
	`, profile.ID, profile.DisplayName, profile.AvatarURL, profile.StatusText).Scan(
		&out.ID, &out.Username, &out.Discriminator, &out.DisplayName, &out.AvatarURL, &out.StatusText,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Profile{}, apperr.NotFound("profile not found")
	}
	if err != nil {
		return domain.Profile{}, err
	}
	eventID := p.ids.Next()
	env, err := eventx.New(id.Format(eventID), "profile.updated", map[string]string{"user_id": id.Format(out.ID)})
	if err != nil {
		return domain.Profile{}, err
	}
	raw, err := env.Bytes()
	if err != nil {
		return domain.Profile{}, err
	}
	if err := outbox.Insert(ctx, tx, eventID, domain.Topic, id.Format(out.ID), raw); err != nil {
		return domain.Profile{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Profile{}, err
	}
	return out, nil
}

func (p *Postgres) GetProfile(ctx context.Context, userID int64) (domain.Profile, error) {
	var out domain.Profile
	err := p.pool.QueryRow(ctx, `
		SELECT id, username, discriminator, display_name, avatar_url, status_text
		FROM profiles WHERE id=$1
	`, userID).Scan(&out.ID, &out.Username, &out.Discriminator, &out.DisplayName, &out.AvatarURL, &out.StatusText)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Profile{}, apperr.NotFound("profile not found")
	}
	return out, err
}

func (p *Postgres) Search(ctx context.Context, query string, limit int) ([]domain.Profile, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT id, username, discriminator, display_name, avatar_url, status_text
		FROM profiles
		WHERE lower(username) LIKE lower($1) || '%' OR username || '#' || discriminator = $2
		ORDER BY username, discriminator
		LIMIT $3
	`, query, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanProfiles(rows)
}

func (p *Postgres) ListByRel(ctx context.Context, userID int64, relType string) ([]domain.Profile, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT p.id, p.username, p.discriminator, p.display_name, p.avatar_url, p.status_text
		FROM relationships r
		JOIN profiles p ON p.id = r.target_id
		WHERE r.user_id=$1 AND r.type=$2
		ORDER BY p.username
	`, userID, relType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanProfiles(rows)
}

func (p *Postgres) FriendIDs(ctx context.Context, userID int64) ([]int64, error) {
	rows, err := p.pool.Query(ctx, `SELECT target_id FROM relationships WHERE user_id=$1 AND type=$2`, userID, domain.RelFriend)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (p *Postgres) IsBlocked(ctx context.Context, a, b int64) (bool, error) {
	var blocked bool
	err := p.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM relationships
			WHERE type=$3 AND ((user_id=$1 AND target_id=$2) OR (user_id=$2 AND target_id=$1))
		)
	`, a, b, domain.RelBlocked).Scan(&blocked)
	return blocked, err
}

type pgTx struct{ tx pgx.Tx }

func (t *pgTx) GetRel(ctx context.Context, userID, targetID int64) (string, error) {
	var typ string
	err := t.tx.QueryRow(ctx, `SELECT type FROM relationships WHERE user_id=$1 AND target_id=$2`, userID, targetID).Scan(&typ)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return typ, err
}

func (t *pgTx) PutRel(ctx context.Context, userID, targetID int64, relType string) error {
	_, err := t.tx.Exec(ctx, `
		INSERT INTO relationships (user_id, target_id, type) VALUES ($1,$2,$3)
		ON CONFLICT (user_id, target_id) DO UPDATE SET type=EXCLUDED.type
	`, userID, targetID, relType)
	return err
}

func (t *pgTx) DelRel(ctx context.Context, userID, targetID int64) error {
	_, err := t.tx.Exec(ctx, `DELETE FROM relationships WHERE user_id=$1 AND target_id=$2`, userID, targetID)
	return err
}

func (t *pgTx) AddEvent(ctx context.Context, event domain.Event) error {
	return outbox.Insert(ctx, t.tx, event.ID, event.Topic, event.Key, event.Payload)
}

func scanProfiles(rows pgx.Rows) ([]domain.Profile, error) {
	var out []domain.Profile
	for rows.Next() {
		var p domain.Profile
		if err := rows.Scan(&p.ID, &p.Username, &p.Discriminator, &p.DisplayName, &p.AvatarURL, &p.StatusText); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
