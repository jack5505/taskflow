# Развёртывание TaskFlow

Инструкция для человека, который впервые ставит или сопровождает стенд (ТЗ §10.2, §10.3). Все команды — из каталога репозитория на сервере (дальше `~/taskflow`). Скрипты лежат в `deploy/`, краткий список — в [deploy/README.md](../deploy/README.md).

## 1. Что разворачивается

| Сервис | Образ | Назначение |
|---|---|---|
| `api` | `ghcr.io/jack5505/taskflow:<тег>` (`taskflow api`) | HTTP API, веб-интерфейс (встроен в бинарник), webhook Telegram. Применяет миграции при старте. Слушает `127.0.0.1:${TASKFLOW_PORT}` |
| `worker` | тот же образ (`taskflow worker`) | outbox → очередь asynq: письма, сообщения бота, таймеры, сводки |
| `postgres` | `postgres:16-alpine` | все данные; том `<проект>_pgdata` |
| `redis` | `redis:7-alpine` | очередь и служебные ключи; том `<проект>_redisdata` |

Вложения — том `<проект>_attachments` (`/data/attachments` в `api` и `worker`). `<проект>` — `COMPOSE_PROJECT_NAME` из `.env`.

Снаружи сервис открывается через уже работающий на сервере nginx с сертификатом Let's Encrypt (раздел 4.3); своего nginx у TaskFlow нет.

**Что нужно на сервере:** Docker с плагином `docker compose` v2, `curl`, `gh` (авторизованный — им скрипт выкладки логинится в GHCR, пакет приватный), systemd с пользовательскими юнитами (`loginctl enable-linger <пользователь>`, чтобы таймеры работали без входа в систему). Go, Node и make на сервере не нужны.

## 2. Переменные окружения (`.env`)

`.env` создаётся из [`.env.example`](../.env.example) и в git не попадает. Секреты (`JWT_SECRET`, `SMTP_PASSWORD`, `TELEGRAM_*`, `GITHUB_WEBHOOK_SECRET`, `GITHUB_CLIENT_SECRET`) — только здесь; приватный ключ GitHub App — файлом в `./secrets/` (раздел 4.4). Значения с пробелами и `<>` берите в кавычки: `SMTP_FROM="TaskFlow <noreply@example.com>"`.

### Приложение (читает `api/internal/config`)

| Переменная | Обязательна | По умолчанию | Что это |
|---|---|---|---|
| `DATABASE_URL` | да | — | строка подключения PostgreSQL; в compose — `postgres://taskflow:taskflow@postgres:5432/taskflow?sslmode=disable` |
| `JWT_SECRET` | да | — | ключ подписи токенов, не короче 32 символов: `openssl rand -base64 32`. Смена разлогинивает всех |
| `APP_BASE_URL` | да на стенде | `http://localhost:8080` | внешний адрес (`https://<домен>`): из него строятся ссылки в письмах; по схеме `https` ставится флаг `Secure` у cookie |
| `APP_ADDR` | нет | `:8080` | адрес внутри контейнера; не менять — на него смотрят healthcheck и проброс порта |
| `APP_ENV` | нет | `development` | `production` включает release-режим Gin (меньше отладочного вывода); на API и cookie не влияет — флаг `Secure` зависит от `APP_BASE_URL` |
| `REDIS_ADDR` | нет | `localhost:6379` | в compose — `redis:6379` |
| `JWT_ACCESS_TTL` | нет | `15m` | срок access-токена |
| `JWT_REFRESH_TTL` | нет | `720h` | срок refresh-токена (cookie), 30 дней |
| `SMTP_HOST`, `SMTP_PORT`, `SMTP_USER`, `SMTP_PASSWORD`, `SMTP_FROM` | нет | порт `587` | внешний SMTP (§10.2). Без `SMTP_HOST` письма не отправляются, а `admin create` печатает ссылку в консоль |
| `TELEGRAM_BOT_TOKEN`, `TELEGRAM_WEBHOOK_SECRET`, `TELEGRAM_BOT_USERNAME` | этап 2 | — | бот (§8): токен от BotFather, секрет заголовка webhook, имя бота без `@` |
| `GITHUB_APP_ID` | нет | пусто | ID GitHub App (§12). Пусто — интеграция выключена, маршруты `/api/v1/github/…` не регистрируются. Остальные `GITHUB_*` задаются вместе с ним (раздел 4.4) |
| `GITHUB_APP_PRIVATE_KEY_FILE` | с `GITHUB_APP_ID` | `/run/secrets/github-app.pem` | путь к PEM-ключу App **внутри контейнера**; на сервере файл лежит в `./secrets/`. Нет файла или не RSA PEM — `api` и `worker` не стартуют |
| `GITHUB_WEBHOOK_SECRET` | с `GITHUB_APP_ID` | — | секрет вебхука App (подпись `X-Hub-Signature-256`), от 16 символов: `openssl rand -hex 32` |
| `GITHUB_CLIENT_ID`, `GITHUB_CLIENT_SECRET` | с `GITHUB_APP_ID` | — | Client ID и Client secret App: вход в GitHub для связки аккаунтов в профиле (§12.5) |
| `GITHUB_API_URL`, `GITHUB_WEB_URL` | нет | `https://api.github.com`, `https://github.com` | меняются только для GitHub Enterprise |
| `ATTACHMENTS_DIR` | нет | `./data/attachments` | в compose — `/data/attachments` (том) |
| `ATTACHMENT_MAX_BYTES` | нет | `52428800` | лимит вложения (§3.9, 50 МБ); у nginx `client_max_body_size` чуть больше |

### Стенд и скрипты `deploy/`

| Переменная | По умолчанию | Что это |
|---|---|---|
| `COMPOSE_PROJECT_NAME` | имя каталога | префикс контейнеров, томов и сети; меняют только для второй копии стенда |
| `TASKFLOW_PORT` | `8100` | порт `api` на `127.0.0.1`; на него смотрит nginx и `deploy.sh` |
| `TASKFLOW_IMAGE` | `taskflow:local` | текущий образ; **пишет `deploy.sh`**, руками не задаётся |
| `BACKUP_DIR` | `<репозиторий>/backups` | каталог бэкапов; переменная окружения важнее `.env`; относительный путь — от каталога вызова (из `.env` — от корня репозитория) |
| `BACKUP_KEEP_DAYS` | `30` | сколько дней хранить датированные бэкапы (§10.3) |
| `BACKUP_REMOTE` | пусто | rclone-адрес внешней копии, например `s3:backups/taskflow`; конфиг rclone — `deploy/rclone.conf` (в git не попадает) |
| `BACKUP_SECRETS_PASSWORD` | пусто | пароль шифрования архива секретов для внешней копии (`openssl rand -base64 32`). Пусто — секреты (`.env`, `secrets/`) остаются только на сервере. Сохраните пароль и вне сервера: без него архив не открыть |

Переменные окружения самого `deploy.sh` (в `.env` не пишутся, задаются при запуске): `TASKFLOW_DEPLOY_WAIT` (ожидание `/health`, 180 с), `TASKFLOW_IMAGE_REPO` (репозиторий образа), `TASKFLOW_SKIP_PULL=1` (взять локальный образ), `TASKFLOW_HEALTH_URL`, `GHCR_USER`.

## 3. Миграции

- Схема — только миграции golang-migrate из `api/migrations` (встроены в бинарник). `api` применяет их при каждом старте, до приёма запросов; `taskflow admin create` — тоже, поэтому его можно запускать и до первого старта `api`.
- Текущая версия: `docker compose exec postgres psql -U taskflow -c 'SELECT * FROM schema_migrations'`. `dirty = t` значит, что миграция упала посередине: `api` не стартует, в логе (`docker compose logs api`) — текст ошибки. Порядок действий: восстановить базу из снимка перед выкладкой (раздел 6), откатить образ, чинить миграцию в новом релизе.
- Откат образа миграции не откатывает. Поэтому миграции вперёд-совместимые (CONTRIBUTING §8): сначала добавляем колонку и выкатываем код, удаляем старое — следующим релизом.

## 4. Первый запуск

### 4.1. Код, `.env`, данные

```bash
git clone https://github.com/jack5505/taskflow.git ~/taskflow && cd ~/taskflow
cp .env.example .env && chmod 600 .env
# заполнить: JWT_SECRET (openssl rand -base64 32), APP_BASE_URL=https://<домен>,
#            SMTP_* (если есть почта), BACKUP_REMOTE (если есть внешнее хранилище)
docker compose up -d --wait postgres redis
```

### 4.2. Первый релиз и администратор

```bash
deploy/deploy.sh vX.Y.Z          # последний релиз: gh release list
curl -s 127.0.0.1:8100/health    # {"checks":{"postgres":"ok","redis":"ok"},"status":"ok"}
docker compose exec api /app/taskflow admin create --email admin@example.com --name "Администратор"
```

`admin create` (ТЗ §7.1): с настроенным SMTP ставит в очередь письмо со ссылкой установки пароля (отправит `worker`), без SMTP печатает ссылку в консоль — она одноразовая и действует 24 часа. Повторный запуск с тем же email нового пользователя не создаёт; существующего пользователя назначает администратором системы. Остальных пользователей и проекты администратор заводит в веб-интерфейсе.

### 4.3. nginx и сертификат (один раз)

TaskFlow встраивается в nginx, который на сервере уже держит 80/443 (`~/mahalla-dev/nginx`):

```bash
cp deploy/nginx/tasks.157.173.109.181.nip.io.conf ~/mahalla-dev/nginx/extra/   # для другого домена — поправить server_name
docker network connect taskflow_default mahalla-dev-nginx      # nginx ходит на alias taskflow-api в сети стенда
docker exec mahalla-dev-nginx nginx -t && docker exec mahalla-dev-nginx nginx -s reload
cd ~/mahalla-dev/nginx && docker run --rm --user "$(id -u):$(id -g)" \
  -v "$PWD/certs:/etc/letsencrypt" -v "$PWD/webroot:/var/www/certbot" certbot/certbot certonly \
  --non-interactive --webroot -w /var/www/certbot -d <домен> \
  --config-dir /etc/letsencrypt --work-dir /etc/letsencrypt/work --logs-dir /etc/letsencrypt/logs
docker exec mahalla-dev-nginx nginx -s reload
```

Продление — общий таймер сервера `mahalla-cert-renew`. Чтобы подключение к сети `taskflow_default` пережило пересоздание контейнера nginx, добавьте её в `~/mahalla-dev/nginx/docker-compose.nginx.yml` (блок `networks`). Контейнер nginx при этом не пересоздавать — на нём живут другие сайты.

### 4.4. GitHub App (по желанию, ТЗ §12)

Без App всё работает, интеграции просто нет. Чтобы включить:

1. GitHub → Settings → Developer settings → GitHub Apps → **New GitHub App** (у организации — в её настройках):
   - Homepage URL — `APP_BASE_URL`;
   - **Callback URL** — `<APP_BASE_URL>/api/v1/github/callback`;
   - **Webhook URL** — `<APP_BASE_URL>/api/v1/github/webhook`, **Webhook secret** — то же значение, что `GITHUB_WEBHOOK_SECRET`;
   - **Permissions → Repository:** Contents — Read-only, Metadata — Read-only, Pull requests — Read-only, Issues — **Read and write** (публикация задач в Issues, §12.9);
   - **Subscribe to events:** Pull request, Create, Delete;
   - «Where can this GitHub App be installed» — Only on this account (или Any account, если репозитории в разных организациях).
2. На странице App: записать **App ID** и **Client ID**, создать **Client secret** и **Private key** (скачается `.pem`).
3. На сервере:
   ```bash
   cd ~/taskflow && mkdir -p secrets
   cp ~/Downloads/<app>.private-key.pem secrets/github-app.pem
   # в .env: GITHUB_APP_ID, GITHUB_WEBHOOK_SECRET, GITHUB_CLIENT_ID, GITHUB_CLIENT_SECRET
   deploy/deploy.sh "$(tail -1 .deployed | cut -d' ' -f1)"   # тот же релиз: выставит права и перезапустит api и worker
   ```
   Права выставляет `deploy.sh`: каталог `secrets/` — ваш пользователь и группа `10001` (пользователь образа), `750`, файлы `640`; остальные пользователи сервера ключ не видят. Если `cp` пишет «Permission denied» — каталог когда-то создал docker от root: сначала `deploy/deploy.sh <текущий тег>`, он вернёт владельца.
4. **Install App** на нужные репозитории. Дальше менеджер проекта выбирает репозиторий во вкладке «GitHub» настроек проекта, а участники связывают свой аккаунт GitHub в профиле.

Если App уже был установлен до появления прав на Issues, GitHub попросит владельца репозитория **подтвердить новые права** (письмо и баннер в Settings → Integrations → GitHub Apps). Пока не подтвердят, публикация в Issues падает с 403 — это видно только в логе `worker`.

### 4.5. Таймеры бэкапа и проверки восстановления

```bash
deploy/install-timers.sh
systemctl --user list-timers 'taskflow-*'
```

Ставит `taskflow-backup.timer` (ежедневно в 03:30) и `taskflow-restore-check.timer` (1-го числа в 05:00; если после простоя оба срабатывают разом, проверка ждёт окончания бэкапа). Журналы: `journalctl --user -u taskflow-backup.service`, `journalctl --user -u taskflow-restore-check.service`.

## 5. Обновление по тегу и откат

Релиз — git-тег `vX.Y.Z` на `main`: GitHub Actions (`release.yml`) публикует образ `ghcr.io/jack5505/taskflow:vX.Y.Z` и GitHub Release со списком изменений.

```bash
cd ~/taskflow && git pull --ff-only     # актуальные deploy/ и docker-compose.yml
deploy/backup.sh pre-v1.2.3             # именованный снимок перед выкладкой (не удаляется по сроку)
deploy/deploy.sh v1.2.3
```

`deploy.sh` тянет образ, записывает `TASKFLOW_IMAGE` в `.env`, пересоздаёт `api` и `worker` (миграции применяет `api`, `worker` при старте ждёт их до 5 минут), ждёт `200` от `/health` до 3 минут и проверяет, что `worker` не перезапускается. Успешные выкладки дописываются в `.deployed`; хранятся три последних образа релизов. Две выкладки одновременно не пойдут — скрипт берёт блокировку `.deploy.lock`.

**Откат.**

- Автоматический: если `/health` не ответил или `worker` падает, `deploy.sh` возвращает предыдущий образ, ждёт его здоровья и выходит с ошибкой. Последние 50 строк логов `api` и `worker` — в выводе.
- Ручной: `deploy/deploy.sh <предыдущий тег>` (теги — `cat .deployed`).
- Если новая миграция всё же сломала совместимость со старым кодом: `deploy/deploy.sh <предыдущий тег>`, затем `deploy/restore.sh pre-v1.2.3` — данные вернутся к моменту снимка, всё, что пользователи сделали после выкладки, потеряется.

## 6. Бэкап и восстановление (ТЗ §10.3)

**Бэкап** — `deploy/backup.sh [метка]`:

- `db-<метка>.sql.gz` — `pg_dump` базы, `attachments-<метка>.tar.gz` — том вложений, `secrets-<метка>.tar.gz` — `.env` и `secrets/` (права `600`: в архиве все секреты стенда), с `BACKUP_SECRETS_PASSWORD` — ещё и зашифрованный `secrets-<метка>.tar.gz.enc`; метка по умолчанию — дата и время UTC (`20261011-0330`).
- Датированные файлы старше `BACKUP_KEEP_DAYS` (30) дней удаляются; именованные (`pre-…`, `check-…`) живут, пока их не удалят руками.
- Если секреты заархивировать не удалось (нет прав на `secrets/`, задан `GITHUB_APP_ID`, а ключа нет), база и вложения всё равно сохраняются и уходят наружу, а скрипт завершается с ошибкой — она видна в `systemctl --user status taskflow-backup.service`.
- С `BACKUP_REMOTE` копия сразу уходит во внешнее хранилище через rclone; открытый `secrets-….tar.gz` не уходит никогда, только `.enc` (в контейнере, ставить rclone не нужно). Без него скрипт пишет об этом в лог — для §10.3 внешнее хранилище обязательно, задайте его при развёртывании.
- Redis не бэкапится: очередь и ключи идемпотентности — производные данные.

**Секреты** `restore.sh` не трогает — на живом стенде `.env` уже правильный. На новом сервере их возвращают руками вместо шага `cp .env.example .env` из раздела 4.1 (его пропустить, иначе `.env` перезапишется образцом):

```bash
git clone https://github.com/jack5505/taskflow.git ~/taskflow && cd ~/taskflow
# из внешнего хранилища — зашифрованный архив; пароль — BACKUP_SECRETS_PASSWORD из вашего менеджера паролей
openssl enc -d -aes-256-cbc -pbkdf2 -iter 200000 -in secrets-<метка>.tar.gz.enc | tar xz   # или: tar xzf secrets-<метка>.tar.gz
docker compose up -d --wait postgres redis      # дальше — восстановление базы и раздел 4.2–4.5
```

**Восстановление на стенде** — `deploy/restore.sh <метка>`: останавливает `api` и `worker`, пересоздаёт базу из дампа, заменяет вложения, сбрасывает Redis (`FLUSHALL` — очередь и отметки обработанных событий относятся к прежнему состоянию базы), поднимает сервисы. **Текущие данные стенда заменяются**, скрипт спрашивает подтверждение (`RESTORE_YES=1` — без вопроса). Бэкап из внешнего хранилища сначала скачивается (как в разделе 7.2) и восстанавливается с `BACKUP_DIR=<каталог>`.

## 7. Ежемесячная проверка восстановления

### 7.1. Автоматическая: `deploy/restore-check.sh`

```bash
deploy/restore-check.sh            # последний полный бэкап (есть и дамп, и архив) из BACKUP_DIR
deploy/restore-check.sh 20261011-0330
cat backups/CHECKS.md              # журнал проверок
```

Скрипт не трогает стенд и его тома: поднимает временные контейнеры `taskflow-restorecheck-<pid>-*` в отдельной сети и после проверки удаляет их. Проверки:

1. временный PostgreSQL поднялся (иначе — провал «не поднялся», а не «битый бэкап»), дамп целиком восстанавливается в чистый PostgreSQL 16 (`ON_ERROR_STOP`);
2. `schema_migrations` есть и не `dirty`; если версия отличается от последней миграции в репозитории — предупреждение;
3. в базе есть пользователи; в журнал пишется число пользователей, проектов, задач, записей времени и вложений (по тем таблицам, что есть в бэкапе — старый `pre-v…` тоже проверяется);
4. каждое вложение из таблицы `attachments` есть в архиве вложений;
5. образ стенда (`TASKFLOW_IMAGE`) стартует на восстановленной базе, применяет миграции и отвечает `200` на `/health`. Секреты стенда не используются (свой `JWT_SECRET`, без SMTP и Telegram), `worker` не запускается — копия ничего не рассылает.

Результат — строка `OK` или `ПРОВАЛ — причина` в `$BACKUP_DIR/CHECKS.md` (другой журнал — `CHECKS_FILE=<файл>`). Строка пишется при любом исходе, в том числе если скрипт упал на docker или psql. Код выхода `0` только при успехе (провал виден в `systemctl --user status taskflow-restore-check.service`). Образ `TASKFLOW_IMAGE` берётся локальный: `deploy.sh` держит три последних релиза.

### 7.2. Что делает человек раз в месяц

1. После 1-го числа: `tail -3 backups/CHECKS.md` и `systemctl --user status taskflow-restore-check.service` — последняя проверка `OK`. При `ПРОВАЛ` — разобраться сегодня же: битый бэкап значит, что восстанавливаться не из чего.
2. Проверить **внешнюю копию** (на сервере её не видно — её и надо проверить, на случай потери сервера):
   ```bash
   cd ~/taskflow
   dir=$(sed -n 's/^BACKUP_DIR=//p' .env | tr -d '"'); dir=${dir:-$PWD/backups}
   remote=$(sed -n 's/^BACKUP_REMOTE=//p' .env | tr -d '"')
   label=$(ls "$dir" | sed -n 's/^db-\(20.*\)\.sql\.gz$/\1/p' | sort | tail -1)   # последний датированный
   mkdir -p /tmp/taskflow-remote-check
   docker run --rm -v "$PWD/deploy/rclone.conf:/config/rclone/rclone.conf:ro" -v /tmp/taskflow-remote-check:/in \
     rclone/rclone copy "$remote" /in --include "*-$label.*"
   CHECKS_FILE="$dir/CHECKS.md" BACKUP_DIR=/tmp/taskflow-remote-check deploy/restore-check.sh "$label"
   rm -rf /tmp/taskflow-remote-check
   ```
   Итог попадает в тот же журнал `CHECKS.md` стенда; в строке после метки — каталог, из которого проверялся бэкап, так что проверку внешней копии видно отдельно.

### 7.3. Полная проверка на копии стенда (по необходимости)

Перед рискованным релизом или если нужно руками посмотреть данные в интерфейсе — вторая копия стенда рядом с боевой. Имена контейнеров и томов зависят от `COMPOSE_PROJECT_NAME`, поэтому копия боевой стенд не задевает:

```bash
git clone ~/taskflow ~/taskflow-check && cd ~/taskflow-check
cp ../taskflow/.env .env
sed -i 's/^COMPOSE_PROJECT_NAME=.*/COMPOSE_PROJECT_NAME=taskflowcheck/; s/^TASKFLOW_PORT=.*/TASKFLOW_PORT=8101/; s/^SMTP_HOST=.*/SMTP_HOST=/; s/^TELEGRAM_BOT_TOKEN=.*/TELEGRAM_BOT_TOKEN=/; s/^GITHUB_APP_ID=.*/GITHUB_APP_ID=/' .env
docker compose up -d --wait postgres redis && deploy/deploy.sh <текущий тег>
BACKUP_DIR=$HOME/taskflow/backups RESTORE_YES=1 deploy/restore.sh <метка>
curl -s 127.0.0.1:8101/health     # и выборочно данные: ssh -L 8101:127.0.0.1:8101 … → http://localhost:8101
docker compose down -v && cd && rm -rf ~/taskflow-check
```

SMTP, токен бота и GitHub App в копии обнуляются: иначе `worker` копии разослал бы письма и сообщения и публиковал бы задачи в настоящие GitHub Issues из восстановленной очереди (а без ключа `secrets/`, которого в клоне нет, `api` и не стартовал бы).

## 8. Повседневное

| Задача | Команда |
|---|---|
| Состояние | `docker compose ps`, `curl -s 127.0.0.1:8100/health` |
| Логи (JSON, с `request_id`) | `docker compose logs -f --tail 100 api worker`; хранятся до 5 файлов по 20 МБ на сервис (`x-logging` в `docker-compose.yml`), старое удаляет Docker. Лимит действует на контейнер, созданный после его появления: `deploy.sh` пересоздаёт только `api` и `worker`, поэтому после выкладки v0.5.0 один раз `docker compose up -d postgres redis` (пересоздаст их, база и очередь — в томах, пауза несколько секунд) |
| Какой релиз стоит | `grep TASKFLOW_IMAGE .env`, `tail -3 .deployed` |
| Перезапуск без смены образа | `docker compose up -d --no-build api worker` |
| Консоль базы | `docker compose exec postgres psql -U taskflow` |
| Ещё один администратор | `docker compose exec api /app/taskflow admin create --email <email>` |
