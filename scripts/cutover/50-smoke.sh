#!/usr/bin/env bash
# 50-smoke.sh - HTTP smoke on the PG backend, then the GO/NO-GO decision.
source "$(dirname "$0")/lib.sh"
guard "GO smoke"
U=http://127.0.0.1:${BACKEND_PORT:-8080}
C="$CUTOVER_DIR/qa-cookie"   # QA session cookie prepared by the operator (never printed)
run curl -fsS -o /dev/null -w 'healthz %{http_code}\n' "$U/healthz"
run curl -fsS -o /dev/null -w 'public menus %{http_code}\n' "$U/api/menus/public"
run curl -fsS -b "$C" -o /dev/null -w 'admin bookings %{http_code}\n' "$U/api/admin/bookings?limit=1"
run curl -fsS -b "$C" -H 'Content-Type: application/json' -X POST "$U/api/admin/pos/visits" --data '{"channel":"TAKEAWAY"}' -o /dev/null -w 'pos visit %{http_code}\n'
say "bot: Jaime sends ONE WhatsApp message from +34692747052 (only this number); check the reply and that the log shows no other recipient"
run bash -c "docker logs --since 10m newvillacarmen-backend-prod 2>&1 | grep -Ec '23505|SQLSTATE|panic|\" 500 ' || true"
say "DECISION (Main/Jaime): GO -> unfreeze below; NO-GO -> 90-rollback.sh"
for c in $CONTAINERS_START_ORDER; do run docker start "$c"; done
run bash -c "${MAINT_OFF_CMD:-echo 'MAINT_OFF_CMD not set'}"
