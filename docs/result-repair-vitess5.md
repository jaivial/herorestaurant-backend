# result-repair-vitess5

Model: claude-merge/claude-opus-5-5-acc2 (low). No writes anywhere, no tests or Playwright, no production, no activation. go.mod and go.sum are unchanged.

## Step 2 (run): BLOCKED in the worker sandbox, so there are no per-column results yet
- `docker ps` and `docker exec` fail: "permission denied … unix:///var/run/docker.sock". The worker runs with no approval escalation, so it cannot get docker access.
- Both target containers are network-none, so they have no TCP endpoint.
- The host has PG sockets at `/run/postgresql/.s.PGSQL.5432`, `/tmp/.s.PGSQL.5432` and `/tmp/biz143-pg/.s.PGSQL.55439`. These belong to OTHER host servers, not the scratch container, and were deliberately NOT touched: wrong target and a production risk.
- **Mismatch counts: none observed. Nothing has been compared.**

## Step 1 (cols.txt): generated from recorded metadata, not a live information_schema read
- File: `/tmp/villacarmen-bot-20261111/collkey-cols-vitess5.txt` (140 lines). Unresolved list: `…-vitess5-unresolved.txt` (9 lines).
- Sources:
  - `internal/db/padkeys.go`: 134 required indexes (116 unique + 18 PK supplementary);
  - `full-export/schema.json`: column collation and PRIMARY columns;
  - `docs/dbmigrate/collation-metadata-local-20261112.json`: the coordinator's 07:16 read-only COLLATIONS PAD_ATTRIBUTE.
- These are real recorded metadata, but not re-read live. The coordinator should re-check against live `information_schema.COLUMNS` before trusting the results.
- 149 distinct PAD columns, split as:
  - 140 runnable: utf8mb4_unicode_ci PAD = 138, utf8mb4_general_ci PAD = 2. The tenant column `restaurant_id` is set where it is in the key.
  - 9 unresolved, because the tool supports only a single-column PK:
    - whatsapp_bot_sessions.user_phone
    - bo_role_permissions.role_slug and .section_key
    - pos_daily_sequences.sequence_type
    - pos_role_permissions.role_slug and .permission_key
    - restaurant_ai_provider_keys.provider
    - stock_role_permissions.role_slug and .permission_key
  - These need composite-PK support, or to be compared by the full key tuple.

## Tool change for split namespaces (code, build OK)
The two containers cannot share one process: both are network-none and live in separate namespaces. `collkey-compare` now has two passes:
- **MySQL-only pass** (`CKC_MYSQL_DSN` only): all key counts as before, plus `raw_set_digest`.
- **PG-only pass** (`CKC_PG_DSN` only): pg_rows, nulls and `raw_set_digest`.
- `raw_set_digest` = sha256 over the sorted per-row sha256(pk ‖ 0x00 ‖ value bytes). It is one aggregate per column; no per-row value or hash is printed. Equal digests mean the raw bytes match; a difference gives no row-level count. The combined mode still gives counts.
- Caveat: PG `col::text` drops trailing spaces of CHAR(n). The listed columns should be VARCHAR/TEXT; re-check before trusting a digest for CHAR.
- Built static with CGO_ENABLED=0, so it runs inside either container: `/tmp/villacarmen-bot-20261111/collkey-compare`. The vitess4 source is backed up at /tmp/collkey-main.vitess4.bak.

## Coordinator commands (read-only; DSN from env, never echoed)
```
docker cp /tmp/villacarmen-bot-20261111/collkey-compare instinct-villacarmen-mysql-source-20261111:/tmp/ckc
docker cp /tmp/villacarmen-bot-20261111/collkey-cols-vitess5.txt instinct-villacarmen-mysql-source-20261111:/tmp/cols.txt
docker exec -e CKC_MYSQL_DSN="$MYSQL_RO_DSN_UNIX_SOCKET" instinct-villacarmen-mysql-source-20261111 /tmp/ckc -columns /tmp/cols.txt > /tmp/villacarmen-bot-20261111/ckc-mysql-vitess5.jsonl
docker cp ... instinct-villacarmen-padqa-20261112:/tmp/ckc   (same two files)
docker exec -e CKC_PG_DSN="$PG_RO_DSN_UNIX_SOCKET" instinct-villacarmen-padqa-20261112 /tmp/ckc -columns /tmp/cols.txt > /tmp/villacarmen-bot-20261111/ckc-pg-vitess5.jsonl
```
- The DSN env vars come from the operator's shell. Use a unix socket inside each container, with a read-only user if one exists. The tool refuses unless the session reports read-only.
- Copying files into a container is a scratch-container filesystem write; the coordinator must decide whether that is acceptable. The alternative is `docker exec -i … sh -c 'cat > /tmp/ckc'`, which is equivalent.
- Afterwards, compare the two `raw_set_digest` values per column. Per column, check `key_vs_mysql_weight_mismatch`, `invalid_utf8`, and `distinct_go_key` vs `distinct_mysql_eq`.

## Gates
- All unchanged.
- Collation and key parity are UNVERIFIED on stored data.
- GPL/provenance is still open.
- PG disabled, checkout 503, 0 handler QA, 1117 pending, 731 orphans, R5 untouched.
