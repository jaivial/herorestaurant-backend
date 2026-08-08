#!/bin/sh
# Forky tool tests: unit only by default; --with-db spins up a throwaway MySQL
# container, applies the SQL migrations and runs the integration tests too.
#
#   ./scripts/test-forky.sh            # unit tests only
#   ./scripts/test-forky.sh --with-db  # unit + integration (docker MySQL)
set -e
cd "$(dirname "$0")/.."

GO="${GO:-/tmp/go126/bin/go}"
[ -x "$GO" ] || GO=go

if [ "${1:-}" = "--with-db" ]; then
  CONTAINER="${FORKY_TEST_MYSQL_CONTAINER:-forky-test-mysql}"
  PORT="${FORKY_TEST_MYSQL_PORT:-3307}"
  DB="forky_test"
  ROOTPW="root"

  if ! docker ps --format '{{.Names}}' | grep -q "^${CONTAINER}$"; then
    echo "starting MySQL container ${CONTAINER} on :${PORT}"
    docker run -d --name "$CONTAINER" \
      -e MYSQL_ROOT_PASSWORD="$ROOTPW" \
      -e MYSQL_DATABASE="$DB" \
      -p "${PORT}:3306" \
      mysql:8.0 >/dev/null
  fi

  i=0
  until docker exec "$CONTAINER" mysql -uroot -p"$ROOTPW" -e 'SELECT 1' >/dev/null 2>&1; do
    i=$((i + 1))
    [ "$i" -gt 90 ] && { echo "MySQL did not become ready"; exit 1; }
    sleep 1
  done
  # MySQL 8 restarts once after first-boot initialization; let it settle.
  sleep 3

  # The test DB mirrors the current dev schema. Dump structure (all migrations
  # already applied in the source DB) and restore it into the container, so no
  # migration replay is needed. Source DB defaults to backend/.env (DB_* vars
  # are extracted with grep because the file also holds non-shell-safe lines).
  env_var() {
    sed -n "s/^${1}=//p" .env 2>/dev/null | head -1
  }
  SRC_HOST="${FORKY_SRC_DB_HOST:-}"; SRC_HOST="${SRC_HOST:-$(env_var DB_HOST)}"; SRC_HOST="${SRC_HOST:-127.0.0.1}"
  SRC_PORT="${FORKY_SRC_DB_PORT:-}"; SRC_PORT="${SRC_PORT:-$(env_var DB_PORT)}"; SRC_PORT="${SRC_PORT:-3306}"
  SRC_USER="${FORKY_SRC_DB_USER:-}"; SRC_USER="${SRC_USER:-$(env_var DB_USER)}"; SRC_USER="${SRC_USER:-root}"
  SRC_PASSWORD="${FORKY_SRC_DB_PASSWORD:-}"; SRC_PASSWORD="${SRC_PASSWORD:-$(env_var DB_PASSWORD)}"
  SRC_DB="${FORKY_SRC_DB_NAME:-}"; SRC_DB="${SRC_DB:-$(env_var DB_NAME)}"
  if [ -z "$SRC_DB" ]; then
    echo "no source database configured (DB_NAME/DB_* in backend/.env); set FORKY_SRC_DB_NAME"
    exit 1
  fi
  echo "copying structure of ${SRC_DB} from ${SRC_HOST}:${SRC_PORT}"
  # Ignore views: mysqldump aborts on broken views (e.g. recent_conversations).
  VIEWS=$(mysql -h"$SRC_HOST" -P"$SRC_PORT" -u"$SRC_USER" -p"$SRC_PASSWORD" -N \
    -e "SELECT table_name FROM information_schema.tables WHERE table_schema='${SRC_DB}' AND table_type='VIEW'" 2>/dev/null)
  IGNORES=""
  for v in $VIEWS; do IGNORES="$IGNORES --ignore-table=${SRC_DB}.${v}"; done
  mysqldump -h"$SRC_HOST" -P"$SRC_PORT" -u"$SRC_USER" -p"$SRC_PASSWORD" \
    --no-data --skip-comments --skip-triggers --no-create-db --set-gtid-purged=OFF $IGNORES \
    "$SRC_DB" | docker exec -i "$CONTAINER" mysql -uroot -p"$ROOTPW" "$DB"

  export FORKY_TEST_MYSQL_DSN="root:${ROOTPW}@tcp(127.0.0.1:${PORT})/${DB}?parseTime=true&charset=utf8mb4&collation=utf8mb4_unicode_ci&multiStatements=true"
fi

exec "$GO" test -mod=mod ./internal/api/ -run 'Assistant|Confirmation' -count=1 -v
