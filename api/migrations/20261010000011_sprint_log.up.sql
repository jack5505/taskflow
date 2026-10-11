-- Журнал спринта (ТЗ §3.8, §5.3): задачи, добавленные и убранные после старта спринта.
CREATE TABLE sprint_log (
    id         uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    sprint_id  uuid        NOT NULL REFERENCES sprints (id),
    task_id    uuid        NOT NULL REFERENCES tasks (id),
    user_id    uuid        REFERENCES users (id),                 -- NULL — система
    action     text        NOT NULL CHECK (action IN ('added', 'removed')),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX sprint_log_sprint_idx ON sprint_log (sprint_id, created_at);
