#!/usr/bin/env bash
# Measure SSR-critical backoffice endpoints via direct curl against the Go backend.
# Logs in once, then times each endpoint with the session cookie.
# Baseline/regression harness for Phase 0 / Phase 3 of the SSR perf work.
#
# Usage: scripts/perf-ssr.sh [BASE_URL]
set -euo pipefail

BASE="${1:-http://127.0.0.1:8085}"
EMAIL="${BOOTSTRAP_ADMIN_EMAIL:-admin@villacarmen.com}"
PASSWORD="${BOOTSTRAP_ADMIN_PASSWORD:-admin123}"
DATE="2026-08-10"

ENDPOINTS=(
  "/api/admin/bookings?date=${DATE}&page=1&count=15&sort=reservation_time&dir=asc"
  "/api/admin/calendar?year=2026&month=8"
  "/api/admin/config/daily-limit?date=${DATE}"
  "/api/admin/dashboard/metrics?date=${DATE}"
  "/api/admin/config/day?date=${DATE}"
  "/api/admin/config/defaults"
  "/api/admin/config/floors/defaults"
  "/api/admin/config/restaurant-info"
  "/api/admin/comida/counts"
)

echo "== login =="
LOGIN="$(curl -sS -c /tmp/bo-perf-cookies.txt -H 'Content-Type: application/json' \
  -d "{\"identifier\":\"${EMAIL}\",\"password\":\"${PASSWORD}\"}" \
  "${BASE}/api/admin/login")"
if ! echo "${LOGIN}" | grep -q '"success":true'; then
  echo "login failed: ${LOGIN}" >&2
  exit 1
fi
echo "logged in"

printf '%-90s %10s %10s\n' "endpoint" "time_total" "size_bytes"
for ep in "${ENDPOINTS[@]}"; do
  out="$(curl -sS -o /tmp/bo-perf-body.tmp -w '%{time_total} %{size_download}' \
    -b /tmp/bo-perf-cookies.txt "${BASE}${ep}")"
  read -r t s <<<"${out}"
  printf '%-90s %10.4f %10s\n' "${ep}" "${t}" "${s}"
done

rm -f /tmp/bo-perf-cookies.txt /tmp/bo-perf-body.tmp
