-- Уведомления колокольчика (ТЗ §3.8, §7.7): хранятся 30 дней, удаляет воркер.
-- task_id без внешнего ключа: таблица задач появится позже, ключ добавит её миграция.
-- event_id — событие outbox, из которого создано уведомление: повтор задания не создаёт дубль.
CREATE TABLE notifications (
    id         uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    task_id    uuid,
    type       text        NOT NULL,
    payload    jsonb       NOT NULL DEFAULT '{}'::jsonb,
    event_id   uuid,
    read_at    timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (event_id, user_id)
);

CREATE INDEX notifications_user_idx ON notifications (user_id, created_at DESC, id DESC);
CREATE INDEX notifications_unread_idx ON notifications (user_id) WHERE read_at IS NULL;
CREATE INDEX notifications_created_idx ON notifications (created_at);
