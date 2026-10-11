#!/usr/bin/env bash
# Нагрузочный тест (ТЗ §10.4) на временном стенде: образ из текущих исходников, отдельный
# compose-проект со своими томами, данные генератора (100 000 задач, 1 000 000 записей времени),
# k6 на 50 пользователей. Рабочий стенд не трогает: свои имена контейнеров и томов, порт api —
# случайный свободный на 127.0.0.1, k6 ходит в api по сети проекта. В конце всё удаляется.
#
#   loadtest/run.sh                       # итог k6 — в stdout и в loadtest/summary.txt
#   DURATION=1m VUS=20 loadtest/run.sh    # короче и меньше пользователей
#   KEEP=1 loadtest/run.sh                # не удалять стенд после прогона (для разбора)
#
# Нужны только docker и docker compose: генератор запускается из сборочного этапа того же Dockerfile
# (исходники и кеш сборки уже там), k6 — образ grafana/k6.
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
PROJECT=${LOADTEST_PROJECT:-tfload$$}
IMAGE=taskflow:${PROJECT}
K6_IMAGE=${K6_IMAGE:-grafana/k6:2.3.0}
PASSWORD=loadtest-password
WORK=$(mktemp -d)

log() { echo "[loadtest $(date +%H:%M:%S)] $*" >&2; }

# Каталог проекта compose — временный: в нём свой .env, настоящий .env стенда не читается.
dc() { docker compose -p "$PROJECT" -f "$ROOT/docker-compose.yml" --project-directory "$WORK" "$@"; }

# shellcheck disable=SC2317,SC2329  # вызывается из trap EXIT
cleanup() {
  if [[ ${KEEP:-} == 1 ]]; then
    log "KEEP=1: стенд оставлен — docker compose -p $PROJECT …; удаление: docker compose -p $PROJECT down -v && docker image rm $IMAGE $IMAGE-build"
  else
    log "удаляю стенд $PROJECT и образ $IMAGE"
    dc down -v --remove-orphans >/dev/null 2>&1 || true
    docker image rm "$IMAGE" "$IMAGE-build" >/dev/null 2>&1 || true
  fi
  rm -rf "$WORK"
}
trap cleanup EXIT

cat >"$WORK/.env" <<EOF
COMPOSE_PROJECT_NAME=$PROJECT
TASKFLOW_IMAGE=$IMAGE
TASKFLOW_PORT=0
APP_ENV=loadtest
APP_BASE_URL=http://localhost
DATABASE_URL=postgres://taskflow:taskflow@postgres:5432/taskflow?sslmode=disable
REDIS_ADDR=redis:6379
JWT_SECRET=$(head -c 32 /dev/urandom | base64)
JWT_ACCESS_TTL=24h
ATTACHMENTS_DIR=/data/attachments
EOF

log "сборка образа $IMAGE"
docker build -q -t "$IMAGE" -f "$ROOT/api/Dockerfile" "$ROOT" >/dev/null
docker build -q -t "$IMAGE-build" --target build -f "$ROOT/api/Dockerfile" "$ROOT" >/dev/null

log "postgres и redis"
dc up -d --wait postgres redis

log "генерация данных"
docker run --rm --network "${PROJECT}_default" -e CGO_ENABLED=0 "$IMAGE-build" go run ./cmd/loadgen \
  -dsn "postgres://taskflow:taskflow@postgres:5432/taskflow?sslmode=disable" -password "$PASSWORD" -users "${USERS:-200}"

log "api"
dc up -d --wait --no-build api

log "k6"
status=0
docker run --rm --network "${PROJECT}_default" -v "$ROOT/loadtest:/scripts:ro" \
  -e BASE_URL=http://api:8080 -e PASSWORD="$PASSWORD" -e VUS="${VUS:-50}" -e USERS="${USERS:-200}" -e DURATION="${DURATION:-3m}" \
  -e REPORT_EVERY="${REPORT_EVERY:-5}" -e K6_NO_COLOR=1 \
  "$K6_IMAGE" run --quiet /scripts/k6.js 2>&1 | tee "$ROOT/loadtest/summary.txt" || status=$?
log "итог k6 сохранён в loadtest/summary.txt"
exit "$status"
