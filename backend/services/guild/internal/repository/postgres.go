package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"rokko.dev/pkg/apperr"
	"rokko.dev/pkg/eventx"
	"rokko.dev/pkg/id"
	"rokko.dev/pkg/outbox"
	"rokko.dev/services/guild/internal/domain"
)

type Postgres struct {
	pool *pgxpool.Pool
	ids  *id.Generator
}

func NewPostgres(pool *pgxpool.Pool) *Postgres {
	return &Postgres{pool: pool, ids: id.New(21)}
}

func (p *Postgres) CreateGuild(ctx context.Context, g domain.Guild, channels []domain.Channel) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `INSERT INTO guilds (id, name, icon_url, owner_id) VALUES ($1,$2,$3,$4)`, g.ID, g.Name, g.IconURL, g.OwnerID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO members (guild_id, user_id, role) VALUES ($1,$2,$3)`, g.ID, g.OwnerID, domain.RoleOwner); err != nil {
		return err
	}
	for _, ch := range channels {
		if _, err := tx.Exec(ctx, `INSERT INTO channels (id, guild_id, name, type, position) VALUES ($1,$2,$3,$4,$5)`, ch.ID, ch.GuildID, ch.Name, ch.Type, ch.Position); err != nil {
			return err
		}
	}
	if err := p.emit(ctx, tx, "guild.created", g.ID, map[string]string{"guild_id": id.Format(g.ID), "name": g.Name}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (p *Postgres) UpdateGuild(ctx context.Context, g domain.Guild) error {
	return p.inTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE guilds SET name=$2, icon_url=$3 WHERE id=$1`, g.ID, g.Name, g.IconURL)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return apperr.NotFound("guild not found")
		}
		return p.emit(ctx, tx, "guild.updated", g.ID, map[string]string{"guild_id": id.Format(g.ID), "name": g.Name})
	})
}

func (p *Postgres) DeleteGuild(ctx context.Context, guildID int64) error {
	return p.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM guilds WHERE id=$1`, guildID); err != nil {
			return err
		}
		return p.emit(ctx, tx, "guild.deleted", guildID, map[string]string{"guild_id": id.Format(guildID)})
	})
}

func (p *Postgres) GetGuild(ctx context.Context, guildID int64) (domain.Guild, error) {
	var g domain.Guild
	err := p.pool.QueryRow(ctx, `SELECT id, name, icon_url, owner_id FROM guilds WHERE id=$1`, guildID).Scan(&g.ID, &g.Name, &g.IconURL, &g.OwnerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Guild{}, apperr.NotFound("guild not found")
	}
	return g, err
}

func (p *Postgres) ListGuilds(ctx context.Context, userID int64) ([]domain.Guild, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT g.id, g.name, g.icon_url, g.owner_id, m.role
		FROM members m JOIN guilds g ON g.id = m.guild_id
		WHERE m.user_id=$1 ORDER BY g.id
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Guild
	for rows.Next() {
		var g domain.Guild
		if err := rows.Scan(&g.ID, &g.Name, &g.IconURL, &g.OwnerID, &g.MyRole); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (p *Postgres) Role(ctx context.Context, guildID, userID int64) (string, error) {
	var role string
	err := p.pool.QueryRow(ctx, `SELECT role FROM members WHERE guild_id=$1 AND user_id=$2`, guildID, userID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", apperr.Forbidden("not a member")
	}
	return role, err
}

func (p *Postgres) SaveChannel(ctx context.Context, ch domain.Channel) error {
	return p.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO channels (id, guild_id, name, type, position) VALUES ($1,$2,$3,$4,$5)
			ON CONFLICT (id) DO UPDATE SET name=EXCLUDED.name, position=EXCLUDED.position
		`, ch.ID, ch.GuildID, ch.Name, ch.Type, ch.Position); err != nil {
			return err
		}
		return p.emit(ctx, tx, "channel.saved", ch.GuildID, map[string]string{
			"guild_id": id.Format(ch.GuildID), "channel_id": id.Format(ch.ID), "name": ch.Name, "type": ch.Type,
		})
	})
}

func (p *Postgres) DeleteChannel(ctx context.Context, channelID int64) error {
	return p.inTx(ctx, func(tx pgx.Tx) error {
		var guildID int64
		err := tx.QueryRow(ctx, `DELETE FROM channels WHERE id=$1 RETURNING guild_id`, channelID).Scan(&guildID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		return p.emit(ctx, tx, "channel.deleted", guildID, map[string]string{"guild_id": id.Format(guildID), "channel_id": id.Format(channelID)})
	})
}

func (p *Postgres) Channel(ctx context.Context, channelID int64) (domain.Channel, error) {
	var ch domain.Channel
	err := p.pool.QueryRow(ctx, `SELECT id, guild_id, name, type, position FROM channels WHERE id=$1`, channelID).
		Scan(&ch.ID, &ch.GuildID, &ch.Name, &ch.Type, &ch.Position)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Channel{}, apperr.NotFound("channel not found")
	}
	return ch, err
}

func (p *Postgres) Channels(ctx context.Context, guildID int64) ([]domain.Channel, error) {
	rows, err := p.pool.Query(ctx, `SELECT id, guild_id, name, type, position FROM channels WHERE guild_id=$1 ORDER BY position, id`, guildID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Channel
	for rows.Next() {
		var ch domain.Channel
		if err := rows.Scan(&ch.ID, &ch.GuildID, &ch.Name, &ch.Type, &ch.Position); err != nil {
			return nil, err
		}
		out = append(out, ch)
	}
	return out, rows.Err()
}

func (p *Postgres) Reorder(ctx context.Context, guildID int64, ids []int64) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `
		UPDATE channels AS c
		SET position = u.pos - 1
		FROM unnest($1::bigint[]) WITH ORDINALITY AS u(id, pos)
		WHERE c.id = u.id AND c.guild_id = $2
	`, ids, guildID)
	if err != nil {
		return err
	}
	if int(tag.RowsAffected()) != len(ids) {
		return apperr.Invalid("channel does not belong to guild")
	}
	return tx.Commit(ctx)
}

func (p *Postgres) SaveInvite(ctx context.Context, inv domain.Invite) error {
	_, err := p.pool.Exec(ctx, `
		INSERT INTO invites (code, guild_id, creator_id, max_uses, uses, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6)
	`, inv.Code, inv.GuildID, inv.CreatorID, inv.MaxUses, inv.Uses, inv.ExpiresAt)
	return err
}

func (p *Postgres) ConsumeInvite(ctx context.Context, code string, userID int64, now time.Time) (domain.Guild, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return domain.Guild{}, err
	}
	defer tx.Rollback(ctx)
	var inv domain.Invite
	err = tx.QueryRow(ctx, `
		SELECT code, guild_id, creator_id, max_uses, uses, expires_at
		FROM invites WHERE code=$1 FOR UPDATE
	`, code).Scan(&inv.Code, &inv.GuildID, &inv.CreatorID, &inv.MaxUses, &inv.Uses, &inv.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Guild{}, apperr.NotFound("invite not found")
	}
	if err != nil {
		return domain.Guild{}, err
	}
	if inv.ExpiresAt != nil && now.After(*inv.ExpiresAt) {
		return domain.Guild{}, apperr.Invalid("invite expired")
	}
	if inv.MaxUses > 0 && inv.Uses >= inv.MaxUses {
		return domain.Guild{}, apperr.Invalid("invite exhausted")
	}
	var role string
	err = tx.QueryRow(ctx, `SELECT role FROM members WHERE guild_id=$1 AND user_id=$2`, inv.GuildID, userID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		if _, err := tx.Exec(ctx, `INSERT INTO members (guild_id, user_id, role) VALUES ($1,$2,$3)`, inv.GuildID, userID, domain.RoleMember); err != nil {
			return domain.Guild{}, err
		}
		if _, err := tx.Exec(ctx, `UPDATE invites SET uses=uses+1 WHERE code=$1`, code); err != nil {
			return domain.Guild{}, err
		}
		role = domain.RoleMember
		if err := p.emit(ctx, tx, "member.joined", inv.GuildID, map[string]string{"guild_id": id.Format(inv.GuildID), "user_id": id.Format(userID)}); err != nil {
			return domain.Guild{}, err
		}
	} else if err != nil {
		return domain.Guild{}, err
	}
	var g domain.Guild
	if err := tx.QueryRow(ctx, `SELECT id, name, icon_url, owner_id FROM guilds WHERE id=$1`, inv.GuildID).Scan(&g.ID, &g.Name, &g.IconURL, &g.OwnerID); err != nil {
		return domain.Guild{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Guild{}, err
	}
	g.MyRole = role
	return g, nil
}

func (p *Postgres) Invites(ctx context.Context, guildID int64) ([]domain.Invite, error) {
	rows, err := p.pool.Query(ctx, `SELECT code, guild_id, creator_id, max_uses, uses, expires_at FROM invites WHERE guild_id=$1`, guildID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Invite
	for rows.Next() {
		var inv domain.Invite
		if err := rows.Scan(&inv.Code, &inv.GuildID, &inv.CreatorID, &inv.MaxUses, &inv.Uses, &inv.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, inv)
	}
	return out, rows.Err()
}

func (p *Postgres) GetInvite(ctx context.Context, code string) (domain.Invite, error) {
	var inv domain.Invite
	err := p.pool.QueryRow(ctx, `SELECT code, guild_id, creator_id, max_uses, uses, expires_at FROM invites WHERE code=$1`, code).
		Scan(&inv.Code, &inv.GuildID, &inv.CreatorID, &inv.MaxUses, &inv.Uses, &inv.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Invite{}, apperr.NotFound("invite not found")
	}
	return inv, err
}

func (p *Postgres) DeleteInvite(ctx context.Context, code string) error {
	_, err := p.pool.Exec(ctx, `DELETE FROM invites WHERE code=$1`, code)
	return err
}

func (p *Postgres) RemoveMember(ctx context.Context, guildID, userID int64) error {
	return p.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM members WHERE guild_id=$1 AND user_id=$2`, guildID, userID); err != nil {
			return err
		}
		return p.emit(ctx, tx, "member.removed", guildID, map[string]string{"guild_id": id.Format(guildID), "user_id": id.Format(userID)})
	})
}

func (p *Postgres) SetRole(ctx context.Context, guildID, userID int64, role string) error {
	return p.inTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE members SET role=$3 WHERE guild_id=$1 AND user_id=$2`, guildID, userID, role)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return apperr.NotFound("member not found")
		}
		return p.emit(ctx, tx, "member.role_changed", guildID, map[string]string{"guild_id": id.Format(guildID), "user_id": id.Format(userID), "role": role})
	})
}

func (p *Postgres) Members(ctx context.Context, guildID int64) ([]domain.Member, error) {
	rows, err := p.pool.Query(ctx, `SELECT guild_id, user_id, role FROM members WHERE guild_id=$1 ORDER BY joined_at`, guildID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Member
	for rows.Next() {
		var m domain.Member
		if err := rows.Scan(&m.GuildID, &m.UserID, &m.Role); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (p *Postgres) inTx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (p *Postgres) emit(ctx context.Context, tx pgx.Tx, typ string, guildID int64, payload map[string]string) error {
	eventID := p.ids.Next()
	env, err := eventx.New(id.Format(eventID), typ, payload)
	if err != nil {
		return err
	}
	env.GuildID = id.Format(guildID)
	raw, err := env.Bytes()
	if err != nil {
		return err
	}
	return outbox.Insert(ctx, tx, eventID, domain.Topic, id.Format(guildID), raw)
}
