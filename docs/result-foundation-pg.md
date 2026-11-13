# Native PostgreSQL foundation — checkpoint (sqldb_native_pg_v1)

Status: **built (every go build exit 0), not activated.** There was no cutover, deploy, commit or send. The default runtime is unchanged (MySQL business DB, SQLite bot context). The staging PG `newvillacarmen_business` stays `ready:false`.

## Changes

| file | change |
|---|---|
| `internal/sqldb/sqldb.go` (new) | explicit dialect (no SQL transpiler); `OpenPostgres(dsn, schema)` pins `search_path` per connection (`public` business vs `bot_context`); `Rebind`; `InsertID` (PG `RETURNING` / MySQL `LastInsertId`); `IsUndefinedObject` (42P01/42703, 1146/1054); `IsUniqueViolation` (23505/1062); `TxAdvisoryLock` (replaces `GET_LOCK`) |
| `internal/botstore/botstore.go` | `OpenSchema`: PG context store in schema `bot_context` of the unified DB (schema created, `search_path` pinned). In schema mode, only the SQLite-context tables are created; the superseded 5-table MySQL subset is not, because the full import owns those in `public`. Schema mode requires only the context marker. `Open` with no schema is unchanged. |
| `internal/config/config.go` | `BOT_CONTEXT_POSTGRES_SCHEMA`, `DB_DRIVER` (default `mysql`), `DATABASE_URL` (placeholder; not used yet) |
| `internal/api/whatsapp_bot_context_sqlite.go`, `cmd/botstore-migrate/main.go` | honour `BOT_CONTEXT_POSTGRES_SCHEMA` |
| `cmd/server/main.go` | **fails closed** on `DB_DRIVER≠mysql`: the business SQL is still MySQL-native, so starting on PG is refused instead of failing per query |
| `internal/api/backoffice_premium.go` | `isSQLSchemaError` also classifies by SQLSTATE / MySQL code, so its 63 callers stay correct on PG. MySQL message matching is unchanged. |
| `internal/api/assistant_ws.go` | NUL adapter: `assistant_messages.content` is bound as `[]byte` (2 INSERTs) and scanned into `[]byte` (history). Bytes are exact on both dialects (bytea on PG, the same mediumtext bytes on MySQL). These are all 3 callers of the table. |
| `internal/dbmigrate/pgimport.go` | `verify` now reads `pending_blocking_decisions` and `ready` from the `dbmigrate.marker` row (it no longer reports 0), and lists the not-valid FKs by their real PG names from `pg_constraint` |

Binaries (static, CGO_ENABLED=0), sha256:
```
4451a32390c8191b0216777c82c25687c46a7bb34170d4e0634b71914a774fa7  /tmp/villacarmen-bot-20261111/dbmigrate-r5
cc7b8344dccf201e515f55d0424b8dc1514dd1b36f9140f9ee225b54781616ca  /tmp/villacarmen-bot-20261111/botstore-migrate-r5
b4963d02d4263f1db4d1dd8eefa8d929c88334970a54ffa5a24ba9387637bbfa  /tmp/villacarmen-bot-20261111/server-r5
f3a3095e415ee0ed68dcf0cbe2f3ab134777338b6aa0e20b64886bcba966dfec  docs/dbmigrate/remaining-callers.json
```

## Context store into the unified DB (coordinator commands)

The context store is copied with the already-verified tool, from the same SQLite source, into schema `bot_context`. The DB must have no `bot_context.*` tables yet; `copy` refuses non-empty targets.
```
docker cp /tmp/villacarmen-bot-20261111/botstore-migrate-r5 $P:/tmp/
R() { docker exec -u postgres -e BOT_CONTEXT_POSTGRES_SCHEMA=bot_context -e BOT_CONTEXT_POSTGRES_DSN='host=/var/run/postgresql user=postgres dbname=newvillacarmen_business sslmode=disable' "$@"; }
R $P /tmp/botstore-migrate-r5 -mode copy     -sqlite /tmp/src.sqlite > ctx-copy-unified.json; echo $?
R $P /tmp/botstore-migrate-r5 -mode verify   -sqlite /tmp/src.sqlite > ctx-verify-unified.json; echo $?
R $P /tmp/botstore-migrate-r5 -mode rollback -sqlite /tmp/ctx-rollback-unified.sqlite > ctx-rollback-unified.json; echo $?
R $P /tmp/botstore-migrate-r5 -mode compare-sqlite -sqlite /tmp/src.sqlite -sqlite2 /tmp/ctx-rollback-unified.sqlite; echo $?
docker exec -u postgres -e 'PG_DSN=host=/var/run/postgresql user=postgres dbname=newvillacarmen_business sslmode=disable' $P /tmp/dbmigrate-r5 -mode verify -dir /tmp/vc-export -plan /tmp/vc-plan-r4   # business proof unchanged
```

The digests should equal those in `migration-verify.json` (separate `villacarmen_bot` DB, which stays as it is). The business `public.conversation_messages` (6 rows) and `bot_context.conversation_messages` (2056 rows) no longer collide.

## Remaining native-port callers (exact list: `docs/dbmigrate/remaining-callers.json`)

2472 call sites in 278 files (2144 static, 328 dynamic), go/ast. Each file has its domain, site count, dynamic count and line numbers. By domain (sites / files / dynamic):

| order | domain | sites | files | dynamic |
|---|---|---|---|---|
| 1 | core_backoffice (auth, sessions, restaurants, config) | 403 | 63 | 27 |
| 2 | bookings (bookings, special dates, floors, capacity) | 160 | 24 | 12 |
| 3 | catalog_stock (menus, wines, dishes, stock) | 506 | 43 | 90 |
| 4 | campaigns_messaging (campaigns, outbox) | 64 | 7 | 5 |
| 5 | pos_billing (POS, cash, invoices, Stripe, premium) | 564 | 35 | 53 |
| 6 | members_staff (fichaje, schedules, payroll) | 84 | 7 | 1 |
| 7 | whatsapp_bot (incl. proposals/op_events; absorbs botstore PG paths) | 315 | 53 | 52 |
| 8 | assistant (assistant_messages adapter done) | 55 | 8 | 22 |
| 9 | site_analytics | 200 | 17 | 21 |
| 10 | tools_cmd | 57 | 12 | 9 |
| — | db_runner (migration runner → PG baseline + `schema_migrations` map) | 5 | 2 | 2 |
| — | migration_tooling_native (already dialect-native) | 59 | 7 | 34 |

Per-file method for each domain:
1. Rewrite each statement as native PG with `sqldb` helpers. Use `InsertID` for every `LastInsertId` and `ON CONFLICT` with explicit targets for upserts.
2. Review each `RowsAffected`, time zone, `GROUP BY` (prod has no ONLY_FULL_GROUP_BY), enum ORDER BY, and collation/LIKE site.
3. Keep the existing transaction boundaries.

A domain is done only when all its files are native. No file keeps a MySQL path while claiming to work on PG. The server refuses PG until every domain is done.

## Unresolved, cutover-blocking (staging marker: 1117 pending, ready:false)

- **Collation, 774 columns:** CI/accent-insensitive ICU collation; PG17 has no LIKE on it, so the search paths need `lower/unaccent` on "C".
- **PAD SPACE, 771 columns:** this needs a source-grounded normalization contract.
- **Range, 190 columns:** identity-linked bigint unsigned columns are narrowed to 2^63-1.
- **ON UPDATE, 136 tables:** an UPDATE that explicitly sets the timestamp to its current value behaves differently.
- **View:** `recent_conversations` needs an activation review (broken at source with error 1356).
- **Storage:** the `assistant_messages.content` app adapter is now implemented. The pending entry is cleared only by the coordinator after QA.
- **Historical orphans:** 4 FKs are NOT VALID, covering 731 rows. This needs a production integrity decision from the user.
- None of these are waived.

