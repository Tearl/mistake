CREATE EXTENSION IF NOT EXISTS "pgcrypto";

CREATE TABLE IF NOT EXISTS users (
    id            text PRIMARY KEY,
    role          text NOT NULL DEFAULT 'user',
    nick_name     text NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL DEFAULT now(),
    last_login_at timestamptz
);

CREATE TABLE IF NOT EXISTS mistakes (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id          text NOT NULL,
    image_file_id    text NOT NULL DEFAULT '',
    subject          text NOT NULL DEFAULT '',
    knowledge_points text[] NOT NULL DEFAULT '{}',
    question_type    text NOT NULL DEFAULT '',
    difficulty       text NOT NULL DEFAULT '中',
    ocr_text         text NOT NULL DEFAULT '',
    answer           text NOT NULL DEFAULT '',
    error_reason     text NOT NULL DEFAULT '',
    mastery          text NOT NULL DEFAULT 'unmastered',
    wrong_count      integer NOT NULL DEFAULT 0,
    kind             text NOT NULL DEFAULT 'photo',
    from_mistake_id  text NOT NULL DEFAULT '',
    created_at       timestamptz NOT NULL DEFAULT now(),
    last_review_at   timestamptz
);

CREATE INDEX IF NOT EXISTS idx_mistakes_user_created ON mistakes (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_mistakes_user_subject ON mistakes (user_id, subject);

CREATE TABLE IF NOT EXISTS review_logs (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    text NOT NULL,
    mistake_id text NOT NULL DEFAULT '',
    action     text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_review_logs_user_created ON review_logs (user_id, created_at DESC);

-- 单用户模式：种一个 dev 用户，并设为 admin 让管理后台可用
INSERT INTO users (id, role, nick_name)
VALUES ('dev-user', 'admin', '本地开发者')
ON CONFLICT (id) DO NOTHING;
