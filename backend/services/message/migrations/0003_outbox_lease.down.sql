DROP INDEX IF EXISTS attachments_message_idx;
DROP INDEX IF EXISTS outbox_lease_idx;
DROP INDEX IF EXISTS outbox_published_at_idx;
ALTER TABLE outbox DROP COLUMN IF EXISTS locked_until;
