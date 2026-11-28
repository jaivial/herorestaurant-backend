#!/usr/bin/env bash
# scripts/cutover/lib.sh - shared guard rails for the PG cutover scripts
# (coordination id: pg_cutover_v1). Sourced, never run directly.
# - DRY-RUN by default: mutating commands are printed, not executed.
# - Executes only with --execute AND CUTOVER_GO set to the exact phrase the
#   operator types for that step (CUTOVER_GO="GO <step>"), on CUTOVER_HOST.
# - Logs to $CUTOVER_DIR/logs/<step>-<ts>.log (0600); env values, DSNs and
#   tokens are never printed (output is filtered).
set -euo pipefail
: "${CUTOVER_HOST:=kraken}"
: "${CUTOVER_DIR:=/srv/cutover-pg-20261115}"
: "${BIN_DIR:=$CUTOVER_DIR/bin}"
TS=$(date +%Y%m%dT%H%M%S)
STEP=$(basename "$0" .sh)
EXECUTE=0
for a in "$@"; do [[ "$a" == "--execute" ]] && EXECUTE=1; done
mkdir -p "$CUTOVER_DIR/logs" 2>/dev/null || CUTOVER_DIR=/tmp/cutover-dryrun
mkdir -p "$CUTOVER_DIR/logs"
LOG="$CUTOVER_DIR/logs/$STEP-$TS.log"
touch "$LOG" && chmod 600 "$LOG"
exec > >(sed -u -E 's#(postgres(ql)?://|mysql://)[^ ]*#\1***#g; s#(PASSWORD|TOKEN|SECRET|KEY|DSN)=[^ ]*#\1=***#Ig' | tee -a "$LOG") 2>&1

say() { printf '[%s] %s\n' "$(date +%H:%M:%S)" "$*"; }
pass() { say "PASS $*"; }
failc() { say "FAIL $*"; FAILS=$((${FAILS:-0} + 1)); }
die() { say "STOP $*"; exit 1; }

# guard "<GO phrase>": host + explicit GO for --execute.
guard() {
	local want="$1"
	if [[ "$(hostname -s)" != "$CUTOVER_HOST" ]]; then
		[[ $EXECUTE == 1 ]] && die "wrong host $(hostname -s) (expected $CUTOVER_HOST)"
		say "dry-run on $(hostname -s) (not $CUTOVER_HOST)"
	fi
	if [[ $EXECUTE == 1 ]]; then
		[[ "${CUTOVER_GO:-}" == "$want" ]] || die "--execute needs CUTOVER_GO=\"$want\" typed by the operator"
		say "EXECUTE mode ($want) log=$LOG"
	else
		say "DRY-RUN: commands are printed only (add --execute and CUTOVER_GO=\"$want\")"
	fi
}

# run: print (dry-run) or execute a mutating command.
run() {
	say "+ $*"
	[[ $EXECUTE == 1 ]] || return 0
	"$@"
}

# env_names FILE: variable NAMES of an env file (never values).
env_names() { sed -nE 's/^[[:space:]]*(export[[:space:]]+)?([A-Za-z_][A-Za-z0-9_]*)=.*/\2/p' "$1" | sort -u; }

# container_env_names NAME: env var NAMES of a container (never values).
container_env_names() { docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "$1" | sed -E 's/=.*//' | sort -u; }

CONTAINERS_STOP_ORDER="newvillacarmen-laya-heads-prod newvillacarmen-backoffice-prod newvillacarmen-bot-pipeline-prod newvillacarmen-backend-prod"
CONTAINERS_START_ORDER="newvillacarmen-bot-pipeline-prod newvillacarmen-backoffice-prod newvillacarmen-laya-heads-prod"
