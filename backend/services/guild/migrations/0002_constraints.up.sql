ALTER TABLE members
    ADD CONSTRAINT members_role_chk CHECK (role IN ('owner', 'admin', 'member'));
ALTER TABLE channels
    ADD CONSTRAINT channels_type_chk CHECK (type IN ('text', 'voice'));
CREATE INDEX invites_guild_idx ON invites (guild_id);
