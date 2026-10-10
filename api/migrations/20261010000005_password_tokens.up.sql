-- Одноразовые ссылки установки и восстановления пароля (ТЗ §7.1, §9.1): срок 24 часа,
-- гасятся после использования. Как и у refresh-токенов, в базе только SHA-256 токена.
CREATE TABLE password_tokens (
    id         uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash text        NOT NULL UNIQUE,
    expires_at timestamptz NOT NULL,
    used_at    timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX password_tokens_user_idx ON password_tokens (user_id) WHERE used_at IS NULL;
CREATE INDEX password_tokens_expires_idx ON password_tokens (expires_at);
