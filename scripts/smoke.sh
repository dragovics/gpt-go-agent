#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${BASE_URL:-http://127.0.0.1:8787}"
WEBHOOK_ENABLED="${WEBHOOK_ENABLED:-0}"

curl -fsS "$BASE_URL/healthz"
printf '\n'
curl -fsS "$BASE_URL/readyz"
printf '\n'
curl -fsS "$BASE_URL/metrics"
printf '\n'

if [[ "$WEBHOOK_ENABLED" == "1" ]]; then
  code="$(curl -sS -o /tmp/gpt-go-agent-webhook.out -w '%{http_code}' -X POST "$BASE_URL/webhook" -H 'Content-Type: application/json' --data '{"intent":"smoke"}')"
  if [[ "$code" != "401" ]]; then
    echo "expected unauthenticated webhook to return 401, got $code" >&2
    cat /tmp/gpt-go-agent-webhook.out >&2 || true
    exit 1
  fi
  echo "unauthenticated webhook: 401"
else
  code="$(curl -sS -o /tmp/gpt-go-agent-webhook.out -w '%{http_code}' -X POST "$BASE_URL/webhook" -H 'Content-Type: application/json' --data '{"intent":"smoke"}')"
  if [[ "$code" != "404" ]]; then
    echo "expected disabled webhook to return 404, got $code" >&2
    exit 1
  fi
  echo "disabled webhook: 404"
fi
