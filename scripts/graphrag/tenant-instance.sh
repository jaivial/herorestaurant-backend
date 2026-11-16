#!/usr/bin/env bash
# graphrag tenant-instance: one neo4j-rust process per restaurant (isolation boundary).
# Usage: tenant-instance.sh {create|start|stop|status|backup} <restaurant_id>
# Never prints tokens. Loopback only. Coordination id: graphrag_tenant_instance_v1
set -euo pipefail
cmd="${1:-}"; rid="${2:-}"
[[ "$rid" =~ ^[1-9][0-9]{0,6}$ ]] || { echo "restaurant_id required (positive integer)" >&2; exit 2; }
BIN="${NEO4J_RUST_BIN:-$HOME/neo4j-rust/target/release/neo4j-rust}"
ROOT="$HOME/graphrag"; DIR="$ROOT/$rid"; REG="$ROOT/registry.tsv"
UNIT="graphrag-$rid.service"; UNIT_DIR="$HOME/.config/systemd/user"
mkdir -p "$ROOT"; chmod 700 "$ROOT"; touch "$REG"; chmod 600 "$REG"

reg_line() { awk -F'\t' -v r="$rid" '$1==r' "$REG"; }

case "$cmd" in
create)
  [[ -z "$(reg_line)" ]] || { echo "tenant $rid already registered" >&2; exit 1; }
  # ports derived deterministically and checked unique in the registry
  http=$((20000 + rid * 2)); bolt=$((http + 1))
  (( bolt < 65536 )) || { echo "port out of range" >&2; exit 1; }
  if awk -F'\t' -v h="$http" -v b="$bolt" '$2==h||$3==b||$2==b||$3==h{f=1}END{exit !f}' "$REG"; then echo "port clash" >&2; exit 1; fi
  mkdir -p "$DIR/data" "$DIR/backups"; chmod 700 "$DIR" "$DIR/data" "$DIR/backups"
  ( umask 077; head -c 32 /dev/urandom | base64 | tr -d '\n=' > "$DIR/token" ); chmod 600 "$DIR/token"
  mkdir -p "$UNIT_DIR"
  cat > "$UNIT_DIR/$UNIT" <<EOF
[Unit]
Description=GraphRAG neo4j-rust tenant $rid (loopback only)

[Service]
Environment=NEO4J_RUST_TOKEN_FILE=$DIR/token
ExecStart=$BIN serve-bolt --data $DIR/data --bind 127.0.0.1:$bolt --http 127.0.0.1:$http
CPUQuota=200%
MemoryMax=2G
Restart=on-failure
NoNewPrivileges=yes
PrivateTmp=yes

[Install]
WantedBy=default.target
EOF
  printf '%s\t%s\t%s\t%s\n' "$rid" "$http" "$bolt" "$DIR/token" >> "$REG"
  systemctl --user daemon-reload
  echo "created tenant $rid http=127.0.0.1:$http bolt=127.0.0.1:$bolt (token file 0600, not printed)";;
start|stop|status)
  [[ -n "$(reg_line)" ]] || { echo "tenant $rid not registered" >&2; exit 1; }
  systemctl --user "$cmd" "$UNIT";;
backup)
  [[ -n "$(reg_line)" ]] || { echo "tenant $rid not registered" >&2; exit 1; }
  # consistent copy: stop, tar data (token excluded), restart if it was running
  was=0; systemctl --user is-active --quiet "$UNIT" && was=1
  (( was )) && systemctl --user stop "$UNIT"
  out="$DIR/backups/graph-$rid-$(date +%Y%m%dT%H%M%S).tar.gz"
  ( umask 077; tar -C "$DIR" -czf "$out" data )
  (( was )) && systemctl --user start "$UNIT"
  echo "backup $out";;
*) echo "usage: $0 {create|start|stop|status|backup} <restaurant_id>" >&2; exit 2;;
esac
