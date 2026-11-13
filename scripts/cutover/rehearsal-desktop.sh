#!/usr/bin/env bash
# rehearsal-desktop.sh - full NATIVE import rehearsal on the desktop
# (coordination id: pg_cutover_rehearsal_v1). DRY-RUN by default; executes
# only with --execute and CUTOVER_GO="GO rehearsal". Refuses on kraken.
# Source: MySQL container $SRC (copy of prod, 11 Nov). Target: a NEW empty
# PG 17 container $PGC (network none, 6g, 2 cpus, trust auth). Nothing else
# is touched. No secrets: MySQL is reached as root over its unix socket
# inside the container; PG over its socket with trust auth.
export CUTOVER_HOST=$(hostname -s)        # lib guard: this host
export CUTOVER_DIR=${REH_DIR:-/tmp/vc-rehearsal}
source "$(dirname "$0")/lib.sh"
[[ "$(hostname -s)" != kraken ]] || die "rehearsal must not run on kraken"
guard "GO rehearsal"

SRC=${SRC:-instinct-villacarmen-mysql-source-20261111}
DB=${SRC_DB:-villacarmen_source}
PGC=${PGC:-instinct-villacarmen-padrehearsal-20261113}
PGDB=${PGDB:-newvillacarmen_business}
BIN=${BIN:-/tmp/villacarmen-bot-20261111}
W=${WORKTREE:-/home/jaime/worktrees/villacarmen-bot-pgnative-20261113}
R="$CUTOVER_DIR/run-$TS"
MYDSN='root@unix(/var/run/mysqld/mysqld.sock)/'"$DB"'?information_schema_stats_expiry=0'
PGDSN="host=/var/run/postgresql user=postgres dbname=$PGDB sslmode=disable"
T0=$SECONDS; step() { say "== $1 (t+$((SECONDS - T0))s)"; }
sh_src() { docker exec -i "$SRC" sh -c "$1"; }
sh_pg() { docker exec -i -u postgres -e "PG_DSN=$PGDSN" "$PGC" sh -c "$1"; }
myq() { docker exec -i "$SRC" mysql -uroot -N -B --raw "$DB"; } # SQL on stdin
export -f sh_src sh_pg myq
export SRC DB PGC PGDB PGDSN

step "0 preflight"
run install -d -m 700 "$R"
run docker inspect -f '{{.State.Status}}' "$SRC"
run bash -c "! docker inspect $PGC >/dev/null 2>&1 || { echo 'target container exists: refusing (must be NEW)'; exit 1; }"
run bash -c "cd '$BIN' && sha256sum dbmigrate-pgnative pgnative-gate pgnative-sqlcheck | tee '$R/bin-sha256.txt'"

step "1 schema + export (binary runs INSIDE the MySQL container, unix socket)"
run docker cp "$BIN/dbmigrate-pgnative" "$SRC:/tmp/dbmigrate-pgnative"
run sh_src "rm -rf /tmp/rh-export && MYSQL_DSN='$MYDSN' /tmp/dbmigrate-pgnative -mode schema -db $DB -out /tmp/rh-schema.json"
run sh_src "MYSQL_DSN='$MYDSN' /tmp/dbmigrate-pgnative -mode export -db $DB -dir /tmp/rh-export > /tmp/rh-export.summary.json"
run docker cp "$SRC:/tmp/rh-export" "$R/export"
run docker cp "$SRC:/tmp/rh-schema.json" "$R/schema.json"

step "2 independent text-bytes reference (MySQL: HEX(CAST(col AS BINARY)) per column, schema order; row order irrelevant)"
run bash -c "python3 '$W/scripts/cutover/gensql.py' '$R/schema.json' independent > '$R/independent.sql'"
run bash -c "myq < '$R/independent.sql' > '$R/independent.ndjson'; chmod 600 '$R/independent.ndjson'; wc -l < '$R/independent.ndjson'"

step "3 crosscheck (files only)"
run "$BIN/dbmigrate-pgnative" -mode crosscheck -dir "$R/export" -independent "$R/independent.ndjson"

step "4 plan (native) + pgcompat"
run "$BIN/dbmigrate-pgnative" -mode plan -schema "$R/schema.json" -out "$R/plan" -view-override "$W/docs/dbmigrate/view-overrides.json" -storage-override "$W/docs/dbmigrate/storage-overrides.json"
run bash -c "! grep -q 'ci_ai\|__wk\|__pad\|ixpad\|ixwk' '$R/plan/plan.sql' && echo 'plan is native (no old-approach objects)'"
run "$BIN/dbmigrate-pgnative" -mode pgcompat -dir "$R/export" -plan "$R/plan"

step "5 orphan allowlist bound to THIS export schema"
run bash -c "python3 - '$W/docs/dbmigrate/staging-orphan-allowlist.json' '$R/export/manifest.json' > '$R/orphan-allowlist.json' <<'PY'
import json,sys
a=json.load(open(sys.argv[1])); m=json.load(open(sys.argv[2]))
a['schema_sha256']=m['schema_sha256']; a['purpose']='REHEARSAL-ONLY: '+a['purpose']
print(json.dumps(a,indent=1))
PY"

step "6 NEW PG 17 container (network none, 6g, 2 cpus, trust)"
run docker run -d --name "$PGC" --network none --memory 6g --cpus 2 -e POSTGRES_HOST_AUTH_METHOD=trust "${PG_IMAGE:-postgres:17-alpine}"
run bash -c "for i in \$(seq 60); do docker exec $PGC pg_isready -q -h /var/run/postgresql && break; sleep 2; done"
run docker exec -u postgres "$PGC" createdb "$PGDB"
run docker exec "$PGC" mkdir -p /tmp/rh
for f in dbmigrate-pgnative pgnative-gate pgnative-sqlcheck; do run docker cp "$BIN/$f" "$PGC:/tmp/rh/$f"; done
run docker cp "$R/export" "$PGC:/tmp/rh/export"
run docker cp "$R/plan" "$PGC:/tmp/rh/plan"
run docker cp "$R/independent.ndjson" "$PGC:/tmp/rh/independent.ndjson"
run docker cp "$R/orphan-allowlist.json" "$PGC:/tmp/rh/orphan-allowlist.json"
run docker exec "$PGC" chown -R postgres /tmp/rh

step "7 import (one transaction; refuses a non-empty DB)"
run bash -c "sh_pg '/tmp/rh/dbmigrate-pgnative -mode import -dir /tmp/rh/export -plan /tmp/rh/plan -independent /tmp/rh/independent.ndjson -orphan-allowlist /tmp/rh/orphan-allowlist.json' > '$R/import.json'"
step "8 verify + reverse export round trip"
run bash -c "sh_pg '/tmp/rh/dbmigrate-pgnative -mode verify -dir /tmp/rh/export -plan /tmp/rh/plan' > '$R/verify.json'"
run bash -c "sh_pg '/tmp/rh/dbmigrate-pgnative -mode export-pg -plan /tmp/rh/plan -out /tmp/rh/pg-export && /tmp/rh/dbmigrate-pgnative -mode compare -a /tmp/rh/export/manifest.json -b /tmp/rh/pg-export/manifest.json' > '$R/roundtrip.json'"
run bash -c "sh_pg '/tmp/rh/dbmigrate-pgnative -mode import -dir /tmp/rh/export -plan /tmp/rh/plan -independent /tmp/rh/independent.ndjson -orphan-allowlist /tmp/rh/orphan-allowlist.json' >/dev/null 2>&1 && { echo 'STOP: second import was NOT refused'; exit 1; } || echo 'second import refused (expected)'"

step "9 pgnative-gate"
run bash -c "sh_pg '/tmp/rh/pgnative-gate -schema /tmp/rh/plan/schema.json' > '$R/gate.json'"

step "10 pgnative-sqlcheck (needs the source tree: internal/ cmd/ docs/pgnative inside the container)"
run bash -c "tar -C '$W' -cf - internal cmd docs/pgnative | docker exec -i $PGC sh -c 'mkdir -p /tmp/rh/src && tar -C /tmp/rh/src -xf - && chown -R postgres /tmp/rh/src'"
run bash -c "sh_pg 'cd /tmp/rh/src && /tmp/rh/pgnative-sqlcheck -schema /tmp/rh/plan/schema.json -prepare -out /tmp/rh/sqlcheck.tsv' 2> '$R/sqlcheck-summary.json'; docker cp $PGC:/tmp/rh/sqlcheck.tsv '$R/sqlcheck.tsv'"

step "11 exact row counts MySQL vs PG (all tables)"
run bash -c "python3 '$W/scripts/cutover/gensql.py' '$R/schema.json' count-mysql > '$R/count-mysql.sql'; python3 '$W/scripts/cutover/gensql.py' '$R/schema.json' count-pg > '$R/count-pg.sql'"
run bash -c "myq < '$R/count-mysql.sql' | sort > '$R/counts-mysql.tsv'"
run bash -c "awk -F'\t' '\$3==\"error\"{print \$1}' '$R/sqlcheck.tsv' | sed -E 's/:[0-9]+\$//' | sort > '$R/sqlcheck-errors.txt'; wc -l < '$R/sqlcheck-errors.txt'   # reviewed list becomes sqlcheck-errors-expected.txt for Sunday"
run bash -c "docker exec -i -u postgres $PGC psql -d $PGDB -At < '$R/count-pg.sql' | sort > '$R/counts-pg.tsv'"
run bash -c "diff '$R/counts-mysql.tsv' '$R/counts-pg.tsv' && echo 'row counts identical'"

step "done: results in $R (import.json verify.json roundtrip.json gate.json sqlcheck.tsv counts-*.tsv); PG container $PGC kept"
