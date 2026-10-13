-- Публикация задач в GitHub Issues (ТЗ §12.9): переключатель проекта и связь задачи с issue.
-- Без привязанного репозитория публикация невозможна.
ALTER TABLE projects ADD COLUMN github_issues boolean NOT NULL DEFAULT false;
ALTER TABLE projects ADD CONSTRAINT projects_github_issues CHECK (NOT github_issues OR github_repo IS NOT NULL);

-- Issue задачи — ещё один вид связи github_links: номер обязателен, у задачи не больше одного.
ALTER TABLE github_links DROP CONSTRAINT github_links_kind_check;
ALTER TABLE github_links ADD CONSTRAINT github_links_kind_check CHECK (kind IN ('branch', 'pr', 'issue'));
ALTER TABLE github_links DROP CONSTRAINT github_links_check;
ALTER TABLE github_links ADD CONSTRAINT github_links_check CHECK ((kind = 'branch') = (number IS NULL));
CREATE UNIQUE INDEX github_links_issue_idx ON github_links (task_id) WHERE kind = 'issue';
