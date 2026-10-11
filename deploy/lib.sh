#!/usr/bin/env bash
# Общая часть скриптов deploy/: каталог проекта, чтение .env, имена, лог.
# Подключается: . "$(dirname "$(readlink -f "$0")")/lib.sh"
# shellcheck shell=bash
# shellcheck disable=SC2034  # переменные используют подключающие скрипты

ROOT=$(cd "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/.." && pwd)
cd "$ROOT" || exit 1

# env_get KEY [default] — значение из .env без source: там бывают значения с пробелами и спецсимволами
# (SMTP_FROM="TaskFlow <noreply@…>"), на которых bash-source ломается молча.
env_get() {
  local v
  v=$(sed -n "s/^$1=//p" .env 2>/dev/null | tail -1)
  v=${v%\"}; v=${v#\"}
  printf '%s' "${v:-${2:-}}"
}

# env_set KEY VALUE — заменить или добавить строку в .env; файл всегда заканчивается переводом строки.
env_set() {
  local tmp
  tmp=$(mktemp)
  { grep -v "^$1=" .env 2>/dev/null || true; echo "$1=$2"; } > "$tmp"
  mv "$tmp" .env
}

log() { echo "[$(date -u +%FT%TZ)] $*"; }

PROJECT=$(env_get COMPOSE_PROJECT_NAME "$(basename "$ROOT")")
export COMPOSE_PROJECT_NAME="$PROJECT"
# BACKUP_DIR из окружения важнее .env: так проверяют бэкап, скачанный из внешнего хранилища в другой каталог.
BACKUP_DIR=${BACKUP_DIR:-$(env_get BACKUP_DIR "$ROOT/backups")}
BACKUP_KEEP_DAYS=$(env_get BACKUP_KEEP_DAYS 30)
BACKUP_REMOTE=$(env_get BACKUP_REMOTE)
PORT=$(env_get TASKFLOW_PORT 8100)

# compose_exec SERVICE CMD… — команда в контейнере сервиса; имена контейнеров не фиксированы,
# чтобы копия стенда с другим COMPOSE_PROJECT_NAME могла жить рядом (проверка восстановления).
compose_exec() { docker compose exec -T "$@"; }
