#!/usr/bin/env bash
# 90-rollback.sh - back to MySQL. Rows written on PG after SWITCH_AT are
# exported to CSV for a later, decided replay (never merged automatically).
source "$(dirname "$0")/lib.sh"
guard "GO rollback"
: "${SWITCH_AT:?ISO time from 40-switch, or 'none' if the backend never ran on PG}"
PG_C=${PG_CONTAINER:-newvillacarmen-pg-prod}
run docker stop -t 30 newvillacarmen-backend-prod
if [[ "$SWITCH_AT" != none ]]; then
	# Per table, only the timestamp columns that really exist (2026-11-15: a blanket
	# GREATEST(created_at, updated_at) failed with "column does not exist" on dozens
	# of tables and their CSVs stayed empty). Read-only on PG; a failed table is
	# reported and the rollback goes on.
	TBL="$CUTOVER_DIR/rollback-tables-$TS.txt"
	pg_q() { docker exec "$PG_C" psql -U postgres -d newvillacarmen_business -At -v ON_ERROR_STOP=1 -c "$1"; }
	DELTA_SQL="SELECT c.table_name || '|GREATEST(' || string_agg(format('COALESCE(%I::timestamptz, %L)', c.column_name, '-infinity'), ', ' ORDER BY c.column_name) || ')' FROM information_schema.columns c JOIN information_schema.tables t ON t.table_schema = c.table_schema AND t.table_name = c.table_name AND t.table_type = 'BASE TABLE' WHERE c.table_schema = 'public' AND c.column_name IN ('created_at', 'updated_at') GROUP BY c.table_name ORDER BY 1"
	say "+ (read-only) $DELTA_SQL > $TBL"
	say "+ (read-only) per table: COPY (SELECT * FROM t WHERE <expr> >= '$SWITCH_AT') TO STDOUT WITH CSV HEADER > $CUTOVER_DIR/rollback-delta-$TS-<t>.csv"
	if [[ $EXECUTE == 1 ]]; then
		bad=0; withrows=0
		pg_q "$DELTA_SQL" > "$TBL" || { bad=1; say "FAIL delta table list"; : > "$TBL"; }
		while IFS='|' read -r t expr; do
			f="$CUTOVER_DIR/rollback-delta-$TS-$t.csv"
			if pg_q "COPY (SELECT * FROM \"$t\" WHERE $expr >= '$SWITCH_AT'::timestamptz) TO STDOUT WITH CSV HEADER" > "$f" 2> "$f.err"; then
				rm -f "$f.err"; [[ $(wc -l < "$f") -gt 1 ]] && withrows=$((withrows + 1))
			else bad=$((bad + 1)); say "FAIL delta export $t: $(head -c 300 "$f.err")"; fi
			chmod 600 "$f" "$f.err" 2>/dev/null || true
		done < "$TBL"
		say "delta export: $(wc -l < "$TBL") tables, $withrows with rows, $bad failed"
	fi
	say "NOTE: deletes and tables without timestamps are NOT captured; the replay needs a decision"
fi
# 40-switch kept the MySQL container renamed: put it back (no image/env change needed)
if [[ "$SWITCH_AT" == none ]] && ! docker inspect newvillacarmen-backend-prod-mysql-precutover >/dev/null 2>&1; then
	say "pre-switch rollback: backend never left MySQL, only read_only OFF + restart"
	run bash -c "${MYSQL_ADMIN_CLI:?admin client used only for read_only} -e 'SET GLOBAL super_read_only=OFF; SET GLOBAL read_only=OFF'"
else
	run bash -c "docker inspect newvillacarmen-backend-prod-mysql-precutover >/dev/null 2>&1 || { echo 'STOP: newvillacarmen-backend-prod-mysql-precutover missing; recreate from backup-latest inspect/env'; exit 1; }"
	run bash -c "docker rename newvillacarmen-backend-prod newvillacarmen-backend-prod-pg-failed 2>/dev/null || true"
	run bash -c "${MYSQL_ADMIN_CLI:?admin client used only for read_only} -e 'SET GLOBAL super_read_only=OFF; SET GLOBAL read_only=OFF'"
	run docker rename newvillacarmen-backend-prod-mysql-precutover newvillacarmen-backend-prod
fi
run docker start newvillacarmen-backend-prod
run bash -c "for i in \$(seq 45); do curl -fsS http://127.0.0.1:8080/healthz >/dev/null && echo healthy && exit 0; sleep 2; done; exit 1"
for c in $CONTAINERS_START_ORDER; do run docker start "$c"; done
run bash -c "${MAINT_OFF_CMD:-echo 'MAINT_OFF_CMD not set'}"
say "rollback done; the PG container/volume are KEPT (never deleted here)"
