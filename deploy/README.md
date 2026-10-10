# Развёртывание на стенде

Стенд — VPS 157.173.109.181, каталог `~/taskflow`, адрес https://tasks.157.173.109.181.nip.io (ТЗ §10.2). Наружу сервис смотрит через nginx сервера (`~/mahalla-dev/nginx`), который уже держит 80/443 для других проектов; свой nginx у TaskFlow нет.

## Релиз и выкладка

1. Влить всё нужное в `main`, поставить тег: `git tag v1.2.3 && git push origin v1.2.3`.
2. GitHub Actions (`release.yml`) собирает образ `ghcr.io/jack5505/taskflow:v1.2.3` (и `:latest`) и создаёт GitHub Release со списком изменений.
3. На сервере:
   ```bash
   cd ~/taskflow && git pull --ff-only          # чтобы deploy/ и docker-compose.yml были актуальны
   deploy/backup.sh pre-v1.2.3                  # именованный снимок перед выкладкой
   deploy/deploy.sh v1.2.3
   ```
   Скрипт тянет образ (логин в GHCR токеном `gh`), пишет `TASKFLOW_IMAGE` в `.env`, перезапускает `api` и `worker`, ждёт `/health` до 3 минут. Миграции применяет `api` при старте.
4. Проверка: `curl -s https://tasks.157.173.109.181.nip.io/health`.

**Откат.** Если `/health` не ответил, `deploy.sh` сам возвращает предыдущий образ. Вручную — `deploy/deploy.sh <предыдущий тег>`. Откатывается только образ, поэтому миграции пишутся вперёд-совместимыми (CONTRIBUTING §8); если новая миграция всё же несовместима — `deploy/restore.sh pre-v1.2.3`.

## Первый запуск стенда

```bash
cd ~/taskflow && cp .env.example .env            # заполнить JWT_SECRET, SMTP_*, TELEGRAM_*
docker compose up -d --wait postgres redis       # данные
deploy/deploy.sh v0.1.0                          # первый образ
docker compose exec api /app/taskflow admin create --email admin@example.com   # первый администратор (ТЗ §7.1)
deploy/install-timers.sh                          # ежедневный бэкап
```

### nginx и сертификат (один раз)

```bash
cp deploy/nginx/tasks.157.173.109.181.nip.io.conf ~/mahalla-dev/nginx/extra/
docker network connect taskflow_default mahalla-dev-nginx      # доступ к контейнеру taskflow-api
docker exec mahalla-dev-nginx nginx -t && docker exec mahalla-dev-nginx nginx -s reload
# сертификат Let's Encrypt тем же certbot и каталогом, что у остальных сайтов сервера:
cd ~/mahalla-dev/nginx && docker run --rm --user "$(id -u):$(id -g)" \
  -v "$PWD/certs:/etc/letsencrypt" -v "$PWD/webroot:/var/www/certbot" certbot/certbot certonly \
  --non-interactive --webroot -w /var/www/certbot -d tasks.157.173.109.181.nip.io \
  --config-dir /etc/letsencrypt --work-dir /etc/letsencrypt/work --logs-dir /etc/letsencrypt/logs
docker exec mahalla-dev-nginx nginx -s reload
```

Продление — общий таймер `mahalla-cert-renew` (`renew.sh` обновляет все сертификаты в `certs/`). Чтобы подключение к сети `taskflow_default` пережило пересоздание контейнера nginx, добавьте её в `~/mahalla-dev/nginx/docker-compose.nginx.yml` (блок `networks`), как сделано для `quizlet_default`; сам контейнер пересоздавать не нужно.

## Бэкап и восстановление (ТЗ §10.3)

- `deploy/backup.sh` — дамп PostgreSQL (`db-<метка>.sql.gz`) и архив вложений (`attachments-<метка>.tar.gz`) в `backups/`; датированные файлы хранятся 30 дней (`BACKUP_KEEP_DAYS`), именованные (`pre-…`) — пока не удалите.
- Внешняя копия: задайте в `.env` `BACKUP_REMOTE=<remote>:<bucket>/taskflow` и положите конфиг rclone в `deploy/rclone.conf` (в git не попадает). Без `BACKUP_REMOTE` скрипт только пишет об этом в лог.
- Таймер: `deploy/install-timers.sh` ставит `taskflow-backup.timer` на 03:30 ежедневно; `systemctl --user list-timers taskflow-backup.timer`, журнал — `journalctl --user -u taskflow-backup.service`.
- Восстановление: `deploy/restore.sh <метка>` останавливает `api` и `worker`, пересоздаёт базу из дампа, распаковывает вложения, поднимает сервисы. Спрашивает подтверждение (`RESTORE_YES=1` — без вопроса).
- **Проверка раз в месяц:** `deploy/backup.sh check-$(date +%Y%m)`, затем `deploy/restore.sh check-…` на стенде в нерабочее время (или на отдельной копии каталога с другим `COMPOSE_PROJECT_NAME`), `curl /health` и выборочная проверка данных; результат — строкой в `backups/CHECKS.md`.
