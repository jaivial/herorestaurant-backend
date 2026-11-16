#!/usr/bin/env bash
# runtime-smoke-pg.sh - broad RUNTIME smoke of the Go backend on PostgreSQL.
#
# Why: the 2026-11 cutover was rolled back because a real WhatsApp webhook hit
# INSERT ... ON CONFLICT (restaurant_id, message_id) on bot_inbound_queue and PG
# answered SQLSTATE 42P10 (the unique index is on app.norm_ascii(message_id)).
# The cutover smoke (50-smoke.sh) only read healthz/menus/bookings/POS. This one
# drives WRITE paths and fails on any SQLSTATE / 23505 / 42P10 / panic / 500.
#
# DESKTOP ONLY. Never kraken, never production, never a real provider:
#   * scratch PG = NEW throwaway container $PGC (network none), restored from a
#     pg_dump of the rehearsal container $SRC_PGC (read-only use of $SRC_PGC);
#     reached only through a unix socket bind-mounted into $OUT/pgsock.
#   * the backend runs inside `unshare -rn` (own network namespace, loopback
#     only): WhatsApp/evo/uazapi, SMTP, Stripe, MiniMax, Bunny... cannot be
#     reached whatever URL the copied DB holds. Provider env is blanked too,
#     WA_OUTBOUND_BLOCK_ALL=1, and all keys are throwaway test values.
#   * a QA superadmin bo_user is created in the scratch DB with a random
#     password kept in $OUT (0600); nothing secret is printed.
#
# Usage:
#   scripts/cutover/runtime-smoke-pg.sh                 # build worktree, run
#   BIN=/path/to/server scripts/cutover/runtime-smoke-pg.sh   # given binary
# Env: SRC_PGC (rehearsal container), PGC (scratch container, recreated DB each
#   run unless REUSE_DB=1), OUT (/tmp/vc-runtime-smoke), PORT (18099),
#   WORKER_SECONDS (60), RID (1), SKIP_BLANKET=1 (skip the every-route pass),
#   SRC_TREE (Go sources matching BIN; default the worktree). For the exact
#   committed code: git archive HEAD | tar -x -C /tmp/src; build there; pass
#   BIN=... SRC_TREE=/tmp/src.
# Exit: 0 clean, 1 failures (see $RUN/summary.txt), 2 setup error.
set -uo pipefail

W=${WORKTREE:-$(cd "$(dirname "$0")/../.." && pwd)}
SRC_TREE=${SRC_TREE:-$W}   # Go sources matching BIN (static checks + route list)
OUT=${OUT:-/tmp/vc-runtime-smoke}
SRC_PGC=${SRC_PGC:-instinct-villacarmen-padrehearsal-20261113}
PGC=${PGC:-vc-runtime-smoke-pg}
PGDB=${PGDB:-newvillacarmen_business}
PORT=${PORT:-18099}
RID=${RID:-1}
WORKER_SECONDS=${WORKER_SECONDS:-60}
SOCK="$OUT/pgsock"
CONN="host=$SOCK user=postgres dbname=$PGDB"

die() { echo "runtime-smoke: $*" >&2; exit 2; }
say() { echo "[$(date +%H:%M:%S)] $*"; }

# ---------------------------------------------------------------- inner phase
# Runs inside `unshare -rn`: no route to anything but loopback.
if [[ "${1:-}" == "--inner" ]]; then
  RUN=$2
  ip link set lo up || die "cannot bring lo up in the namespace"
  curl -sS -m 3 -o /dev/null https://example.com 2>/dev/null && die "network namespace is NOT isolated"
  . "$RUN/env.sh"
  LOG="$RUN/server.log"
  "$RUN/server" >"$LOG" 2>&1 &
  SPID=$!
  B="http://127.0.0.1:$PORT"
  for _ in $(seq 60); do curl -fsS -m 2 -o /dev/null "$B/api/healthz" && break; sleep 1; done
  curl -fsS -m 2 -o /dev/null "$B/api/healthz" || { kill $SPID; tail -20 "$LOG" >&2; die "server did not start"; }

  mkdir -p "$RUN/resp"; : >"$RUN/requests.tsv"; : >"$RUN/attributed.tsv"
  N=0; COOKIE=""
  ERR_RE='SQLSTATE|42P10|23505|panic|pq: |sql: |ERROR: |runtime error'
  attribute() { # $1 label: errors that appeared in the log during the call
    local now; now=$(stat -c %s "$LOG")
    if (( now > LOGOFF )); then
      tail -c +$((LOGOFF + 1)) "$LOG" | head -c $((now - LOGOFF)) | grep -E "$ERR_RE" \
        | sed "s|^|$1\t|" >>"$RUN/attributed.tsv"
    fi
    LOGOFF=$now
  }
  LOGOFF=$(stat -c %s "$LOG"); attribute "startup"
  # req NAME METHOD PATH [BODY]  BODY: '' none | @file | form:<urlencoded> | json text
  req() {
    local name=$1 m=$2 p=$3 b=${4-} code
    N=$((N + 1)); LAST="$RUN/resp/$(printf %04d $N).$name"
    local a=(-sS -m 25 -o "$LAST" -w '%{http_code}' -X "$m" -H "X-Restaurant-Id: $RID"
      -H "X-Admin-Token: $ADMIN_TOKEN" -H "X-Vault-Key: $VAULT_KEY" -H "X-Bearer-Token: $BEARER_TOKEN_KEY" -H 'User-Agent: vc-runtime-smoke')
    [[ -n "$COOKIE" ]] && a+=(-H "Cookie: $COOKIE")
    case "$b" in
      '') ;;
      @*) a+=(-H 'Content-Type: application/json' --data-binary "$b") ;;
      form:*) a+=(-H 'Content-Type: application/x-www-form-urlencoded' --data-binary "${b#form:}") ;;
      *) a+=(-H 'Content-Type: application/json' --data-binary "$b") ;;
    esac
    local t0; t0=$(date +%s.%N)
    code=$(curl "${a[@]}" "$B$p" 2>/dev/null); code=${code:-000}
    printf '%s\t%s\t%s\t%s\t%s\t%s\n' "$code" "$m" "$p" "$name" "$t0" "$(date +%s.%N)" >>"$RUN/requests.tsv"
    sleep 0.05; attribute "$m $p ($name) [$code]"
  }
  jget() { jq -r "$1 // empty" "$LAST" 2>/dev/null | head -1; }

  # ---- 1. WhatsApp / evolution inbound webhook (the rollback path) -------
  INST=$(cat "$RUN/instance")
  EVO="/api/bot/webhook/evolution/$EVOLUTION_WEBHOOK_SECRET"
  T=$(date +%s)
  CUST="34699000111@s.whatsapp.net"
  up() { # id remoteJid fromMe text [participant] [mention-jid]
    local ctx=""; [[ -n "${6:-}" ]] && ctx=",\"contextInfo\":{\"mentionedJid\":[\"$6\"]}"
    printf '{"event":"messages.upsert","instance":"%s","data":{"key":{"remoteJid":"%s","fromMe":%s,"id":"%s"%s},"pushName":"QA Smoke","messageType":"%s","message":{"extendedTextMessage":{"text":"%s"%s}},"messageTimestamp":%s},"sender":"%s"}' \
      "$INST" "$2" "$3" "$1" "${5:+,\"participant\":\"$5\"}" extendedTextMessage "$4" "$ctx" "$T" "$2"
  }
  req evo-connection-open POST "$EVO" "{\"event\":\"connection.update\",\"instance\":\"$INST\",\"data\":{\"instance\":\"$INST\",\"state\":\"open\",\"statusReason\":200}}"
  MID="SMOKE${T}A1B2C3D4E5"
  req evo-inbound-1 POST "$EVO" "$(up "$MID" "$CUST" false 'Hola, quiero reservar para 4 el sábado')"
  req evo-inbound-1-dup POST "$EVO" "$(up "$MID" "$CUST" false 'Hola, quiero reservar para 4 el sábado')"
  req evo-inbound-2 POST "$EVO" "$(up "${MID}B" "$CUST" false 'a las 14:00')"
  req evo-inbound-casevariant POST "$EVO" "$(up "smoke${T}a1b2c3d4e5" "$CUST" false 'otra vez')"
  req evo-fromme-staff POST "$EVO" "$(up "${MID}S" "$CUST" true 'Hola, le atiende el personal')"
  req evo-fromme-staff-dup POST "$EVO" "$(up "${MID}S" "$CUST" true 'Hola, le atiende el personal')"
  req evo-after-takeover POST "$EVO" "$(up "${MID}P" "$CUST" false 'sigue ahi?')"
  req evo-group-unmentioned POST "$EVO" "$(up "${MID}G" "120363000000000001@g.us" false 'hola grupo' "34699000222@s.whatsapp.net")"
  req evo-reaction POST "$EVO" "{\"event\":\"messages.upsert\",\"instance\":\"$INST\",\"data\":{\"key\":{\"remoteJid\":\"$CUST\",\"fromMe\":false,\"id\":\"${MID}R\"},\"messageType\":\"reactionMessage\",\"message\":{\"reactionMessage\":{\"text\":\"👍\",\"key\":{\"id\":\"${MID}\"}}}}}"
  req evo-bad-secret POST "/api/bot/webhook/evolution/wrong" "$(up "${MID}X" "$CUST" false x)"
  req uazapi-webhook POST "/api/bot/webhook" "{\"EventType\":\"messages\",\"token\":\"qa-unknown\",\"message\":{\"messageid\":\"${MID}U\",\"chatid\":\"$CUST\",\"text\":\"hola\",\"fromMe\":false}}"
  sleep 5; attribute "bot worker (async after webhook)"

  # ---- 2. admin login -----------------------------------------------------
  N=$((N + 1)); LAST="$RUN/resp/$(printf %04d $N).login"; t0=$(date +%s.%N)
  hdr=$(curl -sS -m 20 -D - -o "$LAST" -H 'Content-Type: application/json' -H "X-Vault-Key: $VAULT_KEY" -H "X-Bearer-Token: $BEARER_TOKEN_KEY" \
        --data-binary @"$RUN/login.json" "$B/api/admin/login" 2>/dev/null)
  COOKIE=$(printf '%s' "$hdr" | tr -d '\r' | sed -n 's/^[Ss]et-[Cc]ookie: \(bo_session=[^;]*\).*/\1/p' | head -1)
  printf '%s\tPOST\t/api/admin/login\tlogin\t%s\t%s\n' "$(printf '%s' "$hdr" | head -1 | awk '{print $2}')" "$t0" "$(date +%s.%N)" >>"$RUN/requests.tsv"
  attribute "POST /api/admin/login"
  [[ -n "$COOKIE" ]] || echo "LOGIN_FAILED" >>"$RUN/attributed.tsv"
  req set-active-restaurant POST /api/admin/active-restaurant "{\"restaurantId\":$RID}"

  # ---- 3. bookings (BO) + public booking flow ----------------------------
  K=$W/docs/pgnative/qa-kit
  D1=$(date -d '+12 days' +%F); D2=$(date -d '+13 days' +%F)
  req bo-booking-create POST /api/admin/bookings "{\"reservation_date\":\"$D1\",\"reservation_time\":\"14:00\",\"party_size\":2,\"customer_name\":\"QA Smoke\",\"contact_phone\":\"699000111\",\"contact_phone_country_code\":\"34\",\"contact_email\":\"qa-smoke@invalid.local\",\"send_documents_to_client\":false}"
  BID=$(jget '.booking.id // .id // .booking_id // .bookingId')
  BID=${BID:-999999}
  req bo-booking-get GET "/api/admin/bookings/$BID"
  req bo-booking-patch PATCH "/api/admin/bookings/$BID" "{\"party_size\":3,\"reservation_time\":\"14:30\",\"commentary\":\"QA smoke modificación\"}"
  req bo-booking-table PATCH "/api/admin/bookings/$BID" "{\"table_number\":\"7\"}"
  req bo-booking-cancel POST "/api/admin/bookings/$BID/cancel" '{"reason":"QA smoke"}'
  req bo-booking-reactivate POST "/api/admin/bookings/cancelled/$BID/reactivate" '{}'
  req bo-booking-extras POST /api/admin/booking-extras @"$K/booking-extra-1.json"
  req bo-booking-extras-dup POST /api/admin/booking-extras @"$K/booking-extra-2-dup.json"
  req pub-closed-days GET "/api/reservations/closed-days"
  req pub-day-context GET "/api/reservations/day-context?date=$D2"
  req pub-month-avail GET "/api/reservations/month-availability?year=${D2:0:4}&month=${D2:5:2}"
  req pub-hour-data GET "/api/reservations/hour-data?date=$D2"
  req pub-check-date POST /api/check_date_availability.php "form:date=$D2&party_size=4"
  req pub-check-party POST /api/check_party_size_availability.php "form:date=$D2&party_size=4"
  req pub-avail-context POST /api/get_booking_availability_context.php "form:date=$D2&party_size=4"
  req pub-booking-front POST /api/bookings/front "form:reservation_date=$D2&reservation_time=14:00&party_size=4&customer_name=QA+Front&contact_phone=699000333&country_code=34&contact_email=qa-front%40invalid.local&commentary=QA&high_chairs=0&baby_strollers=0"
  FBID=$(jget '.booking_id // .bookingId // .id')
  req pub-booking-front-legacy POST /api/insert_booking_front.php "form:reservation_date=$D2&reservation_time=15:00&party_size=2&customer_name=QA+Legacy&contact_phone=699000444&country_code=34&contact_email=qa-legacy%40invalid.local"
  req pub-contact-lookup POST /api/reservations/contact-lookup '{"phone":"699000333","country_code":"34"}'
  req pub-modify-context POST /api/reservations/modify-context "{\"booking_id\":${FBID:-0},\"phone\":\"699000333\"}"
  req pub-modify POST /api/reservations/modify "{\"booking_id\":${FBID:-0},\"phone\":\"699000333\",\"party_size\":5}"
  req pub-validate-modifiable POST /api/validate_booking_modifiable.php "form:id=${FBID:-0}"
  req pub-update-reservation POST /api/update_reservation.php "form:id=${FBID:-0}&party_size=5&reservation_date=$D2&reservation_time=14:00"
  req pub-save-mod-history POST /api/save_modification_history.php "form:booking_id=${FBID:-0}&field=party_size&old_value=4&new_value=5"
  req pub-checkcancel POST /api/checkcancel.php "form:id=${FBID:-0}"
  req pub-cancel POST /api/cancel_reservation.php "form:id=${FBID:-0}"
  req pub-confirm POST /api/confirm_reservation.php "form:id=${FBID:-0}"
  req pub-unsubscribe POST /api/unsubscribe "{\"booking_id\":${FBID:-0},\"channel\":\"whatsapp\",\"reason\":\"qa\"}"
  req pub-unsubscribe-2 POST /api/unsubscribe "{\"booking_id\":${FBID:-0},\"channel\":\"WhatsApp\",\"reason\":\"qa\"}"
  req pub-save-conv-msg POST /api/save_conversation_message.php "form:sender=34699000111&role=user&message=hola"
  req pub-save-conv-state POST /api/save_conversation_state.php "form:sender=34699000111&state=%7B%7D"
  req legacy-update-daily-limit POST /api/update_daily_limit.php "form:date=$D2&daily_limit=120"
  req legacy-update-daily-limit-2 POST /api/update_daily_limit.php "form:date=$D2&daily_limit=121"
  req legacy-mesas-dos POST /api/limitemesasdedos.php "form:date=$D2&daily_limit=6"
  req legacy-mesas-dos-2 POST /api/limitemesasdedos.php "form:date=$D2&daily_limit=7"
  req legacy-save-hour POST /api/savehourdata.php "{\"date\":\"$D2\",\"hourData\":{\"13:30\":{\"status\":\"open\",\"capacity\":20},\"14:00\":{\"status\":\"open\",\"capacity\":30}}}"
  req legacy-save-hour-2 POST /api/savehourdata.php "{\"date\":\"$D2\",\"hourData\":{\"13:30\":{\"status\":\"open\",\"capacity\":25}}}"
  req legacy-opening-hours POST /api/editopeninghours.php "{\"date\":\"$D2\",\"hours\":[\"13:30\",\"14:00\"]}"
  req legacy-hour-pct POST /api/updatehourpercentages.php "{\"date\":\"$D2\",\"percentages\":{\"13:30\":50,\"14:00\":50}}"
  req legacy-hour-pct-2 POST /api/updatehourpercentages.php "{\"date\":\"$D2\",\"percentages\":{\"13:30\":40,\"14:00\":60}}"
  req legacy-salon-condesa POST /api/salon_condesa_api.php "form:date=$D2&state=1"
  req legacy-insert-booking POST /api/insert_booking.php "form:reservation_date=$D2&reservation_time=13:30&party_size=2&customer_name=QA+Admin&contact_phone=699000555"

  # ---- 4. menus / comida / settings / members / roles / tables -------------
  for f in comida-category-1 comida-category-2-dup; do req "$f" POST /api/admin/comida/categorias @"$K/$f.json"; done
  for f in plato-category-1 plato-category-2-dup; do req "$f" POST /api/admin/comida/platos/categorias @"$K/$f.json"; done
  req menu-dia-dish POST /api/admin/menus/dia/dishes @"$K/menu-dia-dish.json"
  DID=$(jget '.dish.id // .id'); DID=${DID:-999999}
  req menu-dia-dish-patch PATCH "/api/admin/menus/dia/dishes/$DID" '{"descripcion":"QA Paella mixta","active":false}'
  req gm-v2-draft POST /api/admin/group-menus-v2/drafts @"$K/group-menu-v2-draft.json"
  GID=$(jget '.menu.id // .id // .menuId'); GID=${GID:-999999}
  req gm-v2-basics PATCH "/api/admin/group-menus-v2/$GID/basics" '{"title":"QA Menú Smoke","price":"35.00","active":false}'
  req gm-v2-section POST "/api/admin/group-menus-v2/$GID/sections" '{"title":"Entrantes","kind":"entrantes"}'
  req gm-v2-publish POST "/api/admin/group-menus-v2/$GID/publish" '{}'
  req gm-v2-toggle POST "/api/admin/group-menus-v2/$GID/toggle-active" '{}'
  req menu-visibility POST /api/admin/menu-visibility '{"menuKey":"menudeldia","isActive":false}'
  req menu-visibility-2 POST /api/admin/menu-visibility '{"menuKey":"menudeldia","isActive":true}'
  req page-visibility PATCH /api/admin/restaurant/pages/visibility '{"postres":true}'
  req page-visibility-2 PATCH /api/admin/restaurant/pages/visibility '{"postres":false}'
  req cafes-seo PUT /api/admin/restaurant/pages/cafes-seo '{"title":"QA cafés","description":"QA"}'
  req widget-settings PUT /api/admin/widget/settings '{"primary_color":"#123456"}'
  req widget-settings-2 PUT /api/admin/widget/settings '{"primary_color":"#654321"}'
  req vino-create POST /api/admin/vinos '{"tipo":"TINTO","nombre":"QA Vino","precio":12.5,"bodega":"QA","denominacion_origen":"QA","active":1}'
  req seo-sync POST /api/admin/seo/item-routes/sync '{}'
  req dishes-catalog-upsert POST /api/admin/dishes-catalog/upsert '{"title":"QA Plato catálogo","description":"QA","allergens":[]}'
  req dishes-catalog-upsert-2 POST /api/admin/dishes-catalog/upsert '{"title":"qa plato catalogo","description":"QA 2","allergens":[]}'
  req prefs-1 PUT /api/admin/me/preferences '{"key":"reservasDisplayMode","value":"grid"}'
  req prefs-2 PUT /api/admin/me/preferences '{"key":"reservasDisplayMode","value":"TABLA"}'
  req legal-page-1 POST /api/admin/legal-pages/aviso-legal @"$K/legal-page.json"
  req legal-page-2 POST /api/admin/legal-pages/aviso-legal @"$K/legal-page.json"
  req member-1 POST /api/admin/members @"$K/member-1.json"
  req member-2-dup POST /api/admin/members @"$K/member-2-dup.json"
  req role-1 POST /api/admin/roles @"$K/role-1.json"
  req role-2-dup POST /api/admin/roles @"$K/role-2-dup.json"
  req area-1 POST /api/admin/tables @"$K/area-1.json"
  req area-2-dup POST /api/admin/tables @"$K/area-2-dup.json"
  req salon-1 POST /api/admin/config/salons @"$K/salon-1.json"
  req booking-notifications PUT /api/admin/booking-notifications '{"email":true,"whatsapp":false}'
  req bot-config PUT /api/admin/bot/config '{"enabled":true}'
  req bot-settings PUT "/api/admin/bot/settings/$RID" '{"enabled":true}'
  req bot-ai PUT "/api/admin/bot/ai/$RID" '{"tone":"cercano"}'
  req bot-privileged-contacts PUT "/api/admin/bot/ai/$RID/privileged-contacts" '{"contacts":["34699000999@s.whatsapp.net"]}'
  req branding POST /api/admin/branding '{"primaryColor":"#112233"}'
  req website PUT /api/admin/website '{"title":"QA"}'

  # ---- 5. POS / stock / invoices / campaigns (no sends) --------------------
  for f in pos-category-1 pos-category-2-dup; do req "$f" POST /api/admin/pos/categories @"$K/$f.json"; done
  for f in pos-product-1 pos-product-2-dup-sku; do req "$f" POST /api/admin/pos/products @"$K/$f.json"; done
  CI=$(psql "$CONN" -Atc "SELECT id FROM comida_items WHERE restaurant_id=$RID ORDER BY id LIMIT 1" 2>/dev/null)
  for i in 1 2; do req "pos-import-confirm-$i" POST /api/admin/pos/products/import-confirm "{\"items\":[{\"sourceType\":\"COMIDA_ITEM\",\"sourceId\":${CI:-1}}]}"; done
  for f in pos-tag-1 pos-tag-2-dup; do req "$f" POST /api/admin/pos/tags @"$K/$f.json"; done
  for f in pos-pack-1 pos-pack-2-dup; do req "$f" POST /api/admin/pos/packs @"$K/$f.json"; done
  for f in pos-kitchen-station-1 pos-kitchen-station-2-dup; do req "$f" POST /api/admin/pos/kitchen/stations @"$K/$f.json"; done
  for f in pos-service-period-1 pos-service-period-2-dup; do req "$f" POST /api/admin/pos/service-periods @"$K/$f.json"; done
  for f in pos-modifier-group-1 pos-modifier-group-2-dup; do req "$f" POST /api/admin/pos/modifier-groups @"$K/$f.json"; done
  for f in pos-customer-1 pos-customer-2-dup; do req "$f" POST /api/admin/pos/customers @"$K/$f.json"; done
  for i in 1 2; do req "pos-role-perms-$i" PUT /api/admin/pos/roles/admin/permissions @"$K/pos-role-permissions.json"; done
  for f in stock-category-1 stock-category-2-dup; do req "$f" POST /api/admin/stock/categories @"$K/$f.json"; done
  for f in stock-warehouse stock-warehouse-dup; do req "$f" POST /api/admin/stock/warehouses @"$K/$f.json"; done
  for f in stock-supplier-1 stock-supplier-2-dup; do req "$f" POST /api/admin/stock/suppliers @"$K/$f.json"; done
  for f in stock-vat-1 stock-vat-2-dup; do req "$f" POST /api/admin/stock/vat-rates @"$K/$f.json"; done
  for f in stock-item-1 stock-item-2-dup-sku; do req "$f" POST /api/admin/stock/items @"$K/$f.json"; done
  for i in 1 2; do req "stock-role-perms-$i" PUT /api/admin/stock/roles/admin/permissions @"$K/stock-role-permissions.json"; done
  for f in stock-import-1-preview stock-import-1-confirm stock-import-2-confirm; do
    N=$((N + 1)); LAST="$RUN/resp/$(printf %04d $N).$f"; t0=$(date +%s.%N)
    code=$(curl -sS -m 25 -o "$LAST" -w '%{http_code}' -H "X-Restaurant-Id: $RID" -H "Cookie: $COOKIE" -H "X-Vault-Key: $VAULT_KEY" -H "X-Bearer-Token: $BEARER_TOKEN_KEY" \
      -H 'Content-Type: multipart/form-data; boundary=----qakitU4boundary' --data-binary @"$K/$f.multipart" \
      "$B/api/admin/stock/items/import" 2>/dev/null)
    printf '%s\tPOST\t/api/admin/stock/items/import\t%s\t%s\t%s\n' "${code:-000}" "$f" "$t0" "$(date +%s.%N)" >>"$RUN/requests.tsv"; attribute "POST stock/items/import ($f)"
  done
  req invoice-create POST /api/admin/invoices @"$K/invoice-create.json"
  IID=$(jget '.invoice.id // .id'); IID=${IID:-999999}
  req invoice-update PUT "/api/admin/invoices/$IID" @"$K/invoice-update.json"
  req invoice-dup-number POST /api/admin/invoices @"$K/invoice-create-dup-number.json"
  req campaign-create POST /api/admin/campanas @"$K/campaign-create.json"
  req analytics-refresh POST /api/admin/analytics/refresh '{}'
  req fichaje-start POST /api/admin/fichaje/start '{}'
  req fichaje-stop POST /api/admin/fichaje/stop '{}'

  # ---- 6. blanket pass: every route once (GET with id 1; writes with {} on
  # id 999999). Shallow (most writes stop at validation) but every handler's
  # first queries run on PG. Skips session-killing routes and websockets.
  if [[ "${SKIP_BLANKET:-}" != 1 ]]; then
    while IFS=$'\t' read -r m p; do
      case "$p" in
        */logout|*/login|*/me/password|*/active-restaurant|*/ws|*/ws/*|*/events|*/stream*|*/sse*) continue ;;
        /api/bot/webhook*|/api/mcp*|/api/plugin*) continue ;;
      esac
      if [[ $m == GET ]]; then q=$(sed -E 's/\{[^}]+\}/1/g' <<<"$p"); req blanket GET "$q"
      else q=$(sed -E 's/\{[^}]+\}/999999/g' <<<"$p"); req blanket "$m" "$q" '{}'; fi
    done <"$RUN/routes.tsv"
  fi

  # ---- 7. background workers (reminders, wa-outbox, watchdog, stock, ...) --
  LOGOFF=$(stat -c %s "$LOG")
  sleep "$WORKER_SECONDS"; attribute "background workers (${WORKER_SECONDS}s)"
  kill "$SPID" 2>/dev/null; wait "$SPID" 2>/dev/null
  exit 0
fi

# ---------------------------------------------------------------- outer phase
[[ "$(hostname -s)" != kraken ]] || die "desktop only (refusing on kraken)"
[[ "$PGC" != "$SRC_PGC" ]] || die "PGC must be a throwaway container, not the source"
for t in docker psql curl jq unshare htpasswd python3; do command -v $t >/dev/null || die "missing $t"; done
TS=$(date +%Y%m%d-%H%M%S); RUN="$OUT/run-$TS"
install -d -m 700 "$OUT" "$RUN"; mkdir -p "$SOCK"; chmod 777 "$SOCK" 2>/dev/null || true

# binary
if [[ -z "${BIN:-}" ]]; then
  say "build $W/cmd/server (working tree) -> $RUN/server"
  (cd "$W" && go build -buildvcs=false -o "$RUN/server" ./cmd/server) || die "build failed"
else
  cp "$BIN" "$RUN/server" || die "BIN not readable"
fi
sha256sum "$RUN/server" | awk '{print $1}' >"$RUN/server.sha256"

# scratch PG
if ! docker inspect "$PGC" >/dev/null 2>&1; then
  say "create throwaway container $PGC (network none, socket in $SOCK)"
  docker run -d --name "$PGC" --network none --memory 2g -e POSTGRES_HOST_AUTH_METHOD=trust \
    -v "$SOCK:/var/run/postgresql:z" postgres:17-alpine >/dev/null || die "docker run"
elif [[ "$(docker inspect -f '{{.State.Running}}' "$PGC")" != true ]]; then docker start "$PGC" >/dev/null; fi
for _ in $(seq 60); do docker exec "$PGC" pg_isready -q -h /var/run/postgresql 2>/dev/null && break; sleep 1; done
if [[ "${REUSE_DB:-}" != 1 ]]; then
  say "restore $PGDB from $SRC_PGC into $PGC (fresh copy)"
  docker exec -u postgres "$PGC" dropdb --if-exists --force "$PGDB" && docker exec -u postgres "$PGC" createdb "$PGDB" || die "createdb"
  docker exec -u postgres "$SRC_PGC" pg_dump -Fc "$PGDB" \
    | docker exec -i -u postgres "$PGC" pg_restore -d "$PGDB" --no-owner --exit-on-error || die "restore failed"
fi
psql "$CONN" -Atc 'select 1' >/dev/null || die "scratch PG not reachable on $SOCK"
# PG server log = ground truth: handlers often swallow the error (no app log)
# but PG logs every failed statement. %e adds the SQLSTATE.
psql "$CONN" -qc "ALTER SYSTEM SET log_line_prefix = '%m [%p] %e '" -c "ALTER SYSTEM SET log_min_error_statement = 'error'" -c "SELECT pg_reload_conf()" >/dev/null

# marker acceptance string = exactly what the imported marker says
ACCEPT=$(psql "$CONN" -Atc "SELECT 'orphans='||(value->>'allowlisted_orphan_rows')||',pending='||(value->>'pending_blocking_decisions')||',fks_not_valid='||jsonb_array_length(coalesce(value->'fks_not_valid_staging','[]'::jsonb)) FROM dbmigrate.marker WHERE key='import_verified'")

# seed: QA superadmin + active evo instance (scratch only)
QA_EMAIL=qa-runtime-smoke@invalid.local
QA_PASS=$(head -c 18 /dev/urandom | base64 | tr -dc 'A-Za-z0-9' | head -c 20)
HASH=$(htpasswd -nbBC 10 x "$QA_PASS" | cut -d: -f2 | sed 's/^\$2y\$/$2a$/')
psql "$CONN" -v ON_ERROR_STOP=1 -q -v email="$QA_EMAIL" -v hash="$HASH" -v rid="$RID" <<'SQL' || die "seed failed"
DELETE FROM bo_sessions WHERE user_id IN (SELECT id FROM bo_users WHERE email = :'email');
DELETE FROM bo_user_restaurants WHERE user_id IN (SELECT id FROM bo_users WHERE email = :'email');
DELETE FROM bo_users WHERE email = :'email';
INSERT INTO bo_users (email, username, name, password_hash, is_superadmin, must_change_password)
VALUES (:'email', 'qa-runtime-smoke', 'QA Runtime Smoke', :'hash', 1, 0);
INSERT INTO bo_user_restaurants (user_id, restaurant_id, role)
SELECT id, :rid, 'root' FROM bo_users WHERE email = :'email';
-- 0.3 = highest general version (0.4 is a restricted module set): every section open
UPDATE bo_user_restaurants SET app_version = '0.3'
WHERE user_id IN (SELECT id FROM bo_users WHERE email = :'email');
SQL
INST=$(psql "$CONN" -Atc "SELECT provider_instance_id FROM restaurant_uazapi_instances WHERE restaurant_id=$RID AND is_active=1 AND provider_instance_id<>'' LIMIT 1")
if [[ -z "$INST" ]]; then
  INST="qa-smoke-instance-$RID"
  psql "$CONN" -qc "UPDATE restaurant_uazapi_instances SET provider_instance_id='$INST', is_active=1 WHERE restaurant_id=$RID" >/dev/null
fi
echo "$INST" >"$RUN/instance"
jq -n --arg i "$QA_EMAIL" --arg p "$QA_PASS" '{identifier:$i,password:$p}' >"$RUN/login.json"; chmod 600 "$RUN/login.json"
unset QA_PASS HASH

# env: start from nothing (env -i below), only throwaway values
rnd() { head -c 24 /dev/urandom | base64 | tr -dc 'A-Za-z0-9' | head -c 32; }
cat >"$RUN/env.sh" <<EOF
export PATH=/usr/bin:/bin HOME=$RUN TZ=Europe/Madrid
export DB_DRIVER=postgres DB_PG_PRODUCTION_APPROVED=1 DB_PG_ACCEPT_IMPORT_STAGING='$ACCEPT'
export DATABASE_URL='postgres://postgres@/$PGDB?host=$SOCK&sslmode=disable'
export BOT_CONTEXT_DRIVER=postgres BOT_CONTEXT_POSTGRES_DSN='postgres://postgres@/$PGDB?host=$SOCK&sslmode=disable' BOT_CONTEXT_POSTGRES_SCHEMA=bot_context
export PORT=$PORT RID=$RID W='$W' CONN='$CONN' WORKER_SECONDS=$WORKER_SECONDS SKIP_BLANKET=${SKIP_BLANKET:-}
export EVOLUTION_WEBHOOK_SECRET=$(rnd) ADMIN_TOKEN=$(rnd) VAULT_KEY=$(rnd) BEARER_TOKEN_KEY=$(rnd) BACKOFFICE_VAULT_KEY=$(rnd) INTERNAL_API_TOKEN=$(rnd)
export WA_OUTBOUND_BLOCK_ALL=1 BOT_PUBLIC_WEBHOOK_URL=http://127.0.0.1:9/qa PUBLIC_BASE_URL=http://127.0.0.1:$PORT BACKOFFICE_PUBLIC_BASE_URL=http://127.0.0.1:9
export MINIMAX_BASE_URL=http://127.0.0.1:9 MINIMAX_API_KEY=qa-invalid BOT_PIPELINE_URL=http://127.0.0.1:9 PADDLEOCR_GATEWAY_URL=http://127.0.0.1:9
export UAZAPI_URL=http://127.0.0.1:9 UAZAPI_TOKEN=qa-invalid SMTP_HOST=127.0.0.1 STRIPE_SECRET_KEY=sk_test_qa_invalid
EOF
chmod 600 "$RUN/env.sh"
python3 -I "$W/scripts/cutover/runtime-smoke-routes.py" "$SRC_TREE" >"$RUN/routes.tsv"

say "static: ON CONFLICT targets vs live catalog"
python3 -I "$W/scripts/cutover/runtime-smoke-onconflict.py" "$SRC_TREE" "$CONN" >"$RUN/onconflict.tsv"; OC_RC=$?

say "runtime: backend in an isolated network namespace (port $PORT, workers ${WORKER_SECONDS}s)"
psql "$CONN" -qAtc "SELECT pg_stat_reset()" >/dev/null   # coverage: tables written during the run
PGLOG_SINCE=$(date -u +%Y-%m-%dT%H:%M:%SZ)
env -i PATH="$PATH" HOME="$RUN" unshare -rn bash "$0" --inner "$RUN" || die "inner phase failed (see $RUN/server.log)"
docker logs --since "$PGLOG_SINCE" "$PGC" >"$RUN/pg.log" 2>&1
sleep 1   # stats collector flush
psql "$CONN" -AtF $'\t' -c "SELECT schemaname||'.'||relname, n_tup_ins, n_tup_upd, n_tup_del FROM pg_stat_user_tables WHERE n_tup_ins+n_tup_upd+n_tup_del > 0 ORDER BY 1" >"$RUN/tables-written.tsv"
# distinct PG errors: count, SQLSTATE, message, first line of the statement.
# KNOWN_PG = failures that are identical on the MySQL source (object absent
# there too: legacy DIA/FINDE tables, columns the code probes and falls back
# from). They are reported, but do not fail the run. Everything else fails,
# except 23505/23503 (unique/FK violation) raised while a request was in
# flight that the handler answered with a 4xx (expected for the *-dup bodies).
# Each error lists the request(s) in flight when PG logged it (by timestamp).
KNOWN_PG='relation "(DIA|FINDE)" does not exist|column "restaurant_id" does not exist.*site_builder_domain_mappings|column "draft_html_content" of relation "restaurant_websites"|column "(metadata_json|sort_order)" of relation "restaurant_areas"|column "pos_pin_hash" does not exist'
python3 -I - "$RUN/pg.log" "$KNOWN_PG" "$RUN/requests.tsv" >"$RUN/pg-errors.tsv" <<'PY'
import collections, datetime, re, sys
lines = open(sys.argv[1], errors="replace").read().splitlines()
known = re.compile(sys.argv[2])
import os
reqs = []
for i, l in enumerate(open(sys.argv[3]), 1):
    f = l.rstrip("\n").split("\t")
    if len(f) >= 6:
        code = f[0]
        # legacy handlers answer a refused write with 200 + success:false
        try:
            body = open(os.path.join(os.path.dirname(sys.argv[3]), "resp", f"{i:04d}.{f[3]}"), errors="replace").read(4000)
            if code.startswith("2") and re.search(r'"success"\s*:\s*false', body):
                code = "4xx(" + code + " success:false)"
        except OSError:
            pass
        reqs.append((float(f[4]), float(f[5]), code, f"{f[1]} {f[2]} ({f[3]}) [{f[0]}]"))
def owner(ts):
    # the latest request started before ts, if PG logged while it was in flight
    best = None
    for r in reqs:
        if r[0] - 0.02 <= ts:
            best = r
    if best and ts <= best[1] + 0.05:
        return best[2], best[3]
    return "", "background/async (no request in flight)"
c = collections.Counter()
who = collections.defaultdict(set)
for i, l in enumerate(lines):
    m = re.search(r"\] (\w{5}) ERROR:\s+(.*)", l) or re.search(r"\] ()ERROR:\s+(.*)", l)
    if not m:
        continue
    st = ""
    for j in (i + 1, i + 2):
        if j < len(lines) and "STATEMENT:" in lines[j]:
            st = lines[j].split("STATEMENT:", 1)[1].strip()
            break
    msg = re.sub(r" at character \d+", "", m.group(2))
    key = (m.group(1), msg, re.sub(r"\s+", " ", st)[:200])
    c[key] += 1
    tm = re.match(r"(\d{4}-\d\d-\d\d \d\d:\d\d:\d\d\.\d+) UTC", l)
    if tm:
        ts = datetime.datetime.strptime(tm.group(1), "%Y-%m-%d %H:%M:%S.%f").replace(tzinfo=datetime.timezone.utc).timestamp()
        who[key].add(owner(ts))
for key, n in c.most_common():
    code, msg, st = key
    statuses = {s for s, _ in who[key]}
    labels = sorted(l for _, l in who[key])
    if known.search(msg + " " + st):
        tag = "known"
    elif code in ("23505", "23503") and statuses and all(s[:1] == "4" for s in statuses):
        tag = "client-4xx"  # unique/FK violation turned into a 4xx by the handler (same on MySQL)
    else:
        tag = "FAIL"
    shown = "; ".join(labels[:3]) + (f"; +{len(labels) - 3} more" if len(labels) > 3 else "")
    print(f"{tag}\t{n}\t{code}\t{msg}\t{st}\t{shown}")
PY

# ---------------------------------------------------------------- report
# 500s: attach the response body; allowlist = validation messages that the
# legacy handlers answer with 500 on MySQL too, and DIA/FINDE (tables absent
# on MySQL as well). Anything else is a failure.
KNOWN500='parameter is required|Invalid date format|must sum to 100|(Error (consultando|insertando en|actualizando|eliminando) (DIA|FINDE))|VAULT_TOKEN|Error cargando configuraci'
# (DIA/FINDE: tables absent on MySQL too; VAULT_TOKEN/"Error cargando
# configuración": minimax config needs the real VAULT_TOKEN to decrypt.)
: >"$RUN/http500.tsv"
n=0
while IFS=$'\t' read -r code m p name _; do
  n=$((n + 1)); [[ $code == 500 ]] || continue
  body=$(head -c 300 "$RUN/resp/$(printf %04d $n).$name" 2>/dev/null | tr '\n\t' '  ')
  tag=FAIL; grep -qE "$KNOWN500" <<<"$body" && tag=known
  printf '%s\t%s %s (%s)\t%s\n' "$tag" "$m" "$p" "$name" "$body" >>"$RUN/http500.tsv"
done <"$RUN/requests.tsv"
S="$RUN/summary.txt"
{
  echo "runtime-smoke-pg $TS  binary sha256=$(cat "$RUN/server.sha256")"
  echo "requests: $(wc -l <"$RUN/requests.tsv")  by status: $(cut -f1 "$RUN/requests.tsv" | sort | uniq -c | awk '{printf "%s=%s ",$2,$1}')"
  echo; echo "== HTTP 500 (FAIL unless the body matches the known non-PG allowlist)"
  cat "$RUN/http500.tsv"
  echo; echo "== HTTP 502/503/504/000 (info: expected when a handler needs the blocked network)"
  awk -F'\t' '$1 ~ /^50[2-9]|^000/' "$RUN/requests.tsv"
  echo; echo "== SQL / panic lines attributed to a request (label<TAB>log line)"
  cat "$RUN/attributed.tsv"
  echo; echo "== coverage: $(wc -l <"$RUN/tables-written.tsv") tables written (ins/upd/del) -> tables-written.tsv"
  echo; echo "== PG server errors (tag count sqlstate message statement requests-in-flight)"
  cat "$RUN/pg-errors.tsv"
  echo; echo "== distinct SQLSTATEs in server.log"
  grep -oE 'SQLSTATE [0-9A-Z]{5}' "$RUN/server.log" | sort | uniq -c
  echo; echo "== static ON CONFLICT check (non-OK)"
  grep -v '^OK' "$RUN/onconflict.tsv"
} >"$S"
FAIL=0
grep -q '^FAIL' "$RUN/pg-errors.tsv" && FAIL=1
grep -qE 'panic|42P10' "$RUN/server.log" && FAIL=1
# SQLSTATE in the app log fails, except 23505 logged by a request answered 409/400
grep -E 'SQLSTATE' "$RUN/attributed.tsv" | grep -v 'SQLSTATE 23505' | grep -q . && FAIL=1
grep -E 'SQLSTATE 23505' "$RUN/attributed.tsv" | grep -vE '\[(409|400)\]' | grep -q . && FAIL=1
grep -q '^FAIL' "$RUN/http500.tsv" && FAIL=1
grep -q LOGIN_FAILED "$RUN/attributed.tsv" && FAIL=1
(( OC_RC != 0 )) && FAIL=1
cat "$S"
say "results: $RUN (summary.txt requests.tsv attributed.tsv server.log onconflict.tsv resp/)"
exit $FAIL
