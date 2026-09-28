CREATE TABLE media (
    id BIGINT PRIMARY KEY,
    owner_id BIGINT NOT NULL,
    bucket TEXT NOT NULL,
    object_key TEXT NOT NULL,
    filename TEXT NOT NULL,
    content_type TEXT NOT NULL,
    size BIGINT NOT NULL,
    status TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
