-- GitHub-логин пользователя (ТЗ §12.5, §12.8): одна учётная запись GitHub — один пользователь.
-- citext: GitHub не различает регистр логинов.
ALTER TABLE users ADD COLUMN github_login citext;
CREATE UNIQUE INDEX users_github_login_idx ON users (github_login) WHERE github_login IS NOT NULL;
