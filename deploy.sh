#!/usr/bin/env bash
# Сборка reelkino и деплой на сервер по ssh (systemd).
set -euo pipefail

HOST="${HOST:-root@157.22.182.58}"
SSH_OPTS=(-o ConnectTimeout=10)

c() { # цвет с защитой от не-TTY / NO_COLOR
  if [[ -t 2 && -z "${NO_COLOR:-}" && "${TERM:-}" != "dumb" ]]; then
    printf '\033[%sm%s\033[0m' "$1" "$2"
  else
    printf '%s' "$2"
  fi
}
log() { echo "$(date '+%H:%M:%S') $(c 36 '[deploy]') $*" >&2; }

cd "$(dirname "$0")"

log "building linux/amd64 binary..."
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -o build/reelkino ./cmd/reelkino

log "uploading to ${HOST}..."
ssh "${SSH_OPTS[@]}" "$HOST" "mkdir -p /etc/reelkino /var/lib/reelkino"
scp "${SSH_OPTS[@]}" build/reelkino "${HOST}:/usr/local/bin/reelkino.new"
scp "${SSH_OPTS[@]}" deploy/reelkino.service "${HOST}:/etc/systemd/system/reelkino.service"
scp "${SSH_OPTS[@]}" deploy/reelkino.env.example "${HOST}:/etc/reelkino/reelkino.env.example"

log "installing on server..."
ssh "${SSH_OPTS[@]}" "$HOST" '
  set -euo pipefail
  mv /usr/local/bin/reelkino.new /usr/local/bin/reelkino
  chmod +x /usr/local/bin/reelkino
  if [ ! -f /etc/reelkino/reelkino.env ]; then
    cp /etc/reelkino/reelkino.env.example /etc/reelkino/reelkino.env
    chmod 600 /etc/reelkino/reelkino.env
    echo "NEED_CONFIG"
  fi
  # yt-dlp — скачивание рилсов
  if ! command -v yt-dlp >/dev/null; then
    echo "installing yt-dlp..."
    curl -fsSL -o /usr/local/bin/yt-dlp \
      https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp_linux
    chmod +x /usr/local/bin/yt-dlp
  fi
  mkdir -p /var/lib/reelkino/media
  # долить новые ключи в существующий env
  for kv in "ZEN_API_KEY=" "ZEN_MODEL=" "MEDIA_DIR=/var/lib/reelkino/media" "YTDLP_PATH=/usr/local/bin/yt-dlp" "YTDLP_COOKIES="; do
    grep -q "^${kv%%=*}=" /etc/reelkino/reelkino.env || echo "$kv" >> /etc/reelkino/reelkino.env
  done
  systemctl daemon-reload
'

if ! ssh "${SSH_OPTS[@]}" "$HOST" "grep -q '^ZEN_API_KEY=.\+' /etc/reelkino/reelkino.env"; then
  log "$(c 33 'ВНИМАНИЕ') — ZEN_API_KEY пуст в /etc/reelkino/reelkino.env: авто-определение фильма работать не будет."
fi

if ssh "${SSH_OPTS[@]}" "$HOST" "grep -q '^TG_BOT_TOKEN=.\+' /etc/reelkino/reelkino.env"; then
  log "restarting reelkino..."
  ssh "${SSH_OPTS[@]}" "$HOST" "systemctl enable --now reelkino && systemctl restart reelkino && sleep 1 && systemctl is-active reelkino"
  log "$(c 32 'OK') — reelkino запущен. Логи: ssh $HOST journalctl -u reelkino -f"
else
  log "$(c 33 'ВНИМАНИЕ') — /etc/reelkino/reelkino.env не заполнен."
  log "1) ssh $HOST  →  заполни TG_BOT_TOKEN и KINOPOISK_API_TOKEN в /etc/reelkino/reelkino.env"
  log "2) затем: ssh $HOST 'systemctl enable --now reelkino'"
fi
