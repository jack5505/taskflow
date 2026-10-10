# Миграции

Единственный источник схемы — файлы здесь, применяются golang-migrate при старте `taskflow api` (ТЗ §10.2). GORM AutoMigrate не используется.

Имя файла: `YYYYMMDDHHMMSS_name.up.sql` и парный `.down.sql`. Создать пару: `make migrate-new name=create_users`.
