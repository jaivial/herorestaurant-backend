#!/usr/bin/env bash
# 30-import.sh - PRODUCTION import (Sunday). Same pipeline as
# rehearsal-desktop.sh, with the production facts:
#   source  = host service mysql.service 8.0.46, socket /var/run/mysqld/mysqld.sock,
#             DB newvillacarmen (dbmigrate requires a unix-socket MYSQL_DSN)
#   target  = NEW container $PG_C + NEW volume $PG_V, published on 127.0.0.1:$PG_PORT
#             (backend runs with network host), socket dir $CUTOVER_DIR/pgsock
# HARD STOP on any difference (exit != 0 of any check).
source "$(dirname "$0")/lib.sh"
guard "GO import"
: "${MYSQL_DSN:?unix-socket DSN, e.g. villacarmen:<pw>@unix(/var/run/mysqld/mysqld.sock)/newvillacarmen?information_schema_stats_expiry=0 (REQUIRED param: else MySQL 8 stats cache gives NULL AUTO_INCREMENT; from a 0600 file, never argv history)}"
DB=${MYSQL_DB:-newvillacarmen}
PG_C=${PG_CONTAINER:-newvillacarmen-pg-prod}; PG_V=${PG_VOLUME:-newvillacarmen-pg-prod-data}; PG_PORT=${PG_PORT:-55432}
PGDB=${PG_DB:-newvillacarmen_business}
PGDSN="host=/var/run/postgresql user=postgres dbname=$PGDB sslmode=disable"   # inside the PG container
W="$CUTOVER_DIR/src"      # checkout of the approved commit (overrides, allowlist template, sqlcheck tree)
D="$BIN_DIR/dbmigrate-pgnative"
R="$CUTOVER_DIR/import-$TS"
stop_if() { [[ $EXECUTE == 1 ]] || { say "+ (check) $*"; return 0; }; "$@" || die "check failed -> do NOT switch; 90-rollback.sh with SWITCH_AT=none"; }
sh_pg() { docker exec -i -u postgres -e "PG_DSN=$PGDSN" "$PG_C" sh -c "$1"; }
export -f sh_pg; export PG_C PGDSN
run install -d -m 700 "$R"

say "== 1 schema + export (host binary, MySQL unix socket, consistent snapshot, read-only)"
run "$D" -mode schema -db "$DB" -out "$R/schema.json"
run bash -c "'$D' -mode export -db '$DB' -dir '$R/export' > '$R/export.summary.json'"
say "== 2 independent reference (MySQL CLI: HEX(CAST(col AS BINARY)); generator as in rehearsal-desktop.sh gensql)"
run bash -c "python3 '$W/scripts/cutover/gensql.py' '$R/schema.json' independent > '$R/independent.sql' && ${MYSQL_CLI:?} < '$R/independent.sql' > '$R/independent.ndjson' && chmod 600 '$R/independent.ndjson'"
stop_if "$D" -mode crosscheck -dir "$R/export" -independent "$R/independent.ndjson"
say "== 3 plan (native) + pgcompat"
stop_if "$D" -mode plan -schema "$R/schema.json" -out "$R/plan" -view-override "$W/docs/dbmigrate/view-overrides.json" -storage-override "$W/docs/dbmigrate/storage-overrides.json"
stop_if bash -c "! grep -q 'ci_ai\|__wk\|__pad\|ixpad\|ixwk' '$R/plan/plan.sql'"
stop_if "$D" -mode pgcompat -dir "$R/export" -plan "$R/plan"
say "== 4 orphan allowlist bound to this export (counts are enforced by import: exactly 1/726/3/1)"
run bash -c "python3 -c 'import json,sys; a=json.load(open(sys.argv[1])); a[\"schema_sha256\"]=json.load(open(sys.argv[2]))[\"schema_sha256\"]; print(json.dumps(a,indent=1))' '$W/docs/dbmigrate/staging-orphan-allowlist.json' '$R/export/manifest.json' > '$R/orphan-allowlist.json'"
say "== 5 NEW PG (container + volume never reused)"
run install -d -m 700 "$CUTOVER_DIR/pgsock"
run docker volume create "$PG_V"
run docker run -d --name "$PG_C" --restart unless-stopped --memory 6g --cpus 2 -p 127.0.0.1:$PG_PORT:5432 -v "$PG_V":/var/lib/postgresql/data -v "$CUTOVER_DIR/pgsock":/var/run/postgresql --env-file "$CUTOVER_DIR/pg.env" "${PG_IMAGE:-postgres:17-alpine}"
run bash -c "for i in \$(seq 60); do docker exec $PG_C pg_isready -q -h /var/run/postgresql && exit 0; sleep 2; done; exit 1"
run docker exec -u postgres "$PG_C" createdb "$PGDB"
run docker exec "$PG_C" mkdir -p /tmp/imp
for f in dbmigrate-pgnative pgnative-gate pgnative-sqlcheck; do run docker cp "$BIN_DIR/$f" "$PG_C:/tmp/imp/$f"; done
for x in export plan independent.ndjson orphan-allowlist.json; do run docker cp "$R/$x" "$PG_C:/tmp/imp/$x"; done
run bash -c "tar -C '$W' -cf - internal cmd docs/pgnative | docker exec -i $PG_C sh -c 'mkdir -p /tmp/imp/src && tar -C /tmp/imp/src -xf -'"
run docker exec "$PG_C" chown -R postgres /tmp/imp
say "== 6 import / verify / round trip / gate / sqlcheck"
stop_if bash -c "sh_pg '/tmp/imp/dbmigrate-pgnative -mode import -dir /tmp/imp/export -plan /tmp/imp/plan -independent /tmp/imp/independent.ndjson -orphan-allowlist /tmp/imp/orphan-allowlist.json' > '$R/import.json'"
stop_if bash -c "sh_pg '/tmp/imp/dbmigrate-pgnative -mode verify -dir /tmp/imp/export -plan /tmp/imp/plan' > '$R/verify.json'"
stop_if bash -c "sh_pg '/tmp/imp/dbmigrate-pgnative -mode export-pg -plan /tmp/imp/plan -out /tmp/imp/pg-export && /tmp/imp/dbmigrate-pgnative -mode compare -a /tmp/imp/export/manifest.json -b /tmp/imp/pg-export/manifest.json' > '$R/roundtrip.json'"
stop_if bash -c "sh_pg '/tmp/imp/pgnative-gate -schema /tmp/imp/plan/schema.json' > '$R/gate.json'"
run bash -c "sh_pg 'cd /tmp/imp/src && /tmp/imp/pgnative-sqlcheck -schema /tmp/imp/plan/schema.json -prepare -out /tmp/imp/sqlcheck.tsv' 2> '$R/sqlcheck-summary.json'; docker cp $PG_C:/tmp/imp/sqlcheck.tsv '$R/sqlcheck.tsv'"
stop_if bash -c "awk -F'\t' '\$3==\"error\"{print \$1}' '$R/sqlcheck.tsv' | sed -E 's/:[0-9]+$//' | sort > '$R/sqlcheck-errors.txt' && diff '$CUTOVER_DIR/sqlcheck-errors-expected.txt' '$R/sqlcheck-errors.txt'"
say "== 7 exact row counts: freeze (MySQL) vs PG"
run bash -c "python3 '$W/scripts/cutover/gensql.py' '$R/schema.json' count-pg | docker exec -i -u postgres $PG_C psql -d $PGDB -At | sort > '$R/counts-pg.tsv'"
stop_if bash -c "diff <(sort '$CUTOVER_DIR/freeze-rowcounts.tsv') '$R/counts-pg.tsv'"
say "== 8 bot context store: SQLite (volume newvillacarmen-prod_whatsapp-bot-context at /var/lib/herorestaurant) -> schema bot_context"
# copy the SQLite file out of the (stopped) backend volume, read-only mount
run bash -c "docker run --rm --network none -v newvillacarmen-prod_whatsapp-bot-context:/src:ro -v '$R':/dst alpine cp /src/whatsapp-bot-context.sqlite /dst/bot-context.sqlite && chmod 600 '$R/bot-context.sqlite'"
BOTDSN="host=$CUTOVER_DIR/pgsock user=postgres dbname=$PGDB sslmode=disable"
run env BOT_CONTEXT_POSTGRES_DSN="$BOTDSN" BOT_CONTEXT_POSTGRES_SCHEMA=bot_context "$BIN_DIR/botstore-migrate" -mode inspect -sqlite "$R/bot-context.sqlite"
stop_if env BOT_CONTEXT_POSTGRES_DSN="$BOTDSN" BOT_CONTEXT_POSTGRES_SCHEMA=bot_context "$BIN_DIR/botstore-migrate" -mode copy -sqlite "$R/bot-context.sqlite"
stop_if env BOT_CONTEXT_POSTGRES_DSN="$BOTDSN" BOT_CONTEXT_POSTGRES_SCHEMA=bot_context "$BIN_DIR/botstore-migrate" -mode verify -sqlite "$R/bot-context.sqlite"
say "import OK (IMPORT_OK_AT=$(date -Iseconds)); results in $R"
