DROP INDEX IF EXISTS projects_telegram_chat_idx;
ALTER TABLE projects DROP COLUMN IF EXISTS telegram_chat_title;
