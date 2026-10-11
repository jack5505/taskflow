-- Корзина проекта (§7.9, GET /projects/{key}/trash): удалённые задачи, новые удаления сверху,
-- курсор (deleted_at, id).
CREATE INDEX tasks_trash_idx ON tasks (project_id, deleted_at DESC, id DESC)
    WHERE deleted_at IS NOT NULL;
