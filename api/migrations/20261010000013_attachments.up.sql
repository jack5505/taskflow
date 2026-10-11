-- Вложения задач (ТЗ §3.9). Файл лежит в ATTACHMENTS_DIR под именем storage_path (uuid, не имя
-- пользователя); file_name — только для показа и Content-Disposition при скачивании.
CREATE TABLE attachments (
    id           uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id      uuid        NOT NULL REFERENCES tasks (id),
    author_id    uuid        NOT NULL REFERENCES users (id),
    file_name    text        NOT NULL CHECK (length(file_name) BETWEEN 1 AND 255),
    mime_type    text        NOT NULL,
    size_bytes   bigint      NOT NULL CHECK (size_bytes >= 0),
    storage_path text        NOT NULL UNIQUE,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX attachments_task_idx ON attachments (task_id, created_at);
