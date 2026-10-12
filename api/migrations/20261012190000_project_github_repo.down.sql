DROP INDEX IF EXISTS projects_github_repo_idx;
ALTER TABLE projects DROP CONSTRAINT IF EXISTS projects_github_pair;
ALTER TABLE projects DROP COLUMN IF EXISTS github_installation_id;
ALTER TABLE projects DROP COLUMN IF EXISTS github_repo;
