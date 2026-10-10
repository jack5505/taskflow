-- Пользователи (ТЗ §3.1), проекты (§3.2) и участники проектов (§3.2, ProjectMember).
-- Проекты создаются здесь же: без них не сослаться из project_members. CRUD проектов — issue #15.

CREATE TABLE users (
    id                    uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    email                 citext      NOT NULL UNIQUE,                 -- логин без учёта регистра
    password_hash         text        NOT NULL,                        -- bcrypt, cost 12
    display_name          varchar(80) NOT NULL CHECK (length(display_name) BETWEEN 1 AND 80),
    timezone              text        NOT NULL DEFAULT 'Europe/Moscow',
    telegram_user_id      bigint      UNIQUE,                          -- заполняется при привязке бота (§8.1)
    telegram_username     text,                                        -- без @, обновляется при каждом сообщении
    is_system_admin       boolean     NOT NULL DEFAULT false,
    is_active             boolean     NOT NULL DEFAULT true,
    notification_settings jsonb       NOT NULL DEFAULT '{}'::jsonb,    -- настройки личных уведомлений §8.7; по умолчанию все включены
    created_at            timestamptz NOT NULL DEFAULT now(),
    updated_at            timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE projects (
    id                    uuid         PRIMARY KEY DEFAULT gen_random_uuid(),
    key                   text         NOT NULL UNIQUE CHECK (key ~ '^[A-Z]{2,10}$'),  -- не меняется после создания
    name                  varchar(100) NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
    description           text         CHECK (description IS NULL OR length(description) <= 2000),
    task_counter          integer      NOT NULL DEFAULT 0 CHECK (task_counter >= 0),   -- только растёт, UPDATE … RETURNING
    telegram_chat_id      bigint,                                                      -- не более одной группы на проект
    is_archived           boolean      NOT NULL DEFAULT false,
    timezone              text         NOT NULL DEFAULT 'Europe/Moscow',
    notification_settings jsonb        NOT NULL DEFAULT '{}'::jsonb,                   -- настройки событий бота §8.6; по умолчанию как в таблице 8.6
    daily_summary_time    time         NOT NULL DEFAULT '10:00',
    deleted_at            timestamptz,                                                 -- мягкое удаление (§3.2)
    created_at            timestamptz  NOT NULL DEFAULT now(),
    updated_at            timestamptz  NOT NULL DEFAULT now()
);

CREATE TABLE project_members (
    project_id uuid        NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    user_id    uuid        NOT NULL REFERENCES users (id)    ON DELETE CASCADE,
    role       text        NOT NULL CHECK (role IN ('manager', 'member')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (project_id, user_id)                                   -- пара уникальна (§3.2)
);

-- Список проектов пользователя (§7.2) и проверка роли на каждом запросе (§2).
CREATE INDEX project_members_user_idx ON project_members (user_id);
