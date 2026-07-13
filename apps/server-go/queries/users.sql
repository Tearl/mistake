-- name: GetUser :one
SELECT * FROM users WHERE id = $1;

-- name: EnsureUser :one
INSERT INTO users (id, role, nick_name, last_login_at)
VALUES ($1, 'user', sqlc.arg(nick_name)::text, now())
ON CONFLICT (id) DO UPDATE SET last_login_at = now()
RETURNING *;

-- name: CountUsers :one
SELECT count(*)::int FROM users;

-- name: CountAllMistakes :one
SELECT count(*)::int FROM mistakes;

-- name: CountAllMastered :one
SELECT count(*)::int FROM mistakes WHERE mastery = 'mastered';

-- name: AdminPerUser :many
SELECT
    user_id,
    count(*)::int AS count,
    count(*) FILTER (WHERE mastery = 'mastered')::int AS mastered
FROM mistakes
GROUP BY user_id
ORDER BY count DESC
LIMIT 50;
