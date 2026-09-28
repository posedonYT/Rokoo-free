DROP INDEX IF EXISTS profiles_username_prefix_idx;
DROP INDEX IF EXISTS relationships_user_type_idx;
ALTER TABLE relationships DROP CONSTRAINT IF EXISTS relationships_type_chk;
