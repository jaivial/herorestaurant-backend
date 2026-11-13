#!/usr/bin/env bash
# 40-switch.sh - backend newvillacarmen-backend-prod to PostgreSQL through a
# reviewed env diff. POINT OF NO RETURN for data once traffic writes to PG.
#
# BLOCKER (code, pgnative-J): cmd/server/main.go refuses DB_DRIVER=postgres
# unless VC_PG_LOCAL_HTTP_QA=1, and that QA mode forces loopback ADDR, no
# background workers and no providers -> NOT usable for production. A
# reviewed code change (production PG start path keeping the marker/native-key/
# trigger gates) and a rebuilt image are required BEFORE this step can run.
#
# Production facts: container runs with network host, restart unless-stopped,
# entrypoint /app/backend, port 8080; today it reads DB_HOST/DB_PORT/DB_NAME/
# DB_USER/DB_PASSWORD (MySQL). The PG switch needs these NAMES (values in a
# 0600 env file, never printed):
#   DB_DRIVER=postgres
#   DATABASE_URL=postgres://<user>:<pw>@127.0.0.1:55432/newvillacarmen_business?sslmode=disable
#   DB_PG_TOOLS_QA_APPROVED=1                (current business-pool gate)
#   BOT_CONTEXT_DRIVER=postgres
#   BOT_CONTEXT_POSTGRES_DSN=<same DSN>  BOT_CONTEXT_POSTGRES_SCHEMA=bot_context
# DB_HOST.. stay (rollback). The bot context volume
# (/var/lib/herorestaurant) stays mounted but unused once BOT_CONTEXT_DRIVER=postgres.
source "$(dirname "$0")/lib.sh"
guard "GO switch"
: "${BACKEND_ENV:?env file the release script passes to the backend (see runbook 1.2)}"
NEW="$CUTOVER_DIR/backend.env.pg"
need_names="DB_DRIVER DATABASE_URL DB_PG_TOOLS_QA_APPROVED BOT_CONTEXT_DRIVER BOT_CONTEXT_POSTGRES_DSN BOT_CONTEXT_POSTGRES_SCHEMA"
if [[ -r "$NEW" && -r "$BACKEND_ENV" ]]; then
	say "NAME diff (values never shown):"; diff <(env_names "$BACKEND_ENV") <(env_names "$NEW") || true
	for k in $need_names; do grep -q "^$k=" "$NEW" && pass "$NEW has $k" || failc "$NEW lacks $k"; done
	say "DB_DRIVER new=$(sed -n 's/^DB_DRIVER=//p' "$NEW")"
	[[ ${FAILS:-0} == 0 ]] || die "env file incomplete"
else
	say "$NEW or $BACKEND_ENV not readable yet (prepare them before Sunday)"
fi
say "operator: review the diff, then re-run with --execute"
run docker tag newvillacarmen-prod-backend:privileged newvillacarmen-prod-backend:mysql-pre-cutover
run install -m 600 "$NEW" "$BACKEND_ENV"
run bash -c "${BACKEND_UP_CMD:-echo 'BACKEND_UP_CMD not set: e.g. /var/www/newvillacarmen/backend/deploy/release-backend-prod.sh with the PG image (confirm how it passes env)'}"
run bash -c "for i in \$(seq 45); do curl -fsS http://127.0.0.1:8080/healthz >/dev/null && exit 0; sleep 2; done; exit 1"
say "SWITCH_AT=$(date -Iseconds) (record it; 90-rollback needs it)"
