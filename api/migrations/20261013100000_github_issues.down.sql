DROP INDEX IF EXISTS github_links_issue_idx;
DELETE FROM github_links WHERE kind = 'issue';
ALTER TABLE github_links DROP CONSTRAINT github_links_check;
ALTER TABLE github_links ADD CONSTRAINT github_links_check CHECK ((kind = 'pr') = (number IS NOT NULL));
ALTER TABLE github_links DROP CONSTRAINT github_links_kind_check;
ALTER TABLE github_links ADD CONSTRAINT github_links_kind_check CHECK (kind IN ('branch', 'pr'));
ALTER TABLE projects DROP CONSTRAINT projects_github_issues;
ALTER TABLE projects DROP COLUMN github_issues;
