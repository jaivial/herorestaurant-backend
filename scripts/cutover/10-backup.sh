#!/usr/bin/env bash
# 10-backup.sh - consistent MySQL dump + restore check + images/env backup.
# Only writes the backup dir and a THROWAWAY scratch container (removed).
source "$(dirname "$0")/lib.sh"
guard "GO backup"
: "${MYSQL_DUMP:=mysqldump --defaults-extra-file=$CUTOVER_DIR/mysql-ro.cnf}"   # host mysqldump (mysql.service 8.0.46)
: "${MYSQL_DB:=newvillacarmen}"
# kraken: BACKUP_ROOT=/var/www/newvillacarmen/backups/cutover-pg (parent created 0700 too)
BROOT="${BACKUP_ROOT:-/var/www/newvillacarmen/backups/cutover-pg}"
B="$BROOT/cutover-pg-$TS"
run install -d -m 700 "$BROOT"
run install -d -m 700 "$B"
# Views broken in production MySQL (error 1356: missing tables/columns) are
# excluded by name; any OTHER dump error still stops the backup (no --force).
IGN=""
for v in ${BACKUP_IGNORE_VIEWS-recent_conversations}; do IGN+=" --ignore-table=$MYSQL_DB.$v"; say "NOTE dump excludes broken view $MYSQL_DB.$v (BACKUP_IGNORE_VIEWS)"; done
# 1. dump (InnoDB consistent snapshot; blobs hex; utf8mb4; no PROCESS privilege -> --no-tablespaces)
run bash -c "set -o pipefail; $MYSQL_DUMP --single-transaction --quick --routines --triggers --events --hex-blob --default-character-set=utf8mb4 --set-gtid-purged=OFF --no-tablespaces$IGN $MYSQL_DB | gzip -1 > '$B/mysql-$MYSQL_DB.sql.gz'"
run bash -c "cd '$B' && sha256sum mysql-$MYSQL_DB.sql.gz > mysql-$MYSQL_DB.sql.gz.sha256"
# 2. restore check in a throwaway container without network, then removed
SCR=cutover-restorecheck-$TS
run docker run -d --name "$SCR" --network none -e MYSQL_ALLOW_EMPTY_PASSWORD=1 -e MYSQL_DATABASE="$MYSQL_DB" "${MYSQL_IMAGE:-mysql:8.0.46}"
# wait until the entrypoint has CREATED the database (ping answers earlier -> ERROR 1049)
run bash -c "for i in \$(seq 90); do docker exec $SCR mysql -e 'USE $MYSQL_DB' >/dev/null 2>&1 && exit 0; sleep 2; done; echo 'STOP: restore container not ready'; exit 1"
run bash -c "set -o pipefail; gunzip -c '$B/mysql-$MYSQL_DB.sql.gz' | docker exec -i $SCR mysql $MYSQL_DB"
run bash -c "docker exec $SCR mysql -N -B $MYSQL_DB -e \"SELECT TABLE_NAME FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_TYPE='BASE TABLE' ORDER BY 1\" | while read t; do printf '%s\t%s\n' \"\$t\" \"\$(docker exec $SCR mysql -N -B $MYSQL_DB -e \"SELECT COUNT(*) FROM \\\`\$t\\\`\")\"; done > '$B/restore-rowcounts.tsv'"
say "compare: diff $CUTOVER_DIR/freeze-rowcounts.tsv $B/restore-rowcounts.tsv must be empty (dump taken after 20-freeze)"
run docker rm -f "$SCR"
# 3. images, container config, env files (names only in the log), bot store
for c in $CONTAINERS_STOP_ORDER; do
	run bash -c "docker inspect '$c' > '$B/$c.inspect.json' && chmod 600 '$B/$c.inspect.json'"
	run bash -c "docker inspect -f '{{.Config.Image}} {{.Image}}' '$c' > '$B/$c.image'"
done
run bash -c "docker image save newvillacarmen-prod-backend:privileged | gzip -1 > '$B/backend-image.tar.gz'"
for f in ${ENV_FILES:-}; do
	run install -m 600 "$f" "$B/$(basename "$f").bak"
	[[ -r "$f" ]] && say "env file $f variable names: $(env_names "$f" | tr '\n' ' ')"
done
# bot context SQLite: volume newvillacarmen-prod_whatsapp-bot-context (/var/lib/herorestaurant)
run bash -c "docker run --rm --network none -v newvillacarmen-prod_whatsapp-bot-context:/src:ro -v '$B':/dst alpine tar -C /src -czf /dst/bot-context-volume.tar.gz ."
run bash -c "cd '$B' && sha256sum *.gz > SHA256SUMS && du -sh '$B' && ls -l '$B'"
run ln -sfn "$B" "$CUTOVER_DIR/backup-latest"
say "backup dir $B (0700)"
