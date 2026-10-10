#!/bin/sh
# Порог покрытия серверного кода (ТЗ §10.4: не ниже 80%).
# Считается по всем пакетам с логикой; исключены точка входа, тестовые помощники,
# embed фронтенда и сгенерированная документация Swagger.
set -eu

THRESHOLD="${COVERAGE_THRESHOLD:-80}"
PROFILE="${1:-coverage.out}"
EXCLUDE='/cmd/|/internal/testutil$|/internal/web$|/internal/docs$|/migrations$'

PKGS=$(go list ./... | grep -Ev "$EXCLUDE" | tr '\n' ',' | sed 's/,$//')

# Вывод тестов показываем только при падении: при успехе важны лишь цифры покрытия.
LOG=$(mktemp)
if ! go test ./... -count=1 -coverpkg="$PKGS" -coverprofile="$PROFILE" >"$LOG" 2>&1; then
  grep -vE '^(ok|\?) ' "$LOG" | grep -v 'coverage: ' >&2
  rm -f "$LOG"
  exit 1
fi
rm -f "$LOG"

TOTAL=$(go tool cover -func="$PROFILE" | awk '/^total:/ {gsub("%","",$NF); print $NF}')
echo "coverage: ${TOTAL}% (threshold ${THRESHOLD}%)"
go tool cover -func="$PROFILE" | awk -v m=github.com/jack5505/taskflow/api/ '
  !/^total:/ { p=$1; sub(m,"",p); sub(/\/[^\/]*$/,"",p); gsub("%","",$NF); c[p]+=$NF; n[p]++ }
  END { for (p in c) printf "  %-28s %5.1f%%\n", p, c[p]/n[p] }' | sort

awk -v t="$TOTAL" -v th="$THRESHOLD" 'BEGIN { exit (t+0 >= th+0) ? 0 : 1 }' || {
  echo "coverage ${TOTAL}% is below ${THRESHOLD}%" >&2
  exit 1
}
