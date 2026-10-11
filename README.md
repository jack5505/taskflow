# TaskFlow

Система управления задачами и временем с Telegram-ботом: задачи по модели Jira, спринты, учёт времени, бот для взятия задач в работу прямо из группового чата.

Полное техническое задание: [docs/TZ.md](docs/TZ.md). Все задачи в [Issues](../../issues) привязаны к пунктам ТЗ и разбиты на два этапа (milestones): сначала API, веб и учёт времени, затем Telegram-бот.

## Стек

| Слой | Технология |
|---|---|
| Сервер и API | Go 1.25, Gin, GORM (без AutoMigrate), golang-migrate, swag + gin-swagger |
| Telegram-бот | go-telegram/bot — приём webhook в api, отправка в worker |
| База данных | PostgreSQL 16 |
| Очередь | Redis 7 + asynq, outbox-таблица в PostgreSQL |
| Веб-интерфейс | React 18, TypeScript, Vite, TanStack Query, dnd-kit; сборка встраивается в бинарник api |
| Тесты | go test + testify + testcontainers; Vitest + Playwright |

## Структура

```
api/                 Go: один бинарник `taskflow` с режимами api | worker | admin
  cmd/taskflow/      точка входа
  internal/          config, httpserver, web (embed фронтенда), далее — домены по ТЗ §3
  migrations/        SQL-миграции golang-migrate
  Dockerfile         многоэтапная сборка: web → go → alpine
web/                 React + TypeScript (Vite)
docs/TZ.md           техническое задание
docs/DEPLOY.md       развёртывание и сопровождение стенда
deploy/              скрипты выкладки, бэкапа и восстановления
docker-compose.yml   api, worker, postgres, redis
Makefile             сборка и тесты через docker, без локального Go
```

## Запуск стенда

Развёртывание на сервере, переменные окружения, обновление по тегу, откат, бэкап и проверка восстановления — [docs/DEPLOY.md](docs/DEPLOY.md). Локально из исходников:

```bash
cp .env.example .env        # заполнить JWT_SECRET (не короче 32 символов)
make up                     # docker compose up -d --build --wait
curl -s localhost:8100/health
docker compose exec api /app/taskflow admin create --email admin@example.com   # ссылка на пароль — в консоли
```

## Разработка

```bash
make build        # go build в контейнере golang:1.25
make test         # go test ./... (интеграционные — на PostgreSQL и Redis из testcontainers)
make cover        # тесты с порогом покрытия 80%, как в CI
make lint         # golangci-lint
make web-install  # npm ci
make web-dev      # Vite dev-сервер, /api проксируется на localhost:8100
make web-build    # сборка в api/internal/web/dist
make swag         # Swagger 2.0 из аннотаций → /api/docs
```

Первый администратор создаётся командой `docker compose exec api /app/taskflow admin create --email <email>` (ТЗ §7.1).

## CI/CD и стенд

GitHub Actions на каждый PR: `go vet`, golangci-lint, тесты с порогом покрытия 80%, eslint, сборка фронтенда и Docker-образа, shellcheck. Тег `vX.Y.Z` публикует образ в GHCR и создаёт GitHub Release; выкладка на стенд — `deploy/deploy.sh vX.Y.Z` с откатом при неудаче `/health`. Бэкап, восстановление, nginx и сертификат — в [docs/DEPLOY.md](docs/DEPLOY.md).
