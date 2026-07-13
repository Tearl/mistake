-- name: InsertReviewLog :exec
INSERT INTO review_logs (user_id, mistake_id, action)
VALUES ($1, $2, $3);

-- name: ReviewLogsSince :many
SELECT created_at FROM review_logs
WHERE user_id = $1 AND created_at >= sqlc.arg(since)::timestamptz
ORDER BY created_at DESC
LIMIT 2000;
