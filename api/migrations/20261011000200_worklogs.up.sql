-- Записи времени (ТЗ §3.5, §6). Записи удалённой задачи остаются (§4): задачи удаляются мягко.
-- spent_minutes задачи — сумма её записей, поддерживается сервисом в той же транзакции.
CREATE TABLE worklogs (
    id         uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id    uuid        NOT NULL REFERENCES tasks (id),
    user_id    uuid        NOT NULL REFERENCES users (id),
    minutes    integer     NOT NULL CHECK (minutes BETWEEN 1 AND 1440),
    work_date  date        NOT NULL,
    comment    text        CHECK (length(comment) <= 500),
    source     text        NOT NULL CHECK (source IN ('manual', 'timer', 'telegram')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX worklogs_task_idx ON worklogs (task_id, work_date);
-- Дневной лимит (§6.3) и отчёты по пользователю (§6.6).
CREATE INDEX worklogs_user_date_idx ON worklogs (user_id, work_date);
