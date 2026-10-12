-- Репозиторий GitHub проекта (ТЗ §12.1, §12.7, §12.8): owner/name и установка GitHub App, через
-- которую TaskFlow к нему ходит. Один репозиторий — один проект: уникальный индекс без учёта
-- регистра (GitHub не различает регистр в owner/name).
ALTER TABLE projects ADD COLUMN github_repo text;
ALTER TABLE projects ADD COLUMN github_installation_id bigint;
ALTER TABLE projects ADD CONSTRAINT projects_github_pair CHECK ((github_repo IS NULL) = (github_installation_id IS NULL));
CREATE UNIQUE INDEX projects_github_repo_idx ON projects (lower(github_repo)) WHERE github_repo IS NOT NULL;
