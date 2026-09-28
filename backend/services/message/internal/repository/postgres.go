package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"rokko.dev/pkg/apperr"
	"rokko.dev/pkg/outbox"
	"rokko.dev/services/message/internal/domain"
)

type Postgres struct{ pool *pgxpool.Pool }

func NewPostgres(pool *pgxpool.Pool) *Postgres { return &Postgres{pool: pool} }

func (p *Postgres) FindPair(ctx context.Context, a, b int64) (domain.DMChannel, bool, error) {
	lo, hi := a, b
	if lo > hi {
		lo, hi = hi, lo
	}
	var channelID int64
	err := p.pool.QueryRow(ctx, `SELECT channel_id FROM dm_pairs WHERE user_lo=$1 AND user_hi=$2`, lo, hi).Scan(&channelID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.DMChannel{}, false, nil
	}
	if err != nil {
		return domain.DMChannel{}, false, err
	}
	ch, err := p.DM(ctx, channelID)
	return ch, err == nil, err
}

func (p *Postgres) CreateDM(ctx context.Context, ch domain.DMChannel) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `INSERT INTO dm_channels (id, kind) VALUES ($1,$2)`, ch.ID, ch.Kind); err != nil {
		return err
	}
	for _, member := range ch.Members {
		if _, err := tx.Exec(ctx, `INSERT INTO dm_members (channel_id, user_id) VALUES ($1,$2)`, ch.ID, member); err != nil {
			return err
		}
	}
	if ch.Kind == domain.KindDM && len(ch.Members) == 2 {
		lo, hi := ch.Members[0], ch.Members[1]
		if lo > hi {
			lo, hi = hi, lo
		}
		if _, err := tx.Exec(ctx, `INSERT INTO dm_pairs (user_lo, user_hi, channel_id) VALUES ($1,$2,$3)`, lo, hi, ch.ID); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				return apperr.Conflict("dm already exists")
			}
			return err
		}
	}
	return tx.Commit(ctx)
}

func (p *Postgres) DM(ctx context.Context, channelID int64) (domain.DMChannel, error) {
	var ch domain.DMChannel
	err := p.pool.QueryRow(ctx, `SELECT id, kind FROM dm_channels WHERE id=$1`, channelID).Scan(&ch.ID, &ch.Kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.DMChannel{}, apperr.NotFound("dm not found")
	}
	if err != nil {
		return domain.DMChannel{}, err
	}
	rows, err := p.pool.Query(ctx, `SELECT user_id FROM dm_members WHERE channel_id=$1 ORDER BY user_id`, channelID)
	if err != nil {
		return domain.DMChannel{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var userID int64
		if err := rows.Scan(&userID); err != nil {
			return domain.DMChannel{}, err
		}
		ch.Members = append(ch.Members, userID)
	}
	return ch, rows.Err()
}

func (p *Postgres) ListDMs(ctx context.Context, userID int64) ([]domain.DMChannel, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT c.id, c.kind, COALESCE(array_agg(m2.user_id ORDER BY m2.user_id), '{}')
		FROM dm_channels c
		JOIN dm_members m ON m.channel_id = c.id AND m.user_id = $1
		JOIN dm_members m2 ON m2.channel_id = c.id
		GROUP BY c.id, c.kind
		ORDER BY c.id DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.DMChannel
	for rows.Next() {
		var ch domain.DMChannel
		if err := rows.Scan(&ch.ID, &ch.Kind, &ch.Members); err != nil {
			return nil, err
		}
		out = append(out, ch)
	}
	return out, rows.Err()
}

func (p *Postgres) AddMember(ctx context.Context, channelID, userID int64) error {
	_, err := p.pool.Exec(ctx, `INSERT INTO dm_members (channel_id, user_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, channelID, userID)
	return err
}

func (p *Postgres) RemoveMember(ctx context.Context, channelID, userID int64) error {
	_, err := p.pool.Exec(ctx, `DELETE FROM dm_members WHERE channel_id=$1 AND user_id=$2`, channelID, userID)
	return err
}

func (p *Postgres) InsertMessage(ctx context.Context, msg domain.Message, event domain.Event) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
		INSERT INTO messages (id, channel_id, channel_kind, author_id, content)
		VALUES ($1,$2,$3,$4,$5)
	`, msg.ID, msg.ChannelID, msg.ChannelKind, msg.AuthorID, msg.Content); err != nil {
		return err
	}
	for _, att := range msg.Attachments {
		if _, err := tx.Exec(ctx, `
			INSERT INTO attachments (id, message_id, media_id, filename, content_type, size)
			VALUES ($1,$2,$3,$4,$5,$6)
		`, att.ID, msg.ID, att.MediaID, att.Filename, att.ContentType, att.Size); err != nil {
			return err
		}
	}
	if event.ID != 0 {
		if err := outbox.Insert(ctx, tx, event.ID, event.Topic, event.Key, event.Payload); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (p *Postgres) UpdateMessage(ctx context.Context, msg domain.Message, event domain.Event) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE messages SET content=$2, edited_at=now() WHERE id=$1 AND deleted_at IS NULL`, msg.ID, msg.Content)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("message not found")
	}
	if event.ID != 0 {
		if err := outbox.Insert(ctx, tx, event.ID, event.Topic, event.Key, event.Payload); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (p *Postgres) SoftDelete(ctx context.Context, messageID int64, event domain.Event) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE messages SET deleted_at=now() WHERE id=$1 AND deleted_at IS NULL`, messageID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("message not found")
	}
	if event.ID != 0 {
		if err := outbox.Insert(ctx, tx, event.ID, event.Topic, event.Key, event.Payload); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (p *Postgres) GetMessage(ctx context.Context, messageID int64) (domain.Message, error) {
	var msg domain.Message
	err := p.pool.QueryRow(ctx, `
		SELECT id, channel_id, channel_kind, author_id, content,
		       EXTRACT(EPOCH FROM created_at)::bigint,
		       COALESCE(EXTRACT(EPOCH FROM edited_at)::bigint, 0)
		FROM messages WHERE id=$1 AND deleted_at IS NULL
	`, messageID).Scan(&msg.ID, &msg.ChannelID, &msg.ChannelKind, &msg.AuthorID, &msg.Content, &msg.CreatedAt, &msg.EditedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Message{}, apperr.NotFound("message not found")
	}
	if err != nil {
		return domain.Message{}, err
	}
	msg.Attachments, err = p.attachments(ctx, []int64{msg.ID})
	return msg, err
}

func (p *Postgres) ListMessages(ctx context.Context, channelID int64, before int64, limit int) ([]domain.Message, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT id, channel_id, channel_kind, author_id, content,
		       EXTRACT(EPOCH FROM created_at)::bigint,
		       COALESCE(EXTRACT(EPOCH FROM edited_at)::bigint, 0)
		FROM messages
		WHERE channel_id=$1 AND deleted_at IS NULL AND ($2::bigint = 0 OR id < $2)
		ORDER BY id DESC
		LIMIT $3
	`, channelID, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Message
	var ids []int64
	for rows.Next() {
		var msg domain.Message
		if err := rows.Scan(&msg.ID, &msg.ChannelID, &msg.ChannelKind, &msg.AuthorID, &msg.Content, &msg.CreatedAt, &msg.EditedAt); err != nil {
			return nil, err
		}
		out = append(out, msg)
		ids = append(ids, msg.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	atts, err := p.attachments(ctx, ids)
	if err != nil {
		return nil, err
	}
	byMsg := map[int64][]domain.Attachment{}
	for _, att := range atts {
		byMsg[att.MessageID] = append(byMsg[att.MessageID], att)
	}
	for i := range out {
		out[i].Attachments = byMsg[out[i].ID]
	}
	return out, nil
}

func (p *Postgres) attachments(ctx context.Context, messageIDs []int64) ([]domain.Attachment, error) {
	if len(messageIDs) == 0 {
		return nil, nil
	}
	rows, err := p.pool.Query(ctx, `
		SELECT id, message_id, media_id, filename, content_type, size
		FROM attachments WHERE message_id = ANY($1)
	`, messageIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Attachment
	for rows.Next() {
		var att domain.Attachment
		var messageID int64
		if err := rows.Scan(&att.ID, &messageID, &att.MediaID, &att.Filename, &att.ContentType, &att.Size); err != nil {
			return nil, err
		}
		att.MessageID = messageID
		out = append(out, att)
	}
	return out, rows.Err()
}
