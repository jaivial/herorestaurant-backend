#!/usr/bin/env bash
# 51-unfreeze.sh - after the GO decision (50-smoke clean): start the other
# writers and switch the maintenance page off. Split from 50-smoke on kraken
# 2026-11-15 so the unfreeze only happens after bot reply + clean logs.
source "$(dirname "$0")/lib.sh"
guard "GO unfreeze"
for c in $CONTAINERS_START_ORDER; do run docker start "$c"; done
run bash -c "${MAINT_OFF_CMD:-echo 'MAINT_OFF_CMD not set'}"
run bash -c "sleep 3; for u in https://alqueriavillacarmen.com/ https://backoffice.alqueriavillacarmen.com/; do curl -s -o /dev/null -w \"\$u %{http_code}\\n\" \"\$u\"; done"
say "UNFREEZE_AT=$(date -Iseconds) (write it in the runbook log)"
