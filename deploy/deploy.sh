#!/usr/bin/env bash
# Выкладка релиза на стенд (ТЗ §10.2, CONTRIBUTING §8):
#   deploy/deploy.sh v1.2.3
# Тянет образ ghcr.io/jack5505/taskflow:v1.2.3, записывает TASKFLOW_IMAGE в .env,
# перезапускает api и worker (миграции применяет api при старте, ТЗ §10.2), ждёт /health
# и проверяет, что worker не падает. Иначе возвращает предыдущий образ и выходит с ошибкой.
# Откатывается только образ: применённые миграции остаются (они вперёд-совместимые, CONTRIBUTING §8).
set -euo pipefail
# shellcheck source=deploy/lib.sh
. "$(dirname "$(readlink -f "$0")")/lib.sh"

TAG=${1:-}
[[ $TAG =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "usage: deploy/deploy.sh vX.Y.Z" >&2; exit 2; }
[ -f .env ] || { echo ".env не найден: cp .env.example .env и заполните секреты" >&2; exit 2; }

IMAGE_REPO=${TASKFLOW_IMAGE_REPO:-ghcr.io/jack5505/taskflow}
IMAGE="$IMAGE_REPO:$TAG"
HEALTH_URL=${TASKFLOW_HEALTH_URL:-http://127.0.0.1:$PORT/health}
WAIT_SECONDS=${TASKFLOW_DEPLOY_WAIT:-180}
WORKER_SETTLE=${TASKFLOW_WORKER_SETTLE:-10}
KEEP_IMAGES=3

exec 9>".deploy.lock"
flock -w 900 9 || { echo "другая выкладка не завершилась за 15 минут" >&2; exit 1; }

# Пакет в GHCR приватный: логинимся токеном gh, если он есть (на сервере gh авторизован).
if [[ $IMAGE_REPO == ghcr.io/* ]] && command -v gh >/dev/null 2>&1; then
  gh auth token 2>/dev/null | docker login ghcr.io -u "${GHCR_USER:-jack5505}" --password-stdin >/dev/null 2>&1 \
    || log "docker login ghcr.io не удался — пробуем тянуть без него"
fi

if [ "${TASKFLOW_SKIP_PULL:-}" = "1" ]; then
  log "pull пропущен (TASKFLOW_SKIP_PULL=1), используем локальный $IMAGE"
  docker image inspect "$IMAGE" >/dev/null
else
  log "pull $IMAGE"
  docker pull --quiet "$IMAGE" >/dev/null
fi

PREV=$(env_get TASKFLOW_IMAGE)
if [ -z "$PREV" ]; then
  running=$(docker compose ps -q api 2>/dev/null || true)
  [ -n "$running" ] && PREV=$(docker inspect -f '{{.Config.Image}}' "$running" 2>/dev/null || true)
fi

api_healthy() {
  local deadline=$((SECONDS + WAIT_SECONDS))
  while (( SECONDS < deadline )); do
    if curl -fsS --max-time 3 "$HEALTH_URL" >/dev/null 2>&1; then return 0; fi
    sleep 3
  done
  log "api не ответил 200 на $HEALTH_URL за ${WAIT_SECONDS}s"
  return 1
}

# worker здоров, если через WORKER_SETTLE секунд после старта он всё ещё running и не перезапускался:
# у него нет HTTP, а падение при старте проявляется именно рестартами.
worker_stable() {
  local id status restarts
  id=$(docker compose ps -q worker)
  [ -n "$id" ] || { log "контейнер worker не найден"; return 1; }
  sleep "$WORKER_SETTLE"
  status=$(docker inspect -f '{{.State.Status}}' "$id")
  restarts=$(docker inspect -f '{{.RestartCount}}' "$id")
  if [ "$status" != "running" ] || [ "$restarts" != "0" ]; then
    log "worker нестабилен: status=$status, перезапусков=$restarts"
    return 1
  fi
}

up() { docker compose up -d --no-build --remove-orphans api worker; }

log "up $IMAGE (было: ${PREV:-нет})"
env_set TASKFLOW_IMAGE "$IMAGE"
if ! up || ! api_healthy || ! worker_stable; then
  docker compose logs --tail 50 api worker >&2 || true
  if [ -n "$PREV" ] && [ "$PREV" != "$IMAGE" ]; then
    log "откат на $PREV"
    env_set TASKFLOW_IMAGE "$PREV"
    up || true
    if api_healthy && worker_stable; then
      log "откат выполнен, стенд на $PREV"
    else
      log "ВНИМАНИЕ: стенд не поднялся и после отката"
    fi
  fi
  exit 1
fi

echo "$TAG $(date -u +%FT%TZ)" >> .deployed
# Старые образы релизов убираем, оставляя последние KEEP_IMAGES для быстрого отката.
docker images --format '{{.Repository}}:{{.Tag}}' "$IMAGE_REPO" | grep -E ':v[0-9]+\.[0-9]+\.[0-9]+$' \
  | grep -vxF "$IMAGE" | tail -n +"$KEEP_IMAGES" | xargs -r docker rmi >/dev/null 2>&1 || true
log "OK: $IMAGE"
