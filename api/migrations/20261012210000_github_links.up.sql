-- Связи задач с ветками и pull request'ами GitHub (ТЗ §12.3, §12.8). Таблица модуля task:
-- карточка задачи показывает связи, а правила статуса по PR (§12.4) смотрят на другие PR задачи.
CREATE TABLE github_links (
    id           uuid        PRIMARY KEY,
    task_id      uuid        NOT NULL REFERENCES tasks (id) ON DELETE CASCADE,
    kind         text        NOT NULL CHECK (kind IN ('branch', 'pr')),
    repo         text        NOT NULL,
    number       integer,
    name         text        NOT NULL,
    url          text        NOT NULL,
    state        text        NOT NULL CHECK (state IN ('open', 'draft', 'merged', 'closed')),
    author_login text,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    CHECK ((kind = 'pr') = (number IS NOT NULL))
);
CREATE UNIQUE INDEX github_links_pr_idx ON github_links (task_id, lower(repo), number) WHERE kind = 'pr';
CREATE UNIQUE INDEX github_links_branch_idx ON github_links (task_id, lower(repo), name) WHERE kind = 'branch';

-- §12.4: история задачи и журнал знают источник github.
ALTER TABLE task_history DROP CONSTRAINT IF EXISTS task_history_source_check;
ALTER TABLE task_history ADD CONSTRAINT task_history_source_check CHECK (source IN ('web', 'telegram', 'api', 'github'));
ALTER TABLE audit_logs DROP CONSTRAINT IF EXISTS audit_logs_source_check;
ALTER TABLE audit_logs ADD CONSTRAINT audit_logs_source_check CHECK (source IN ('web', 'bot', 'cli', 'system', 'github'));
