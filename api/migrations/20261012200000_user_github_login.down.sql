DROP INDEX IF EXISTS users_github_login_idx;
ALTER TABLE users DROP COLUMN IF EXISTS github_login;
