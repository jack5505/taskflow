#!/usr/bin/env bash
# Выкладка релиза на стенд (ТЗ §10.2, CONTRIBUTING §8):
#   deploy/deploy.sh v1.2.3
# Тянет образ ghcr.io/jack5505/taskflow:v1.2.3, записывает TASKFLOW_IMAGE в .env,
# перезапускает api и worker (миграции применяет api при старте, ТЗ §10.2), ждёт /health.
# Если /health не ответил 200 за 3 минуты — возвращает предыдущий образ и выходит с ошибкой.
# Откатывается только образ: применённые миграции остаются (они вперёд-совместимые, CONTRIBUTING §8).
set -euo pipefail

TAG=${1:-}
[[ $TAG =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "usage: deploy/deploy.sh vX.Y.Z" >&2; exit 2; }

ROOT=$(cd "$(dirname "$(readlink -f "$0")")/.." && pwd)
IMAGE_REPO=${TASKFLOW_IMAGE_REPO:-ghcr.io/jack5505/taskflow}
IMAGE="$IMAGE_REPO:$TAG"
HEALTH_URL=${TASKFLOW_HEALTH_URL:-http://127.0.0.1:8100/health}
WAIT_SECONDS=${TASKFLOW_DEPLOY_WAIT:-180}
KEEP_IMAGES=3

cd "$ROOT"
[ -f .env ] || { echo ".env не найден: cp .env.example .env и заполните секреты" >&2; exit 2; }

exec 9>".deploy.lock"
flock -w 900 9 || { echo "другая выкладка не завершилась за 15 минут" >&2; exit 1; }
log() { echo "[$(date -u +%FT%TZ)] $*"; }

# Пакет в GHCR приватный: логинимся токеном gh, если он есть (на сервере gh авторизован).
if [[ $IMAGE_REPO == ghcr.io/* ]] && command -v gh >/dev/null 2>&1; then
  gh auth token 2>/dev/null | docker login ghcr.io -u "${GHCR_USER:-jack5505}" --password-stdin >/dev/null 2>&1 \
    || log "docker login ghcr.io не удался — пробуем тянуть без него"
fi

log "pull $IMAGE"
docker pull --quiet "$IMAGE" >/dev/null

set_image() { sed -i '/^TASKFLOW_IMAGE=/d' .env; echo "TASKFLOW_IMAGE=$1" >> .env; }
PREV=$(sed -n 's/^TASKFLOW_IMAGE=//p' .env | tail -1)
PREV=${PREV:-$(docker inspect -f '{{.Config.Image}}' taskflow-api 2>/dev/null || true)}

healthy() {
  local deadline=$((SECONDS + WAIT_SECONDS))
  while (( SECONDS < deadline )); do
    if curl -fsS --max-time 3 "$HEALTH_URL" >/dev/null 2>&1; then return 0; fi
    sleep 3
  done
  return 1
}

up() { docker compose up -d --no-build --remove-orphans api worker; }

log "up $IMAGE (было: ${PREV:-нет})"
set_image "$IMAGE"
if ! up || ! healthy; then
  log "api не ответил 200 на $HEALTH_URL за ${WAIT_SECONDS}s"
  docker logs --tail 50 taskflow-api >&2 || true
  if [ -n "$PREV" ] && [ "$PREV" != "$IMAGE" ]; then
    log "откат на $PREV"
    set_image "$PREV"
    up || true
    if healthy; then
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
