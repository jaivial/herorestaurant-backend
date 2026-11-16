#!/usr/bin/env bash
# 40-switch.sh - backend newvillacarmen-backend-prod to PostgreSQL.
# POINT OF NO RETURN for data once traffic writes to PG.
# Same container spec as deploy/release-backend-prod.sh (network host,
# restart unless-stopped, volume newvillacarmen-prod_whatsapp-bot-context at
# /var/lib/herorestaurant), env captured VERBATIM from the live container plus
# the PG variables from $CUTOVER_DIR/pg-vars.env (0600, prepared before Sunday):
#   DB_DRIVER=postgres
#   DB_PG_PRODUCTION_APPROVED=1                       (pg_production_start_v1)
#   DB_PG_ACCEPT_IMPORT_STAGING=orphans=731,pending=329,fks_not_valid=4   (exact marker match)
#   DATABASE_URL=postgres://<user>:<pw>@127.0.0.1:55432/newvillacarmen_business?sslmode=disable
#   BOT_CONTEXT_DRIVER=postgres
#   BOT_CONTEXT_POSTGRES_DSN=<same as DATABASE_URL>
#   BOT_CONTEXT_POSTGRES_SCHEMA=bot_context
# DB_HOST/DB_PORT/DB_NAME/DB_USER/DB_PASSWORD stay (rollback). VC_PG_LOCAL_HTTP_QA*
# must NOT be present (the server refuses).
source "$(dirname "$0")/lib.sh"
guard "GO switch"
NAME=newvillacarmen-backend-prod
IMAGE=${PG_IMAGE_TAG:-newvillacarmen-prod-backend:pgnative-20261113}
VOLUME=newvillacarmen-prod_whatsapp-bot-context
OLDENV="$CUTOVER_DIR/backend.env.mysql"; NEW="$CUTOVER_DIR/backend.env.pg"; VARS="$CUTOVER_DIR/pg-vars.env"
need="DB_DRIVER DB_PG_PRODUCTION_APPROVED DB_PG_ACCEPT_IMPORT_STAGING DATABASE_URL BOT_CONTEXT_DRIVER BOT_CONTEXT_POSTGRES_DSN BOT_CONTEXT_POSTGRES_SCHEMA"
run bash -c "docker inspect $NAME | python3 -c 'import json,sys; print(chr(10).join(json.load(sys.stdin)[0][\"Config\"][\"Env\"]))' | grep -vE '^(PATH|HOME|HOSTNAME)=' > '$OLDENV' && chmod 600 '$OLDENV'"
run bash -c "{ grep -vE '^(DB_DRIVER|DB_PG_[A-Z_]+|DATABASE_URL|BOT_CONTEXT_DRIVER|BOT_CONTEXT_POSTGRES_[A-Z_]+|VC_PG_LOCAL_HTTP_QA[A-Z_]*)=' '$OLDENV'; cat '$VARS'; } > '$NEW' && chmod 600 '$NEW'"
if [[ -r "$NEW" && -r "$OLDENV" ]]; then
	say "NAME diff (values never shown):"; diff <(env_names "$OLDENV") <(env_names "$NEW") || true
	for k in $need; do grep -q "^$k=" "$NEW" && pass "has $k" || failc "missing $k"; done
	grep -qE '^VC_PG_LOCAL_HTTP_QA' "$NEW" && failc "VC_PG_LOCAL_HTTP_QA* present"
	[[ ${FAILS:-0} == 0 ]] || die "env file incomplete"
fi
run docker image inspect "$IMAGE" -f '{{.Id}}'
run docker stop -t 30 "$NAME"
run docker rename "$NAME" "$NAME-mysql-precutover"
START_AT=$(date -Iseconds)   # PG may take writes from here on: this is SWITCH_AT
run docker run -d --name "$NAME" --network host --restart unless-stopped --env-file "$NEW" -v "$VOLUME:/var/lib/herorestaurant" "$IMAGE"
# healthz must answer 200 within 90 s; "Connection refused" while the server
# boots is expected and silenced. No 200 -> STOP (no SWITCH_AT) -> 90-rollback.
run bash -c "for i in \$(seq 45); do [ \"\$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/healthz)\" = 200 ] && echo 'healthz 200' && exit 0; sleep 2; done; docker logs --tail 30 $NAME 2>&1 | sed -E 's#(postgres(ql)?://)[^ ]*#\1***#g'; exit 1" || die "healthz not 200 within 90 s: do NOT continue; 90-rollback.sh with SWITCH_AT=$START_AT (container may have taken traffic)"
say "SWITCH_AT=$START_AT (record it; 50-smoke and 90-rollback need it)"
say "host timers (ops-check, cdn-sweep) read /etc/villacarmen/backend.env: switch them to PG (DB_DRIVER=postgres, DB_PG_TOOLS_QA_APPROVED=1, DATABASE_URL) or keep them stopped; decide in the runbook"
