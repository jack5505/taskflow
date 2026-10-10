#!/usr/bin/env bash
# Ставит пользовательский systemd-таймер ежедневного бэкапа (03:30 по времени сервера).
# Запускать один раз на сервере от пользователя, под которым работает docker compose.
set -euo pipefail

ROOT=$(cd "$(dirname "$(readlink -f "$0")")/.." && pwd)
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

systemctl --user daemon-reload
systemctl --user enable --now taskflow-backup.timer
systemctl --user list-timers taskflow-backup.timer --no-pager
echo "Чтобы таймер работал без активной сессии: sudo loginctl enable-linger $USER (если ещё не включено)"
