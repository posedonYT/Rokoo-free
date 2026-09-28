ALTER TABLE media
    ADD CONSTRAINT media_status_chk CHECK (status IN ('pending', 'ready'));
CREATE INDEX media_owner_idx ON media (owner_id);

CREATE TABLE outbox (
    id BIGINT PRIMARY KEY,
    topic TEXT NOT NULL,
    key TEXT NOT NULL,
    payload JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at TIMESTAMPTZ
);
CREATE INDEX outbox_unpublished_idx ON outbox (id) WHERE published_at IS NULL;
