CREATE TABLE IF NOT EXISTS recognition_jobs (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    request_id    text NOT NULL UNIQUE,
    event_id      uuid NOT NULL UNIQUE,
    user_id       text NOT NULL,
    image_file_id text NOT NULL,
    status        text NOT NULL DEFAULT 'queued',
    attempts      integer NOT NULL DEFAULT 0,
    result        jsonb NOT NULL DEFAULT '{}'::jsonb,
    last_error    text NOT NULL DEFAULT '',
    published_at  timestamptz,
    completed_at  timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT recognition_jobs_status_check CHECK (
        status IN ('queued', 'processing', 'retrying', 'succeeded', 'failed', 'dead_lettered', 'publish_failed')
    )
);

CREATE INDEX IF NOT EXISTS idx_recognition_jobs_user_created
    ON recognition_jobs (user_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_recognition_jobs_status_updated
    ON recognition_jobs (status, updated_at);
