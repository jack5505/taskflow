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
# Разбивка по пакетам из профиля: доля покрытых выражений, а не среднее по функциям.
# Один блок встречается в профиле по разу на каждый тестовый бинарник — берём максимум счётчика.
awk -v m=github.com/jack5505/taskflow/api/ '
  NR == 1 { next }                                  # строка mode:
  { stmts[$1] = $2; if ($3 > cnt[$1]) cnt[$1] = $3 }
  END {
    for (k in stmts) {
      split(k, f, ":"); p = f[1]; sub(m, "", p); sub(/\/[^\/]*$/, "", p)
      total[p] += stmts[k]; if (cnt[k] > 0) covered[p] += stmts[k]
    }
    for (p in total) printf "  %-28s %5.1f%%\n", p, 100 * covered[p] / total[p]
  }' "$PROFILE" | sort

awk -v t="$TOTAL" -v th="$THRESHOLD" 'BEGIN { exit (t+0 >= th+0) ? 0 : 1 }' || {
  echo "coverage ${TOTAL}% is below ${THRESHOLD}%" >&2
  exit 1
}
