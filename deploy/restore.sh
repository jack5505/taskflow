#!/usr/bin/env bash
# Восстановление из бэкапа (ТЗ §10.3, проверка раз в месяц):
#   deploy/restore.sh <метка>        — например 20261010-0330 или pre-v1.2.3
# Останавливает api и worker, пересоздаёт базу из дампа, распаковывает вложения, сбрасывает Redis
# (очередь, ключи идемпотентности и отметки выполненных обработчиков относятся к прежнему состоянию базы),
# поднимает всё обратно. Данные стенда ЗАМЕНЯЮТСЯ — скрипт просит подтверждение.
set -euo pipefail
# shellcheck source=deploy/lib.sh
. "$(dirname "$(readlink -f "$0")")/lib.sh"

LABEL=${1:-}
[ -n "$LABEL" ] || { echo "usage: deploy/restore.sh <метка бэкапа>" >&2; exit 2; }
DB_FILE="$BACKUP_DIR/db-$LABEL.sql.gz"
FILES_FILE="$BACKUP_DIR/attachments-$LABEL.tar.gz"
[ -f "$DB_FILE" ] || { echo "нет файла $DB_FILE" >&2; exit 2; }
[ -f "$FILES_FILE" ] || { echo "нет файла $FILES_FILE" >&2; exit 2; }

if [ "${RESTORE_YES:-}" != "1" ]; then
  read -r -p "Заменить базу, вложения и Redis проекта «$PROJECT» данными из $LABEL? [yes/N] " answer
  [ "$answer" = "yes" ] || { echo "отменено"; exit 1; }
fi

log "stop api, worker"
docker compose stop api worker >/dev/null

log "restore database"
compose_exec postgres psql -U taskflow -d postgres -v ON_ERROR_STOP=1 -q \
  -c "DROP DATABASE IF EXISTS taskflow;" -c "CREATE DATABASE taskflow OWNER taskflow;"
gunzip -c "$DB_FILE" | compose_exec postgres psql -U taskflow -d taskflow -v ON_ERROR_STOP=1 -q

log "restore attachments"
docker run --rm -v "${PROJECT}_attachments:/data" -v "$BACKUP_DIR:/in:ro" alpine \
  sh -c "find /data -mindepth 1 -delete; tar xzf /in/$(basename "$FILES_FILE") -C /data"

log "flush redis"
compose_exec redis redis-cli FLUSHALL >/dev/null

log "start api, worker"
docker compose up -d --no-build --wait api worker
log "OK: восстановлено из $LABEL"
