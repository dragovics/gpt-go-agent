#!/usr/bin/env bash
set -euo pipefail

BIN="${1:?usage: deploy.sh /path/to/gpt-go-agent}"
SERVICE=gpt-go-agent.service
TARGET=/usr/local/bin/gpt-go-agent
BACKUP="${TARGET}.previous"
TMP="${TARGET}.next"

if [[ $EUID -ne 0 ]]; then
  echo "run as root" >&2
  exit 1
fi
[[ -x "$BIN" ]] || { echo "artifact is not executable: $BIN" >&2; exit 1; }

install -o root -g root -m 0755 "$BIN" "$TMP"
sha256sum "$TMP"

if [[ -f "$TARGET" ]]; then
  cp -a "$TARGET" "$BACKUP"
fi

restore_previous() {
  systemctl stop "$SERVICE" || true
  if [[ -f "$BACKUP" ]]; then
    mv -f "$BACKUP" "$TARGET"
  fi
  systemctl start "$SERVICE" || true
}

systemctl stop "$SERVICE"
mv -f "$TMP" "$TARGET"
systemctl start "$SERVICE"

healthy=0
for _ in {1..20}; do
  if systemctl is-active --quiet "$SERVICE" \
    && curl -fsS http://127.0.0.1:8787/healthz >/dev/null \
    && curl -fsS http://127.0.0.1:8787/readyz >/dev/null; then
    healthy=1
    break
  fi
  sleep 1
done

if [[ "$healthy" -ne 1 ]]; then
  echo "deployment health check failed; restoring previous artifact" >&2
  restore_previous
  exit 1
fi

rm -f "$BACKUP"
echo "deployment healthy"
