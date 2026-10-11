-- Что воркер уже отправил по расписанию, чтобы не повторять (ТЗ §8.6, §8.7, §7.7).
-- Напоминание о сроке: «срок завтра» и «срок просрочен» — один раз на задачу, срок и получателя
-- (сменился срок или исполнитель — напоминание придёт снова). Строки старше недели после срока
-- удаляет воркер: условия отправки для них уже не выполнятся.
CREATE TABLE due_reminders (
    task_id    uuid        NOT NULL REFERENCES tasks (id) ON DELETE CASCADE,
    kind       text        NOT NULL CHECK (kind IN ('due_tomorrow', 'overdue')),
    due_date   date        NOT NULL,
    user_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (task_id, kind, due_date, user_id)
);
CREATE INDEX due_reminders_due_idx ON due_reminders (due_date);

-- Открытые задачи со сроком: их воркер проверяет раз в минуту.
CREATE INDEX tasks_open_due_idx ON tasks (due_date)
    WHERE due_date IS NOT NULL AND deleted_at IS NULL AND status NOT IN ('done', 'cancelled');

-- Ежедневная сводка в группу проекта — одна за дату по поясу проекта.
CREATE TABLE daily_summaries (
    project_id uuid        NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    date       date        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (project_id, date)
);
