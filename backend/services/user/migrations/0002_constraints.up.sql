ALTER TABLE relationships
    ADD CONSTRAINT relationships_type_chk CHECK (type IN ('pending_out', 'pending_in', 'friend', 'blocked'));

CREATE INDEX relationships_user_type_idx ON relationships (user_id, type);
CREATE INDEX profiles_username_prefix_idx ON profiles (lower(username) text_pattern_ops);
