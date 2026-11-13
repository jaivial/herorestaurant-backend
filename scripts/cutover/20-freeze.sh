#!/usr/bin/env bash
# 20-freeze.sh - stop every MySQL writer in a fixed order; WhatsApp inbound
# stays in the gateway (evo-weai-villacarmen keeps running and retries the
# webhook). VERIFY the gateway retry/queue behaviour before Sunday.
source "$(dirname "$0")/lib.sh"
guard "GO freeze"
run bash -c "${MAINT_ON_CMD:-echo 'MAINT_ON_CMD not set: maintenance page switch unknown (see runbook)'}"
# bot-pipeline belongs to compose project newvillacarmen-prod
# (/var/www/newvillacarmen/backend/deploy/docker-compose.prod.yml); docker stop
# is enough for the window (restart policies apply again on docker start).
for c in $CONTAINERS_STOP_ORDER; do run docker stop -t 30 "$c"; done
for t in villacarmen-ops-check.timer villacarmen-cdn-sweep.timer; do run systemctl stop "$t"; done   # host timers that write the DB
say "MySQL must now have no client writing:"
run bash -c "${MYSQL_CLI:=mysql --defaults-extra-file=$CUTOVER_DIR/mysql-ro.cnf} -N -B -e \"SELECT USER, SUBSTRING_INDEX(HOST,':',1), COMMAND, TIME FROM information_schema.PROCESSLIST WHERE ID<>CONNECTION_ID()\""
# read_only blocks any forgotten writer (reverted by 90-rollback)
run bash -c "${MYSQL_ADMIN_CLI:?admin client used only for read_only} -e 'SET GLOBAL super_read_only=ON; SET GLOBAL read_only=ON'"
run bash -c "${MYSQL_CLI:=mysql --defaults-extra-file=$CUTOVER_DIR/mysql-ro.cnf} -N -B -e \"SELECT TABLE_NAME FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_TYPE='BASE TABLE' ORDER BY 1\" ${MYSQL_DB:-newvillacarmen} | while read t; do printf '%s\t%s\n' \"\$t\" \"\$($MYSQL_CLI -N -B -e \"SELECT COUNT(*) FROM \\\`\$t\\\`\" ${MYSQL_DB:-newvillacarmen})\"; done > '$CUTOVER_DIR/freeze-rowcounts.tsv'"
say "FREEZE_AT=$(date -Iseconds) (write it in the runbook log)"
