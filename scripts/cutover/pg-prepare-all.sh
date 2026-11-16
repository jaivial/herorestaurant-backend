#!/usr/bin/env bash
# pg-prepare-all.sh - rehearsal gate (pgnative_sqlcheck_v2): plan every
# PostgreSQL statement of the backend against a PG container, read-only.
#
#   scripts/cutover/pg-prepare-all.sh <pg-container> [db] [plan.sql] [out.tsv]
#
# Uses EXPLAIN (GENERIC_PLAN) (PG >= 16), NOT plain PREPARE: ON CONFLICT
# arbiter inference (SQLSTATE 42P10) happens in the planner, so a PREPARE-only
# gate passes statements that fail at EXECUTE (bot_inbound_queue rollback).
# One READ ONLY transaction per run, always rolled back; nothing is written.
# Runs twice (search_path public, then bot_context,public: the bot store's
# schema); a site fails only if it fails in both.
# Exit 0 = no errors outside the known allow-list, 1 = new errors, 2 = usage.
set -euo pipefail

C="${1:?usage: $0 <pg-container> [db] [plan.sql] [out.tsv]}"
DB="${2:-newvillacarmen_business}"
PLAN="${3:-}"
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
OUT="${4:-$ROOT/docs/pgnative/pg-runtime-sql-check.tsv}"
# Known non-PG-specific failures (schema drift present in both dialects,
# SQLite-only source reads); see docs/pgnative/pg-runtime-sql-audit.tsv.
ALLOW="$ROOT/scripts/cutover/pg-prepare-allow.txt"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
cd "$ROOT"
go run ./cmd/pgnative-sqlcheck -emit-psql "$work/check.sql"

outs=()
for sp in "public" "bot_context, public"; do
  n=${#outs[@]}
  { echo "SET search_path = $sp;"; cat "$work/check.sql"; } >"$work/run$n.sql"
  docker exec -i -u postgres "$C" psql -X -q -d "$DB" -f - <"$work/run$n.sql" 2>"$work/run$n.out" >/dev/null || true
  grep -q '^@@end' "$work/run$n.out" || { echo "psql run '$sp' did not finish" >&2; tail -5 "$work/run$n.out" >&2; exit 2; }
  outs+=("$work/run$n.out")
done

args=(-psql-result "$(IFS=,; echo "${outs[*]}")" -out "$OUT")
[ -n "$PLAN" ] && args+=(-plan "$PLAN")
go run ./cmd/pgnative-sqlcheck "${args[@]}"

# Errors keyed by file:line (column dropped) minus the allow-list (an entry
# "file:line" matches that line, a bare "file" matches the whole file).
pat="$work/allow.pat"
{ grep -v '^#' "$ALLOW" 2>/dev/null || true; } | cut -f1 | sed '/^$/d' |
  awk '{ k=$0; gsub(/\./,"\\.",k); if ($0 ~ /\.go:[0-9]+$/) print "^" k "(\t|:)"; else print "^" k ":" }' >"$pat"
echo '^$' >>"$pat"
new="$(awk -F'\t' '$3=="error"{split($1,p,":"); print p[1]":"p[2]"\t"$4}' "$OUT" | sort -u | grep -v -E -f "$pat" || true)"
lint="$(awk -F'\t' '$2=="lint" && $3=="b_on_conflict"{print $1"\t"$4}' "$OUT" | grep -v -E -f "$pat" || true)"
if [ -n "$new$lint" ]; then
  echo "FAIL: PG statements that do not plan (or ON CONFLICT targets matching no unique key):" >&2
  printf '%s\n' "$new" "$lint" | sed '/^$/d' >&2
  exit 1
fi
echo "OK: every PG statement plans on $C/$DB (report: $OUT)"
