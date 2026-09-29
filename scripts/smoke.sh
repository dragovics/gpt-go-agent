#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${BASE_URL:-http://127.0.0.1:8787}"

curl -fsS "$BASE_URL/healthz"
printf '\n'
curl -fsS "$BASE_URL/readyz"
printf '\n'
curl -fsS "$BASE_URL/metrics"
printf '\n'
code="$(curl -sS -o /tmp/gpt-go-agent-unauth.out -w '%{http_code}' -X POST "$BASE_URL/webhook" -H 'Content-Type: application/json' --data '{"intent":"smoke"}')"
if [[ "$code" != "401" ]]; then
  echo "expected unauthenticated webhook to return 401, got $code" >&2
  cat /tmp/gpt-go-agent-unauth.out >&2 || true
  exit 1
fi

echo "unauthenticated webhook: 401"
