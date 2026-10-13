#!/usr/bin/env bash
# Ежедневный бэкап (ТЗ §10.3): дамп PostgreSQL + архив тома вложений + секреты стенда (.env, secrets/),
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
SECRETS_FILE="$BACKUP_DIR/secrets-$LABEL.tar.gz"

log "pg_dump → $DB_FILE"
compose_exec postgres pg_dump -U taskflow --no-owner --no-privileges taskflow | gzip -9 > "$DB_FILE.tmp"
mv "$DB_FILE.tmp" "$DB_FILE"

log "attachments → $FILES_FILE"
docker run --rm -v "${PROJECT}_attachments:/data:ro" -v "$BACKUP_DIR:/out" alpine \
  tar czf "/out/$(basename "$FILES_FILE").tmp" -C /data .
mv "$FILES_FILE.tmp" "$FILES_FILE"

# .env (JWT_SECRET, токен бота, SMTP) и secrets/ (ключ GitHub App) — чтобы после потери сервера их не
# перевыпускать. Локально — только для владельца; во внешнее хранилище уходит лишь зашифрованная копия
# (.enc, пароль BACKUP_SECRETS_PASSWORD), открытый архив — никогда. restore.sh их не трогает (DEPLOY.md §6).
# Сбой здесь не мешает отправить базу и вложения: скрипт доделывает остальное и в конце выходит с ошибкой.
status=0
log "secrets → $SECRETS_FILE"
secrets=(.env)
[ -d secrets ] && secrets+=(secrets)
key=$(env_get GITHUB_APP_PRIVATE_KEY_FILE /run/secrets/github-app.pem)
if [ -n "$(env_get GITHUB_APP_ID)" ] && [ ! -f "secrets/${key#/run/secrets/}" ]; then
  log "ПРЕДУПРЕЖДЕНИЕ: GITHUB_APP_ID задан, а ключа secrets/${key#/run/secrets/} нет — в бэкапе его не будет"
  status=1
fi
if (umask 077 && tar czf "$SECRETS_FILE.tmp" "${secrets[@]}"); then
  mv "$SECRETS_FILE.tmp" "$SECRETS_FILE"
  if [ -n "$BACKUP_SECRETS_PASSWORD" ]; then
    (umask 077 && BACKUP_SECRETS_PASSWORD=$BACKUP_SECRETS_PASSWORD openssl enc -aes-256-cbc -pbkdf2 -iter 200000 -salt \
      -pass env:BACKUP_SECRETS_PASSWORD -in "$SECRETS_FILE" -out "$SECRETS_FILE.enc")
  fi
else
  rm -f "$SECRETS_FILE.tmp"
  log "ОШИБКА: не удалось заархивировать секреты (права на secrets/? см. DEPLOY.md §4.4)"
  status=1
fi

# Именованные снимки (pre-…, check-…) живут, пока их не удалят вручную; датированные — BACKUP_KEEP_DAYS.
find "$BACKUP_DIR" -maxdepth 1 -type f \( -name 'db-20*' -o -name 'attachments-20*' -o -name 'secrets-20*' \) -mtime +"$BACKUP_KEEP_DAYS" -delete

if [ -n "$BACKUP_REMOTE" ]; then
  # rclone без установки на сервер: конфиг в deploy/rclone.conf (не в git), remote вида s3:bucket/taskflow
  log "rclone copy → $BACKUP_REMOTE"
  docker run --rm -v "$ROOT/deploy/rclone.conf:/config/rclone/rclone.conf:ro" -v "$BACKUP_DIR:/backups:ro" \
    rclone/rclone copy /backups "$BACKUP_REMOTE" --filter "- secrets-*.tar.gz" --filter "+ *-$LABEL.*" --filter "- *"
  [ -n "$BACKUP_SECRETS_PASSWORD" ] || log "BACKUP_SECRETS_PASSWORD не задан — секреты во внешнее хранилище не копируются"
else
  log "BACKUP_REMOTE не задан — копия на внешнее хранилище не делается"
fi

secrets_size=$( [ -f "$SECRETS_FILE" ] && du -h "$SECRETS_FILE" | cut -f1 || echo "нет")
if [ "$status" -ne 0 ]; then
  log "ЗАВЕРШЕНО С ОШИБКАМИ: $(du -h "$DB_FILE" | cut -f1) база, $(du -h "$FILES_FILE" | cut -f1) вложения, секреты — $secrets_size"
  exit "$status"
fi
log "OK: $(du -h "$DB_FILE" | cut -f1) база, $(du -h "$FILES_FILE" | cut -f1) вложения, $secrets_size секреты"
