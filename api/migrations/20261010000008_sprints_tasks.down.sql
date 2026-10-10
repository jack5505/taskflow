ALTER TABLE notifications DROP CONSTRAINT IF EXISTS notifications_task_fk;
DROP TABLE IF EXISTS task_history;
DROP FUNCTION IF EXISTS task_history_immutable();
DROP TABLE IF EXISTS tasks;
DROP TABLE IF EXISTS sprints;
