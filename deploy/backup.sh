#!/usr/bin/env bash
# Ежедневный бэкап (ТЗ §10.3): дамп PostgreSQL + архив тома вложений,
# хранение BACKUP_KEEP_DAYS (30) дней локально, копия на внешнее хранилище через rclone, если задан BACKUP_REMOTE.
#   deploy/backup.sh            — обычный запуск (таймер taskflow-backup.timer)
#   deploy/backup.sh pre-v1.2.3 — именованный снимок перед выкладкой, по сроку не удаляется
# Redis не бэкапится: очередь и ключи идемпотентности — производные данные, restore.sh их сбрасывает.
set -euo pipefail
# shellcheck source=deploy/lib.sh
. "$(dirname "$(readlink -f "$0")")/lib.sh"

LABEL=${1:-$(date -u +%Y%m%d-%H%M)}
mkdir -p "$BACKUP_DIR"

DB_FILE="$BACKUP_DIR/db-$LABEL.sql.gz"
FILES_FILE="$BACKUP_DIR/attachments-$LABEL.tar.gz"

log "pg_dump → $DB_FILE"
compose_exec postgres pg_dump -U taskflow --no-owner --no-privileges taskflow | gzip -9 > "$DB_FILE.tmp"
mv "$DB_FILE.tmp" "$DB_FILE"

log "attachments → $FILES_FILE"
docker run --rm -v "${PROJECT}_attachments:/data:ro" -v "$BACKUP_DIR:/out" alpine \
  tar czf "/out/$(basename "$FILES_FILE").tmp" -C /data .
mv "$FILES_FILE.tmp" "$FILES_FILE"

# Именованные снимки (pre-…, check-…) живут, пока их не удалят вручную; датированные — BACKUP_KEEP_DAYS.
find "$BACKUP_DIR" -maxdepth 1 -type f \( -name 'db-20*' -o -name 'attachments-20*' \) -mtime +"$BACKUP_KEEP_DAYS" -delete

if [ -n "$BACKUP_REMOTE" ]; then
  # rclone без установки на сервер: конфиг в deploy/rclone.conf (не в git), remote вида s3:bucket/taskflow
  log "rclone copy → $BACKUP_REMOTE"
  docker run --rm -v "$ROOT/deploy/rclone.conf:/config/rclone/rclone.conf:ro" -v "$BACKUP_DIR:/backups:ro" \
    rclone/rclone copy /backups "$BACKUP_REMOTE" --include "*-$LABEL.*"
else
  log "BACKUP_REMOTE не задан — копия на внешнее хранилище не делается"
fi

log "OK: $(du -h "$DB_FILE" | cut -f1) база, $(du -h "$FILES_FILE" | cut -f1) вложения"
