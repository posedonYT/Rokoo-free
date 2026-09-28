DROP INDEX IF EXISTS invites_guild_idx;
ALTER TABLE channels DROP CONSTRAINT IF EXISTS channels_type_chk;
ALTER TABLE members DROP CONSTRAINT IF EXISTS members_role_chk;
