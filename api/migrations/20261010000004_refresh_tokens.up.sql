-- Refresh-токены хранятся на сервере (ТЗ §9.1): отзыв при смене пароля, деактивации и «Выйти везде».
-- В базе лежит только SHA-256 токена: утечка таблицы не даёт сессий.
CREATE TABLE refresh_tokens (
    id         uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash text        NOT NULL UNIQUE,
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    user_agent text        NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);

-- Отзыв всех сессий пользователя и уборка просроченных.
CREATE INDEX refresh_tokens_user_idx ON refresh_tokens (user_id) WHERE revoked_at IS NULL;
CREATE INDEX refresh_tokens_expires_idx ON refresh_tokens (expires_at);
