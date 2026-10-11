#!/usr/bin/env bash
# Ежемесячная проверка восстановления (ТЗ §10.3): бэкап разворачивается во временные контейнеры
# рядом со стендом, стенд и его данные не трогаются.
#   deploy/restore-check.sh            — последний бэкап из BACKUP_DIR
#   deploy/restore-check.sh <метка>    — конкретный, например 20261010-0330 или pre-v1.2.3
# Шаги: дамп → отдельный PostgreSQL 16 (ON_ERROR_STOP), версия миграций не dirty, в базе есть
# пользователи; каждое вложение из таблицы attachments есть в архиве; образ стенда (TASKFLOW_IMAGE)
# стартует на восстановленной базе — применяет миграции и отвечает 200 на /health. Секреты стенда
# не используются (свой JWT_SECRET, без SMTP и Telegram), так что копия ничего не рассылает.
# Итог — строкой в $BACKUP_DIR/CHECKS.md; код выхода 0 только если все проверки прошли.
set -euo pipefail
# shellcheck source=deploy/lib.sh
. "$(dirname "$(readlink -f "$0")")/lib.sh"

LABEL=${1:-}
if [ -z "$LABEL" ]; then
  latest=$(find "$BACKUP_DIR" -maxdepth 1 -name 'db-*.sql.gz' -printf '%T@ %f\n' 2>/dev/null | sort -n | tail -1 | cut -d' ' -f2)
  [ -n "$latest" ] || { echo "в $BACKUP_DIR нет бэкапов db-*.sql.gz" >&2; exit 2; }
  LABEL=${latest#db-}; LABEL=${LABEL%.sql.gz}
fi
DB_FILE="$BACKUP_DIR/db-$LABEL.sql.gz"
FILES_FILE="$BACKUP_DIR/attachments-$LABEL.tar.gz"
[ -f "$DB_FILE" ] || { echo "нет файла $DB_FILE" >&2; exit 2; }
[ -f "$FILES_FILE" ] || { echo "нет файла $FILES_FILE" >&2; exit 2; }

IMAGE=$(env_get TASKFLOW_IMAGE taskflow:local)
CHECK=taskflow-restorecheck-$$
PG="$CHECK-postgres"
API="$CHECK-api"
REDIS="$CHECK-redis"
WAIT_SECONDS=${RESTORE_CHECK_WAIT:-120}

# shellcheck disable=SC2329  # вызывается из trap EXIT
cleanup() {
  docker rm -fv "$API" "$REDIS" "$PG" >/dev/null 2>&1 || true
  docker network rm "$CHECK" >/dev/null 2>&1 || true
}
trap cleanup EXIT

fail() {
  log "ПРОВАЛ: $*"
  echo "- $(date -u +%F) \`$LABEL\`: ПРОВАЛ — $*" >> "$BACKUP_DIR/CHECKS.md"
  exit 1
}

psql_q() { docker exec "$PG" psql -U taskflow -d taskflow -At -v ON_ERROR_STOP=1 -c "$1"; }

log "проверка $LABEL: $(du -h "$DB_FILE" | cut -f1) база, $(du -h "$FILES_FILE" | cut -f1) вложения"
docker network create "$CHECK" >/dev/null
docker run -d --name "$PG" --network "$CHECK" -e POSTGRES_DB=taskflow -e POSTGRES_USER=taskflow \
  -e POSTGRES_PASSWORD=taskflow postgres:16-alpine >/dev/null
for _ in $(seq 60); do
  # pg_isready отвечает и во время initdb; готовность — когда проходит запрос по TCP.
  docker exec "$PG" psql -h 127.0.0.1 -U taskflow -d taskflow -c 'SELECT 1' >/dev/null 2>&1 && break
  sleep 1
done

log "восстановление базы во временный контейнер $PG"
gunzip -c "$DB_FILE" | docker exec -i "$PG" psql -U taskflow -d taskflow -v ON_ERROR_STOP=1 -q >/dev/null \
  || fail "дамп не восстановился (psql ON_ERROR_STOP)"

read -r version dirty <<<"$(psql_q "SELECT version, dirty FROM schema_migrations" | tr '|' ' ')"
[ -n "${version:-}" ] || fail "нет schema_migrations"
[ "$dirty" = "f" ] || fail "миграция $version помечена dirty"
repo_version=$(find "$ROOT/api/migrations" -name '*.up.sql' -printf '%f\n' 2>/dev/null | sort | tail -1 | cut -d_ -f1)
if [ -n "$repo_version" ] && [ "$repo_version" != "$version" ]; then
  log "версия схемы в бэкапе $version, последняя миграция в репозитории $repo_version (стенд на другом релизе?)"
fi

counts=$(psql_q "SELECT format('пользователей %s, проектов %s, задач %s, записей времени %s, вложений %s',
  (SELECT count(*) FROM users), (SELECT count(*) FROM projects), (SELECT count(*) FROM tasks),
  (SELECT count(*) FROM worklogs), (SELECT count(*) FROM attachments))")
log "$counts"
[ "$(psql_q "SELECT count(*) FROM users")" -gt 0 ] || fail "в базе нет пользователей"

log "вложения: файлы из таблицы attachments есть в архиве"
archived=$(mktemp)
expected=$(mktemp)
trap 'rm -f "$archived" "$expected"; cleanup' EXIT
tar tzf "$FILES_FILE" | sed 's|^\./||' | sed '/^$/d' | sort > "$archived" || fail "архив вложений не читается"
psql_q "SELECT storage_path FROM attachments" | sed 's|.*/||' | sort > "$expected"
missing=$(comm -23 "$expected" "$archived" | wc -l)
[ "$missing" -eq 0 ] || fail "в архиве нет $missing файлов вложений из базы"

log "образ $IMAGE на восстановленной базе"
docker run -d --name "$REDIS" --network "$CHECK" redis:7-alpine >/dev/null
docker run -d --name "$API" --network "$CHECK" \
  -e DATABASE_URL="postgres://taskflow:taskflow@$PG:5432/taskflow?sslmode=disable" \
  -e REDIS_ADDR="$REDIS:6379" -e JWT_SECRET="$(head -c 32 /dev/urandom | base64)" \
  -e APP_BASE_URL=http://localhost -e ATTACHMENTS_DIR=/tmp "$IMAGE" api >/dev/null
deadline=$((SECONDS + WAIT_SECONDS))
until docker exec "$API" wget -qO- http://127.0.0.1:8080/health >/dev/null 2>&1; do
  if (( SECONDS > deadline )) || [ "$(docker inspect -f '{{.State.Running}}' "$API")" != "true" ]; then
    docker logs --tail 30 "$API" >&2 || true
    fail "api ($IMAGE) не ответил на /health за ${WAIT_SECONDS}s"
  fi
  sleep 2
done
version_after=$(psql_q "SELECT version FROM schema_migrations")

log "OK: $LABEL восстанавливается; $counts; схема $version → $version_after"
echo "- $(date -u +%F) \`$LABEL\`: OK — $counts; схема $version, образ \`$IMAGE\`" >> "$BACKUP_DIR/CHECKS.md"
