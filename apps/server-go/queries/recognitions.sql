-- name: CreateRecognitionJob :one
INSERT INTO recognition_jobs (request_id, event_id, user_id, image_file_id)
VALUES ($1, $2, $3, $4)
ON CONFLICT (request_id) DO UPDATE
SET request_id = EXCLUDED.request_id
RETURNING *;

-- name: GetRecognitionJob :one
SELECT * FROM recognition_jobs
WHERE id = $1 AND user_id = $2;

-- name: ClaimRecognitionJob :one
UPDATE recognition_jobs
SET status = 'processing',
    attempts = GREATEST(attempts, $2),
    last_error = '',
    updated_at = now()
WHERE id = $1
  AND user_id = $3
  AND (
    status IN ('queued', 'retrying', 'dead_lettered', 'publish_failed')
    OR (status = 'processing' AND updated_at < now() - interval '90 seconds')
  )
RETURNING *;

-- name: CompleteRecognitionJob :one
UPDATE recognition_jobs
SET status = 'succeeded',
    result = sqlc.arg(result)::jsonb,
    last_error = '',
    completed_at = now(),
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: MarkRecognitionJobError :one
UPDATE recognition_jobs
SET status = $2,
    attempts = GREATEST(attempts, $3),
    last_error = $4,
    completed_at = CASE WHEN $2 IN ('failed', 'dead_lettered') THEN now() ELSE NULL END,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: MarkRecognitionJobPublished :exec
UPDATE recognition_jobs
SET status = 'queued', published_at = now(), last_error = '', updated_at = now()
WHERE id = $1 AND status IN ('queued', 'publish_failed');

-- name: MarkRecognitionJobPublishFailed :exec
UPDATE recognition_jobs
SET status = 'publish_failed', last_error = $2, updated_at = now()
WHERE id = $1 AND published_at IS NULL;
