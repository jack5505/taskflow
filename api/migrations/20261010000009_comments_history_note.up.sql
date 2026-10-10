-- Комментарии к задачам (ТЗ §3.6). Здесь — схема и причина перехода (§4: вернуть на доработку,
-- отменить); эндпоинты комментариев — задача комментариев.
CREATE TABLE comments (
    id         uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id    uuid        NOT NULL REFERENCES tasks (id),
    author_id  uuid        NOT NULL REFERENCES users (id),
    body       text        NOT NULL CHECK (length(body) BETWEEN 1 AND 5000),
    source     text        NOT NULL CHECK (source IN ('web', 'telegram')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX comments_task_idx ON comments (task_id, created_at);

-- Пояснение к записи истории: §3.1 требует «снят при деактивации» при освобождении задач.
ALTER TABLE task_history ADD COLUMN note text;
