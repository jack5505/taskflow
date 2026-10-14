-- Ссылки-приглашения в проект (ТЗ §3.8 ProjectInvite, §7.1): действует 7 дней или до отзыва,
-- по ссылке может присоединиться любое число людей. В базе только SHA-256 токена.
CREATE TABLE project_invites (
    id           uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id   uuid        NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    role         text        NOT NULL CHECK (role IN ('manager', 'member')),
    token_hash   text        NOT NULL UNIQUE,
    created_by   uuid        REFERENCES users (id) ON DELETE SET NULL,
    expires_at   timestamptz NOT NULL,
    revoked_at   timestamptz,
    joined_count integer     NOT NULL DEFAULT 0 CHECK (joined_count >= 0),
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);

-- Действующие ссылки проекта во вкладке «Участники» (7.9).
CREATE INDEX project_invites_project_idx ON project_invites (project_id, created_at DESC) WHERE revoked_at IS NULL;
