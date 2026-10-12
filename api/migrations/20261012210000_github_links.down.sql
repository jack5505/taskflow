-- Записи от GitHub переводятся в общие источники, иначе прежнее ограничение не встанет.
UPDATE audit_logs SET source = 'system' WHERE source = 'github';
UPDATE task_history SET source = 'api' WHERE source = 'github';
ALTER TABLE audit_logs DROP CONSTRAINT IF EXISTS audit_logs_source_check;
ALTER TABLE audit_logs ADD CONSTRAINT audit_logs_source_check CHECK (source IN ('web', 'bot', 'cli', 'system'));
ALTER TABLE task_history DROP CONSTRAINT IF EXISTS task_history_source_check;
ALTER TABLE task_history ADD CONSTRAINT task_history_source_check CHECK (source IN ('web', 'telegram', 'api'));
DROP TABLE IF EXISTS github_links;
