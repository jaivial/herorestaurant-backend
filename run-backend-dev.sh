#!/bin/sh
# Dev runner for the Go backend: sources backend/.env explicitly so the server
# connects to the intended database regardless of the ambient process env.
# Ambient MINIMAX_BASE_URL (e.g. the e2e MiniMax stub) is preserved.
PRE_MM="${MINIMAX_BASE_URL:-}"
set -a
. "$(dirname "$0")/.env"
set +a
if [ -n "$PRE_MM" ]; then
  export MINIMAX_BASE_URL="$PRE_MM"
fi
# Pin the e2e/dev port and the real dev database regardless of ambient env.
export PORT=8080
export STATIC_DIR=${FORKY_STATIC_DIR:-}
export DB_NAME=newvillacarmen
exec /tmp/go126/bin/go run ./cmd/server
