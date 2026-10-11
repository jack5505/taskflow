-- Одноразовые коды Telegram (ТЗ §3.8 OneTimeCode): привязка аккаунта (§8.1, 10 минут) и
-- подключение группы к проекту (§8.2, 15 минут). Код гасится при использовании (used_at).
CREATE TABLE one_time_codes (
    id         uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    kind       text        NOT NULL CHECK (kind IN ('user_link', 'group_bind')),
    code       text        NOT NULL UNIQUE,
    user_id    uuid        REFERENCES users (id) ON DELETE CASCADE,
    project_id uuid        REFERENCES projects (id) ON DELETE CASCADE,
    expires_at timestamptz NOT NULL,
    used_at    timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    -- Код привязки аккаунта — пользователя, код группы — проекта.
    CHECK ((kind = 'user_link' AND user_id IS NOT NULL AND project_id IS NULL)
        OR (kind = 'group_bind' AND project_id IS NOT NULL AND user_id IS NULL))
);
-- Новый код пользователя гасит его прежние неиспользованные.
CREATE INDEX one_time_codes_user_idx ON one_time_codes (user_id) WHERE used_at IS NULL;
CREATE INDEX one_time_codes_project_idx ON one_time_codes (project_id) WHERE used_at IS NULL;
-- Уборка истёкших (воркер).
CREATE INDEX one_time_codes_expires_idx ON one_time_codes (expires_at);
