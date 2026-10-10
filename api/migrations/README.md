# Миграции

Единственный источник схемы — файлы здесь. Они встраиваются в бинарник (`embed.go`) и применяются golang-migrate при старте `taskflow api` (ТЗ §10.2). GORM AutoMigrate не используется.

Имя файла: `YYYYMMDDHHMMSS_name.up.sql` и парный `.down.sql`. Создать пару: `make migrate-new name=create_users`.
