ALTER TABLE dm_pairs
    ADD CONSTRAINT dm_pairs_channel_fk FOREIGN KEY (channel_id) REFERENCES dm_channels(id) ON DELETE CASCADE;
