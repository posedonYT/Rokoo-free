CREATE TABLE dm_channels (
    id BIGINT PRIMARY KEY,
    kind TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE dm_members (
    channel_id BIGINT NOT NULL REFERENCES dm_channels(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL,
    PRIMARY KEY (channel_id, user_id)
);

CREATE INDEX dm_members_user_idx ON dm_members (user_id);

CREATE TABLE dm_pairs (
    user_lo BIGINT NOT NULL,
    user_hi BIGINT NOT NULL,
    channel_id BIGINT NOT NULL UNIQUE,
    PRIMARY KEY (user_lo, user_hi)
);

CREATE TABLE messages (
    id BIGINT PRIMARY KEY,
    channel_id BIGINT NOT NULL,
    channel_kind TEXT NOT NULL,
    author_id BIGINT NOT NULL,
    content TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    edited_at TIMESTAMPTZ,
    deleted_at TIMESTAMPTZ
);

CREATE INDEX messages_channel_idx ON messages (channel_id, id DESC);

CREATE TABLE attachments (
    id BIGINT PRIMARY KEY,
    message_id BIGINT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    media_id TEXT NOT NULL,
    filename TEXT NOT NULL,
    content_type TEXT NOT NULL,
    size BIGINT NOT NULL
);

CREATE TABLE outbox (
    id BIGINT PRIMARY KEY,
    topic TEXT NOT NULL,
    key TEXT NOT NULL,
    payload JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at TIMESTAMPTZ
);

CREATE INDEX outbox_unpublished_idx ON outbox (id) WHERE published_at IS NULL;
