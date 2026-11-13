#!/usr/bin/env bash
# 00-precheck.sh - STRICTLY READ-ONLY. PASS/FAIL per check, no secrets.
# Production facts: MySQL = host service mysql.service 8.0.46 on 127.0.0.1:3306,
# DB newvillacarmen, user villacarmen. Inputs (env, never printed):
# MYSQL_CLI (default: host mysql with a 0600 defaults file), MYSQL_DB,
# $CUTOVER_DIR/expected-sha256.txt ("<sha256>  <name>").
source "$(dirname "$0")/lib.sh"
EXECUTE=0 # this script never mutates
say "precheck (read-only)"
OUT="$CUTOVER_DIR/precheck-$TS"; mkdir -p "$OUT"; chmod 700 "$OUT"
[[ "$(hostname -s)" == "$CUTOVER_HOST" ]] && pass "host $CUTOVER_HOST" || failc "host is $(hostname -s), expected $CUTOVER_HOST"

# 1. disk (dump + export + PG data ~ 3x DB size; default minimum 40 GB)
for v in / /var/lib/docker /var/lib/mysql /var/www/newvillacarmen/backups "$CUTOVER_DIR"; do
	free=$(df -Pk "$v" 2>/dev/null | awk 'NR==2{print int($4/1024/1024)}')
	[[ -n "$free" && "$free" -ge ${MIN_FREE_GB:-10} ]] && pass "disk $v free=${free}G" || failc "disk $v free=${free:-?}G (< ${MIN_FREE_GB:-10}G)"
done

# 2. containers and host services (status only)
for c in $CONTAINERS_STOP_ORDER; do
	st=$(docker inspect -f '{{.State.Status}} {{if .State.Health}}{{.State.Health.Status}}{{end}} {{.Config.Image}}' "$c" 2>/dev/null || echo missing)
	[[ "$st" == running* ]] && pass "container $c: $st" || failc "container $c: $st"
done
for s in evo-weai-villacarmen botgenerator villacarmen-landing neo4j-rust; do
	st=$(systemctl is-active "$s" 2>/dev/null || systemctl --user is-active "$s" 2>/dev/null || true)
	say "INFO host service $s: ${st:-unknown} (not touched by the cutover)"
done

# 3. discovery: where is MySQL and how is the backend env set (names only)
say "INFO mysql-like containers:"; docker ps -a --format '{{.Names}}\t{{.Image}}\t{{.Status}}\t{{.Ports}}' | grep -Ei 'mysql|maria' || say "INFO none"
say "INFO listeners on 3306/3307/33060:"; ss -ltnp 2>/dev/null | grep -E ':(3306|3307|33060)[[:space:]]' || say "INFO none"
say "INFO mysqld on host:"; pgrep -a mysqld 2>/dev/null | sed -E 's/--password[^ ]*/--password=***/' || say "INFO none"
say "INFO backend env var NAMES (DB related):"; container_env_names newvillacarmen-backend-prod | grep -Ei 'DB|MYSQL|DATABASE|PG|DSN' || true
say "INFO backend networks:"; docker inspect -f '{{range $k,$v := .NetworkSettings.Networks}}{{$k}} {{end}}' newvillacarmen-backend-prod || true
say "INFO backend mounts (env files?):"; docker inspect -f '{{range .Mounts}}{{.Source}} -> {{.Destination}}{{"\n"}}{{end}}' newvillacarmen-backend-prod || true
say "INFO compose project:"; docker inspect -f '{{index .Config.Labels "com.docker.compose.project.working_dir"}} {{index .Config.Labels "com.docker.compose.project.config_files"}}' newvillacarmen-backend-prod 2>/dev/null || true

# 4. MySQL: version, row counts, orphans, NUL
: "${MYSQL_CLI:=mysql --defaults-extra-file=$CUTOVER_DIR/mysql-ro.cnf}"
systemctl is-active --quiet mysql && pass "mysql.service active" || failc "mysql.service not active"
if [[ -n "${MYSQL_CLI:-}" ]]; then
	q() { $MYSQL_CLI -N -B -e "$1" "${MYSQL_DB:-newvillacarmen}"; }
	if ver=$(q "SELECT VERSION()"); then pass "mysql reachable version=$ver"; else failc "mysql not reachable"; fi
	q "SELECT TABLE_NAME FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_TYPE='BASE TABLE' ORDER BY 1" > "$OUT/tables.txt"
	: > "$OUT/rowcounts.tsv"
	while read -r t; do printf '%s\t%s\n' "$t" "$(q "SELECT COUNT(*) FROM \`$t\`")" >> "$OUT/rowcounts.tsv"; done < "$OUT/tables.txt"
	pass "row counts: $(wc -l < "$OUT/rowcounts.tsv") tables -> $OUT/rowcounts.tsv"
	o1=$(q "SELECT COUNT(*) FROM bo_invoice_comments c LEFT JOIN invoices i ON i.id=c.invoice_id WHERE c.invoice_id IS NOT NULL AND i.id IS NULL")
	o2=$(q "SELECT COUNT(*) FROM invoice_line_items c LEFT JOIN invoices i ON i.id=c.invoice_id WHERE c.invoice_id IS NOT NULL AND i.id IS NULL")
	o3=$(q "SELECT COUNT(*) FROM site_builder_domain_mappings c LEFT JOIN site_builder_sites s ON s.id=c.site_id WHERE c.site_id IS NOT NULL AND s.id IS NULL")
	o4=$(q "SELECT COUNT(*) FROM site_builder_pages c LEFT JOIN site_builder_sites s ON s.id=c.site_id WHERE c.site_id IS NOT NULL AND s.id IS NULL")
	[[ "$o1/$o2/$o3/$o4" == "1/726/3/1" ]] && pass "orphans 1/726/3/1 = 731" || failc "orphans $o1/$o2/$o3/$o4 (expected 1/726/3/1): STOP for a decision, regenerate the allowlist"
	nul=$(q "SELECT COUNT(*) FROM assistant_messages WHERE LOCATE(BINARY UNHEX('00'), content) > 0")
	[[ "$nul" == "${EXPECTED_NUL:-1}" ]] && pass "NUL rows assistant_messages.content=$nul" || failc "NUL rows=$nul (expected ${EXPECTED_NUL:-1}): bytea storage override decision"
else
	failc "MYSQL_CLI not set: MySQL checks skipped"
fi

# 5. binaries (sha256 vs expected list)
for b in vc-server-pgnative-static dbmigrate-pgnative pgnative-gate pgnative-sqlcheck; do
	f="$BIN_DIR/$b"
	if [[ -x "$f" ]]; then
		h=$(sha256sum "$f" | cut -d' ' -f1); want=$(awk -v n="$b" '$2==n{print $1}' "$CUTOVER_DIR/expected-sha256.txt" 2>/dev/null || true)
		[[ -n "$want" && "$h" == "$want" ]] && pass "binary $b sha256 ok" || failc "binary $b sha256 $h != ${want:-<missing>}"
	else failc "binary $b missing in $BIN_DIR"; fi
done

# 6. new PG target must not exist yet (never reuse a container/volume)
PG_C=${PG_CONTAINER:-newvillacarmen-pg-prod}; PG_V=${PG_VOLUME:-newvillacarmen-pg-prod-data}
docker inspect "$PG_C" >/dev/null 2>&1 && failc "container $PG_C already exists" || pass "container name $PG_C free"
docker volume inspect "$PG_V" >/dev/null 2>&1 && failc "volume $PG_V already exists" || pass "volume $PG_V free"
docker image inspect "${PG_IMAGE:-postgres:17-alpine}" >/dev/null 2>&1 && pass "image ${PG_IMAGE:-postgres:17-alpine} present" || failc "image ${PG_IMAGE:-postgres:17-alpine} not pulled"
ss -ltn | grep -q "127.0.0.1:${PG_PORT:-55432}[[:space:]]" && failc "port ${PG_PORT:-55432} busy" || pass "port ${PG_PORT:-55432} free"

# 7. writers: every running container (review: each MySQL writer is stopped in 20-freeze)
say "INFO running containers:"; docker ps --format '{{.Names}}\t{{.Image}}\t{{.Status}}'
if [[ -n "${MYSQL_CLI:-}" ]]; then
	say "INFO MySQL client sessions by user/host (no queries shown):"
	q "SELECT USER, SUBSTRING_INDEX(HOST,':',1), COUNT(*) FROM information_schema.PROCESSLIST GROUP BY 1,2" || true
fi

say "precheck done FAILS=${FAILS:-0} out=$OUT"
[[ ${FAILS:-0} == 0 ]]
