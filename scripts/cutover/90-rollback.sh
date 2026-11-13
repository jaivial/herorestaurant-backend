#!/usr/bin/env bash
# 90-rollback.sh - back to MySQL. Rows written on PG after SWITCH_AT are
# exported to CSV for a later, decided replay (never merged automatically).
source "$(dirname "$0")/lib.sh"
guard "GO rollback"
: "${SWITCH_AT:?ISO time from 40-switch, or 'none' if the backend never ran on PG}"
PG_C=${PG_CONTAINER:-newvillacarmen-pg-prod}
run docker stop -t 30 newvillacarmen-backend-prod
if [[ "$SWITCH_AT" != none ]]; then
	run bash -c "docker exec $PG_C psql -U postgres -d newvillacarmen_business -At -c \"SELECT table_name FROM information_schema.columns WHERE table_schema='public' AND column_name IN ('created_at','updated_at') GROUP BY table_name ORDER BY 1\" > '$CUTOVER_DIR/rollback-tables-$TS.txt'"
	run bash -c "while read t; do docker exec $PG_C psql -U postgres -d newvillacarmen_business -c \"COPY (SELECT * FROM \\\"\$t\\\" WHERE GREATEST(COALESCE(created_at::timestamptz,'-infinity'),COALESCE(updated_at::timestamptz,'-infinity')) >= '$SWITCH_AT') TO STDOUT WITH CSV HEADER\" > '$CUTOVER_DIR/rollback-delta-$TS-'\$t.csv; done < '$CUTOVER_DIR/rollback-tables-$TS.txt'; chmod 600 '$CUTOVER_DIR'/rollback-delta-$TS-*"
	say "NOTE: deletes and tables without timestamps are NOT captured; the replay needs a decision"
fi
# 40-switch kept the MySQL container renamed: put it back (no image/env change needed)
run bash -c "docker inspect newvillacarmen-backend-prod-mysql-precutover >/dev/null 2>&1 || { echo 'STOP: newvillacarmen-backend-prod-mysql-precutover missing; recreate from backup-latest inspect/env'; exit 1; }"
run bash -c "docker rename newvillacarmen-backend-prod newvillacarmen-backend-prod-pg-failed 2>/dev/null || true"
run bash -c "${MYSQL_ADMIN_CLI:?admin client used only for read_only} -e 'SET GLOBAL super_read_only=OFF; SET GLOBAL read_only=OFF'"
run docker rename newvillacarmen-backend-prod-mysql-precutover newvillacarmen-backend-prod
run docker start newvillacarmen-backend-prod
run bash -c "for i in \$(seq 45); do curl -fsS http://127.0.0.1:8080/healthz >/dev/null && echo healthy && exit 0; sleep 2; done; exit 1"
for c in $CONTAINERS_START_ORDER; do run docker start "$c"; done
run bash -c "${MAINT_OFF_CMD:-echo 'MAINT_OFF_CMD not set'}"
say "rollback done; the PG container/volume are KEPT (never deleted here)"
