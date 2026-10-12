-- Принятые доставки вебхуков GitHub App (ТЗ §12.2, §12.8): повторная доставка с тем же
-- X-GitHub-Delivery не обрабатывается. Хранятся 7 дней, уборку делает воркер.
CREATE TABLE github_deliveries (
    delivery_id text PRIMARY KEY,
    event       text        NOT NULL,
    received_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX github_deliveries_received_idx ON github_deliveries (received_at);
