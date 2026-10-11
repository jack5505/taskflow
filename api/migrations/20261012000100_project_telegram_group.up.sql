-- Подключённая Telegram-группа проекта (ТЗ §8.2, §7.9): название группы для настроек проекта
-- (обновляется, когда группу переименовывают) и поиск проектов группы по id чата — для /unbind,
-- миграции в супергруппу и удаления бота из группы. Одна группа может обслуживать несколько
-- проектов, поэтому индекс не уникальный.
ALTER TABLE projects ADD COLUMN telegram_chat_title text;
CREATE INDEX projects_telegram_chat_idx ON projects (telegram_chat_id) WHERE telegram_chat_id IS NOT NULL;
