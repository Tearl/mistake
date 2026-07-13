-- name: ListMistakes :many
SELECT * FROM mistakes
WHERE user_id = $1
  AND (sqlc.arg(subject)::text = '' OR subject = sqlc.arg(subject)::text)
ORDER BY created_at DESC
LIMIT sqlc.arg(lim)::int;

-- name: GetMistake :one
SELECT * FROM mistakes
WHERE id = sqlc.arg(id)::uuid AND user_id = $1;

-- name: CreateMistake :one
INSERT INTO mistakes (
    user_id, image_file_id, subject, knowledge_points, question_type,
    difficulty, ocr_text, answer, error_reason, mastery, wrong_count,
    kind, from_mistake_id
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13
)
RETURNING *;

-- name: UpdateMastery :one
UPDATE mistakes
SET mastery = sqlc.arg(mastery)::text
WHERE id = sqlc.arg(id)::uuid AND user_id = $1
RETURNING *;

-- name: GradeMistake :one
UPDATE mistakes
SET mastery = sqlc.arg(mastery)::text,
    wrong_count = wrong_count + sqlc.arg(wrong_inc)::int,
    last_review_at = now()
WHERE id = sqlc.arg(id)::uuid AND user_id = $1
RETURNING *;

-- name: DeleteMistake :one
DELETE FROM mistakes
WHERE id = sqlc.arg(id)::uuid AND user_id = $1
RETURNING image_file_id;

-- name: CountPool :one
SELECT count(*) FROM mistakes
WHERE user_id = $1
  AND (sqlc.arg(subject)::text = '' OR subject = sqlc.arg(subject)::text)
  AND (sqlc.arg(mastery)::text = 'all' OR mastery = sqlc.arg(mastery)::text);

-- name: RandomMistake :one
SELECT * FROM mistakes
WHERE user_id = $1
  AND (sqlc.arg(subject)::text = '' OR subject = sqlc.arg(subject)::text)
  AND (sqlc.arg(mastery)::text = 'all' OR mastery = sqlc.arg(mastery)::text)
ORDER BY random()
LIMIT 1;

-- name: StatsCounts :one
SELECT
    count(*)::int AS total,
    count(*) FILTER (WHERE mastery = 'mastered')::int AS mastered,
    count(*) FILTER (WHERE mastery = 'reviewing')::int AS reviewing
FROM mistakes
WHERE user_id = $1;

-- name: StatsBySubject :many
SELECT subject, count(*)::int AS count
FROM mistakes
WHERE user_id = $1
GROUP BY subject
ORDER BY count DESC;

-- name: ExportMistakes :many
SELECT * FROM mistakes
WHERE user_id = $1
  AND (
    (cardinality(sqlc.arg(ids)::text[]) > 0 AND id::text = ANY(sqlc.arg(ids)::text[]))
    OR
    (cardinality(sqlc.arg(ids)::text[]) = 0 AND (sqlc.arg(subject)::text = '' OR subject = sqlc.arg(subject)::text))
  )
ORDER BY created_at DESC
LIMIT 200;
