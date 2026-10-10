-- Спринты (ТЗ §3.4) — только схема; правила запуска и завершения — задачи спринтов.
CREATE TABLE sprints (
    id         uuid         PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id uuid         NOT NULL REFERENCES projects (id),
    name       varchar(100) NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
    goal       text         CHECK (goal IS NULL OR length(goal) <= 1000),
    start_date date,
    end_date   date,
    state      text         NOT NULL DEFAULT 'planned' CHECK (state IN ('planned', 'active', 'closed')),
    closed_at  timestamptz,
    created_at timestamptz  NOT NULL DEFAULT now(),
    updated_at timestamptz  NOT NULL DEFAULT now(),
    CHECK (start_date IS NULL OR end_date IS NULL OR (end_date > start_date AND end_date <= start_date + 42))
);
CREATE INDEX sprints_project_idx ON sprints (project_id);

-- Задачи (ТЗ §3.3).
CREATE TABLE tasks (
    id                  uuid             PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id          uuid             NOT NULL REFERENCES projects (id),       -- не меняется
    number              integer          NOT NULL CHECK (number > 0),             -- из projects.task_counter
    key                 text             NOT NULL UNIQUE,                          -- {project.key}-{number}
    title               varchar(200)     NOT NULL CHECK (length(title) BETWEEN 1 AND 200),
    description         text             CHECK (description IS NULL OR length(description) <= 20000),
    type                text             NOT NULL DEFAULT 'task' CHECK (type IN ('task', 'bug', 'feature')),
    status              text             NOT NULL DEFAULT 'todo' CHECK (status IN ('todo', 'in_progress', 'review', 'done', 'cancelled')),
    priority            text             NOT NULL DEFAULT 'medium' CHECK (priority IN ('low', 'medium', 'high', 'critical')),
    reporter_id         uuid             NOT NULL REFERENCES users (id),
    assignee_id         uuid             REFERENCES users (id),
    sprint_id           uuid             REFERENCES sprints (id),                  -- NULL = бэклог
    estimate_minutes    integer          CHECK (estimate_minutes BETWEEN 1 AND 60000),
    spent_minutes       integer          NOT NULL DEFAULT 0 CHECK (spent_minutes >= 0), -- сумма записей времени
    due_date            date,
    labels              jsonb            NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(labels) = 'array' AND jsonb_array_length(labels) <= 10),
    started_at          timestamptz,
    completed_at        timestamptz,
    position            double precision NOT NULL,                                 -- порядок в секции (7.4)
    telegram_message_id bigint,
    version             integer          NOT NULL DEFAULT 1 CHECK (version >= 1),  -- If-Match (§3.3)
    deleted_at          timestamptz,
    created_at          timestamptz      NOT NULL DEFAULT now(),
    updated_at          timestamptz      NOT NULL DEFAULT now(),
    UNIQUE (project_id, number)
);
-- Доска и бэклог: секция (спринт или бэклог) по порядку.
CREATE INDEX tasks_section_idx ON tasks (project_id, sprint_id, position) WHERE deleted_at IS NULL;
-- «Мои задачи» (§7.2) и освобождение задач при деактивации (§3.1).
CREATE INDEX tasks_assignee_open_idx ON tasks (assignee_id) WHERE deleted_at IS NULL AND status NOT IN ('done', 'cancelled');

-- История изменений задачи (ТЗ §3.7): не редактируется и не удаляется.
CREATE TABLE task_history (
    id         uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id    uuid        NOT NULL REFERENCES tasks (id),
    user_id    uuid        REFERENCES users (id),                                 -- NULL — система (воркер)
    field      text        NOT NULL,
    old_value  jsonb,
    new_value  jsonb,
    source     text        NOT NULL CHECK (source IN ('web', 'telegram', 'api')),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX task_history_task_idx ON task_history (task_id, created_at, id);

CREATE FUNCTION task_history_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'task_history: история задачи не изменяется и не удаляется';
END;
$$;
CREATE TRIGGER task_history_immutable BEFORE UPDATE OR DELETE ON task_history
    FOR EACH ROW EXECUTE FUNCTION task_history_immutable();

-- Уведомления о задачах теперь ссылаются на задачу (таблица уведомлений появилась раньше задач).
ALTER TABLE notifications ADD CONSTRAINT notifications_task_fk FOREIGN KEY (task_id) REFERENCES tasks (id);
