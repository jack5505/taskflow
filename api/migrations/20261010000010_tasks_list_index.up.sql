-- Список задач проекта (§9.2): порядок «секция → ручной порядок → номер». Секция — спринт,
-- бэклог (sprint_id IS NULL) — последней; выражение то же, что в запросе списка.
CREATE INDEX tasks_list_idx ON tasks (project_id, (COALESCE(sprint_id::text, '~')), position, number)
    WHERE deleted_at IS NULL;
