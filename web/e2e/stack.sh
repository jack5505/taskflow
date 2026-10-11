#!/usr/bin/env bash
# Стенд для E2E (issue #46):
#   web/e2e/stack.sh up    — собрать образ, поднять api, worker, postgres, redis и завести
#                            администраторов; в stdout строки KEY=VALUE для тестов (в CI → $GITHUB_ENV)
#   web/e2e/stack.sh down  — удалить контейнеры и данные
# E2E_PROJECT (имя compose-проекта) и E2E_PORT меняют, чтобы не задеть рабочий стенд на 8100.
set -euo pipefail

cd "$(dirname "$(readlink -f "$0")")"
export E2E_PROJECT=${E2E_PROJECT:-taskflow-e2e}
export E2E_PORT=${E2E_PORT:-8199}
compose=(docker compose -f docker-compose.yml)

# Ссылка из вывода `taskflow admin create` (ТЗ §7.1: без SMTP она печатается в консоль).
# Пусто — у администратора уже есть пароль (повторный запуск на том же стенде).
admin_link() {
  "${compose[@]}" exec -T api /app/taskflow admin create --email "$1" --name "$2" |
    grep -o 'http[^ ]*/password-set?token=[^ ]*' || true
}

case ${1:-} in
  up)
    "${compose[@]}" up -d --build --wait >&2
    echo "E2E_BASE_URL=http://127.0.0.1:$E2E_PORT"
    # Администратор, от имени которого тесты готовят данные: пароль задаёт global-setup.
    echo "E2E_ADMIN_LINK=$(admin_link e2e-admin@example.com 'E2E Администратор')"
    # Первый администратор для сценария «вход по ссылке»: каждый прогон — новый адрес.
    login_email="first-admin-$(date +%s)@example.com"
    echo "E2E_LOGIN_EMAIL=$login_email"
    echo "E2E_LOGIN_LINK=$(admin_link "$login_email" 'Первый администратор')"
    ;;
  down)
    "${compose[@]}" down -v --remove-orphans >&2
    ;;
  *)
    echo "usage: web/e2e/stack.sh up|down" >&2
    exit 2
    ;;
esac
