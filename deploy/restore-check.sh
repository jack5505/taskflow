#!/usr/bin/env bash
# Ежемесячная проверка восстановления (ТЗ §10.3): бэкап разворачивается во временные контейнеры
# рядом со стендом, стенд и его данные не трогаются.
#   deploy/restore-check.sh            — последний бэкап из BACKUP_DIR
#   deploy/restore-check.sh <метка>    — конкретный, например 20261010-0330 или pre-v1.2.3
# Шаги: дамп → отдельный PostgreSQL 16 (ON_ERROR_STOP), версия миграций не dirty, в базе есть
# пользователи; каждое вложение из таблицы attachments есть в архиве; образ стенда (TASKFLOW_IMAGE)
# стартует на восстановленной базе — применяет миграции и отвечает 200 на /health. Секреты стенда
# не используются (свой JWT_SECRET, без SMTP и Telegram), так что копия ничего не рассылает.
# Итог — строкой в $BACKUP_DIR/CHECKS.md (CHECKS_FILE — другой журнал, например боевой при проверке
# внешней копии); код выхода 0 только если все проверки прошли.
set -euo pipefail
# shellcheck source=deploy/lib.sh
. "$(dirname "$(readlink -f "$0")")/lib.sh"

CHECKS_FILE=${CHECKS_FILE:-$BACKUP_DIR/CHECKS.md}
IMAGE=$(env_get TASKFLOW_IMAGE taskflow:local)
CHECK=taskflow-restorecheck-$$
PG="$CHECK-postgres"
API="$CHECK-api"
REDIS="$CHECK-redis"
WAIT_SECONDS=${RESTORE_CHECK_WAIT:-120}
LABEL=${1:-}
TMP_FILES=()
RECORDED=""
record() {
  RECORDED=1
  mkdir -p "$(dirname "$CHECKS_FILE")"
  echo "- $(date -u +%F) \`${LABEL:-?}\` ($BACKUP_DIR): $*" >> "$CHECKS_FILE"
}

# Любой выход без записи в журнал (упал docker, нет образа, ошибка psql) — тоже ПРОВАЛ в журнале:
# иначе ежемесячный взгляд на CHECKS.md увидел бы прошлый OK.
# shellcheck disable=SC2317,SC2329  # вызывается из trap EXIT
cleanup() {
  local rc=$?
  [ -n "$RECORDED" ] || record "ПРОВАЛ — прервано с кодом $rc, подробности в выводе скрипта (journalctl --user -u taskflow-restore-check)"
  docker rm -fv "$API" "$REDIS" "$PG" >/dev/null 2>&1 || true
  docker network rm "$CHECK" >/dev/null 2>&1 || true
  rm -f "${TMP_FILES[@]}"
}
trap cleanup EXIT

fail() {
  log "ПРОВАЛ: $*"
  record "ПРОВАЛ — $*"
  exit 1
}

if [ -z "$LABEL" ]; then
  # Последний полный бэкап: у которого есть и дамп, и архив вложений (backup.sh мог ещё писать архив).
  while read -r f; do
    l=${f#db-}; l=${l%.sql.gz}
    if [ -f "$BACKUP_DIR/attachments-$l.tar.gz" ]; then LABEL=$l; break; fi
  done < <(find "$BACKUP_DIR" -maxdepth 1 -name 'db-*.sql.gz' -printf '%T@ %f\n' 2>/dev/null | sort -rn | cut -d' ' -f2)
  [ -n "$LABEL" ] || fail "в $BACKUP_DIR нет полного бэкапа (db-*.sql.gz и attachments-*.tar.gz)"
fi
DB_FILE="$BACKUP_DIR/db-$LABEL.sql.gz"
FILES_FILE="$BACKUP_DIR/attachments-$LABEL.tar.gz"
[ -f "$DB_FILE" ] || fail "нет файла $DB_FILE"
[ -f "$FILES_FILE" ] || fail "нет файла $FILES_FILE"

psql_q() { docker exec "$PG" psql -U taskflow -d taskflow -At -v ON_ERROR_STOP=1 -c "$1"; }

log "проверка $LABEL: $(du -h "$DB_FILE" | cut -f1) база, $(du -h "$FILES_FILE" | cut -f1) вложения"
docker network create "$CHECK" >/dev/null
docker run -d --name "$PG" --network "$CHECK" -e POSTGRES_DB=taskflow -e POSTGRES_USER=taskflow \
  -e POSTGRES_PASSWORD=taskflow postgres:16-alpine >/dev/null
ready=""
for _ in $(seq "$WAIT_SECONDS"); do
  # pg_isready отвечает и во время initdb; готовность — когда проходит запрос по TCP.
  if docker exec "$PG" psql -h 127.0.0.1 -U taskflow -d taskflow -c 'SELECT 1' >/dev/null 2>&1; then ready=1; break; fi
  sleep 1
done
[ -n "$ready" ] || fail "временный PostgreSQL не поднялся за ${WAIT_SECONDS}s (бэкап не проверен)"

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

# Счётчики только по существующим таблицам: в старом бэкапе (pre-v…) части таблиц ещё нет.
has_table() { [ "$(psql_q "SELECT to_regclass('public.$1') IS NOT NULL")" = "t" ]; }
counts=""
for t in users:пользователей projects:проектов tasks:задач worklogs:"записей времени" attachments:вложений; do
  has_table "${t%%:*}" || continue
  counts+="${counts:+, }${t#*:} $(psql_q "SELECT count(*) FROM ${t%%:*}")"
done
log "$counts"
if ! has_table users || [ "$(psql_q "SELECT count(*) FROM users")" -eq 0 ]; then fail "в базе нет пользователей"; fi

log "вложения: файлы из таблицы attachments есть в архиве"
archived=$(mktemp)
expected=$(mktemp)
TMP_FILES+=("$archived" "$expected")
tar tzf "$FILES_FILE" | sed 's|^\./||' | sed '/^$/d' | sort > "$archived" || fail "архив вложений не читается"
if has_table attachments; then
  psql_q "SELECT storage_path FROM attachments" | sed 's|.*/||' | sort > "$expected"
fi
missing=$(comm -23 "$expected" "$archived" | wc -l)
[ "$missing" -eq 0 ] || fail "в архиве нет $missing файлов вложений из базы"

log "образ $IMAGE на восстановленной базе"
docker run -d --name "$REDIS" --network "$CHECK" redis:7-alpine >/dev/null
docker image inspect "$IMAGE" >/dev/null 2>&1 || fail "нет локального образа $IMAGE (deploy.sh держит три последних релиза)"
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
record "OK — $counts; схема $version, образ \`$IMAGE\`"
