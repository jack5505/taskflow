#!/usr/bin/env bash
# Восстановление из бэкапа (ТЗ §10.3, проверка раз в месяц):
#   deploy/restore.sh <метка>        — например 20261010-0330 или pre-v1.2.3
# Останавливает api и worker, пересоздаёт базу из дампа, распаковывает вложения, поднимает всё обратно.
# Данные стенда ЗАМЕНЯЮТСЯ — перед запуском скрипт просит подтверждение.
set -euo pipefail

LABEL=${1:-}
[ -n "$LABEL" ] || { echo "usage: deploy/restore.sh <метка бэкапа>" >&2; exit 2; }

ROOT=$(cd "$(dirname "$(readlink -f "$0")")/.." && pwd)
cd "$ROOT"
# shellcheck source=/dev/null
[ -f .env ] && set -a && . ./.env && set +a
BACKUP_DIR=${BACKUP_DIR:-$ROOT/backups}
PROJECT=${COMPOSE_PROJECT_NAME:-taskflow}
DB_FILE="$BACKUP_DIR/db-$LABEL.sql.gz"
FILES_FILE="$BACKUP_DIR/attachments-$LABEL.tar.gz"
[ -f "$DB_FILE" ] || { echo "нет файла $DB_FILE" >&2; exit 2; }
[ -f "$FILES_FILE" ] || { echo "нет файла $FILES_FILE" >&2; exit 2; }

if [ "${RESTORE_YES:-}" != "1" ]; then
  read -r -p "Заменить базу и вложения стенда данными из $LABEL? [yes/N] " answer
  [ "$answer" = "yes" ] || { echo "отменено"; exit 1; }
fi
log() { echo "[$(date -u +%FT%TZ)] $*"; }

log "stop api, worker"
docker compose stop api worker >/dev/null

log "restore database"
docker exec taskflow-postgres psql -U taskflow -d postgres -v ON_ERROR_STOP=1 -q \
  -c "DROP DATABASE IF EXISTS taskflow;" -c "CREATE DATABASE taskflow OWNER taskflow;"
gunzip -c "$DB_FILE" | docker exec -i taskflow-postgres psql -U taskflow -d taskflow -v ON_ERROR_STOP=1 -q

log "restore attachments"
docker run --rm -v "${PROJECT}_attachments:/data" -v "$BACKUP_DIR:/in:ro" alpine \
  sh -c "rm -rf /data/* /data/..?* /data/.[!.]* 2>/dev/null; tar xzf /in/$(basename "$FILES_FILE") -C /data"

log "start api, worker"
docker compose up -d --no-build --wait api worker
log "OK: восстановлено из $LABEL"
