ALTER TABLE outbox ADD COLUMN IF NOT EXISTS locked_until TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS outbox_published_at_idx ON outbox (published_at) WHERE published_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS outbox_lease_idx ON outbox (id) WHERE published_at IS NULL;

