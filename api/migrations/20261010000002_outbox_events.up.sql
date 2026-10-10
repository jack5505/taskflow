-- Outbox (ТЗ §3.8, §9.4): событие пишется в той же транзакции, что и данные;
-- воркер переносит его в очередь asynq и ставит sent_at. Отправленные чистятся через 7 дней.
CREATE TABLE outbox_events (
    id         uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    type       text        NOT NULL,
    payload    jsonb       NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    sent_at    timestamptz
);

-- Воркер читает неотправленные по порядку создания.
CREATE INDEX outbox_events_unsent_idx ON outbox_events (created_at) WHERE sent_at IS NULL;
-- Очистка отправленных старше 7 дней.
CREATE INDEX outbox_events_sent_idx ON outbox_events (sent_at) WHERE sent_at IS NOT NULL;
