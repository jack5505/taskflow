#!/usr/bin/env bash
# Ставит пользовательские systemd-таймеры: ежедневный бэкап (03:30 по времени сервера) и
# ежемесячная проверка восстановления последнего бэкапа (1-го числа в 05:00, deploy/restore-check.sh).
# Запускать один раз на сервере от пользователя, под которым работает docker compose.
set -euo pipefail

# shellcheck source=deploy/lib.sh
. "$(dirname "$(readlink -f "$0")")/lib.sh"
UNIT_DIR="$HOME/.config/systemd/user"
mkdir -p "$UNIT_DIR"

cat > "$UNIT_DIR/taskflow-backup.service" <<EOF
[Unit]
Description=TaskFlow: ежедневный бэкап базы и вложений (ТЗ §10.3)

[Service]
Type=oneshot
WorkingDirectory=$ROOT
ExecStart=$ROOT/deploy/backup.sh
EOF

cat > "$UNIT_DIR/taskflow-backup.timer" <<'EOF'
[Unit]
Description=TaskFlow: бэкап каждый день в 03:30

[Timer]
OnCalendar=*-*-* 03:30:00
Persistent=true
RandomizedDelaySec=5min

[Install]
WantedBy=timers.target
EOF

cat > "$UNIT_DIR/taskflow-restore-check.service" <<EOF
[Unit]
Description=TaskFlow: проверка восстановления последнего бэкапа во временные контейнеры (ТЗ §10.3)

[Service]
Type=oneshot
WorkingDirectory=$ROOT
ExecStart=$ROOT/deploy/restore-check.sh
EOF

cat > "$UNIT_DIR/taskflow-restore-check.timer" <<'EOF'
[Unit]
Description=TaskFlow: проверка восстановления 1-го числа каждого месяца в 05:00

[Timer]
OnCalendar=*-*-01 05:00:00
Persistent=true

[Install]
WantedBy=timers.target
EOF

systemctl --user daemon-reload
systemctl --user enable --now taskflow-backup.timer taskflow-restore-check.timer
systemctl --user list-timers 'taskflow-*' --no-pager
echo "Чтобы таймер работал без активной сессии: sudo loginctl enable-linger $USER (если ещё не включено)"
