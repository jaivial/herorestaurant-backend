#!/usr/bin/env bash
# 50-smoke.sh - smoke on the PG backend: read-only HTTP (healthz, menus, BO,
# POS) + ONE real WhatsApp message that must land in PG, + clean logs.
# Exit != 0 on any failure -> NO-GO -> 90-rollback.sh. GO -> 51-unfreeze.sh.
# Inputs: SWITCH_AT (printed by 40-switch), $CUTOVER_DIR/qa-cookie (QA session
# cookie) and $CUTOVER_DIR/vault-hdr (admin header, "Name: value"), both 0600
# and never printed. BOT_WAIT_S (default 600) = time to wait for the message.
source "$(dirname "$0")/lib.sh"
guard "GO smoke"
: "${SWITCH_AT:?ISO time printed by 40-switch.sh}"
U=http://127.0.0.1:${BACKEND_PORT:-8080}
C="$CUTOVER_DIR/qa-cookie"; H="$CUTOVER_DIR/vault-hdr"
BE=newvillacarmen-backend-prod; PG_C=${PG_CONTAINER:-newvillacarmen-pg-prod}; PGDB=${PG_DB:-newvillacarmen_business}

# http_ok NAME CURL_ARGS...: read-only request, PASS only on 200.
http_ok() {
	local name=$1 code; shift
	say "+ (check) curl $name $*"
	[[ $EXECUTE == 1 ]] || return 0
	code=$(curl -s -o /dev/null -w '%{http_code}' "$@" || true)
	[[ "$code" == 200 ]] && pass "$name $code" || failc "$name $code"
}
pg_q() { docker exec -u postgres "$PG_C" psql -d "$PGDB" -At -v ON_ERROR_STOP=1 -c "$1"; }

http_ok healthz "$U/healthz"
http_ok "public menus" "$U/api/menus/public"
http_ok "admin bookings" -H @"$H" -b "$C" "$U/api/admin/bookings?limit=1"
# POS read (creating a visit opens a real ticket number on the cash day: not harmless)
http_ok "admin pos visits" -H @"$H" -b "$C" "$U/api/admin/pos/visits"

say "bot: Jaime sends ONE WhatsApp message from +34692747052 (only this number); check the reply and that the log shows no other recipient"
# The 2026-11-15 rollback: the webhook INSERT ... ON CONFLICT on bot_inbound_queue
# failed with 42P10. The message must be stored in PG after SWITCH_AT.
say "+ (check) waiting up to ${BOT_WAIT_S:-600}s: SELECT count(*) FROM bot_inbound_queue WHERE created_at >= '$SWITCH_AT' > 0"
if [[ $EXECUTE == 1 ]]; then
	n=0
	for _ in $(seq $(( ${BOT_WAIT_S:-600} / 10 ))); do
		n=$(pg_q "SELECT count(*) FROM bot_inbound_queue WHERE created_at >= '$SWITCH_AT'::timestamptz" 2>&1) || n="ERR"
		[[ "$n" =~ ^[1-9][0-9]*$ ]] && break
		sleep 10
	done
	[[ "$n" =~ ^[1-9][0-9]*$ ]] && pass "bot_inbound_queue rows since SWITCH_AT=$n" || failc "bot_inbound_queue rows since SWITCH_AT=${n:-0}: the webhook did not reach PG"
	say "INFO bot_inbound_queue states since SWITCH_AT: $(pg_q "SELECT string_agg(state||'='||n, ' ') FROM (SELECT state, count(*) n FROM bot_inbound_queue WHERE created_at >= '$SWITCH_AT'::timestamptz GROUP BY 1) s" 2>&1 || true)"
fi

# logs since SWITCH_AT: backend errors and PG server errors (PG logs ERROR:, not SQLSTATE)
for spec in "$BE|SQLSTATE|42P10|23505|panic|\" 500 \"" "$PG_C|SQLSTATE|42P10|23505|panic|PANIC|ERROR:|FATAL:"; do
	c=${spec%%|*}; pat=${spec#*|}
	say "+ (check) docker logs --since $SWITCH_AT $c | grep -E '$pat'"
	[[ $EXECUTE == 1 ]] || continue
	docker inspect "$c" >/dev/null 2>&1 || { failc "container $c missing"; continue; }
	n=$(docker logs --since "$SWITCH_AT" "$c" 2>&1 | grep -Ec "$pat" || true)
	if [[ "$n" == 0 ]]; then pass "logs $c clean"; else
		failc "logs $c: $n lines match"
		docker logs --since "$SWITCH_AT" "$c" 2>&1 | grep -E "$pat" | tail -5 | cut -c1-300
	fi
done

say "smoke done FAILS=${FAILS:-0}"
[[ ${FAILS:-0} == 0 ]] || die "NO-GO: 90-rollback.sh with SWITCH_AT=$SWITCH_AT"
say "DECISION (Main/Jaime): GO -> 51-unfreeze.sh; NO-GO -> 90-rollback.sh"
