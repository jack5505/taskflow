-- Запущенные таймеры (ТЗ §3.8 ActiveTimer, §6.4): не более одного на пользователя.
-- Таймер живёт только на задаче в работе, где пользователь исполнитель; остановка пишет
-- записи времени source = timer и удаляет строку.
CREATE TABLE active_timers (
    user_id    uuid        PRIMARY KEY REFERENCES users (id),
    task_id    uuid        NOT NULL REFERENCES tasks (id),
    started_at timestamptz NOT NULL
);
-- Остановка таймеров задачи при смене статуса, исполнителя и удалении.
CREATE INDEX active_timers_task_idx ON active_timers (task_id);
-- Автостоп через 12 часов (воркер).
CREATE INDEX active_timers_started_idx ON active_timers (started_at);
