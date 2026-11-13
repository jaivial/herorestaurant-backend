# Native PostgreSQL port — core_backoffice, group 1 (auth / sessions / RBAC / tenant)

Status: **group 1 converted and built** (`go build -p 2 ./...` exit 0). It is not executed against any DB and not activated. The server still refuses `DB_DRIVER=postgres`, and MySQL stays the default with byte-identical SQL on the MySQL path.

## Mechanism (explicit per site, no transpiler)

`internal/api/sql_native.go`:
- `nativeSQL(mysql, pg)`: two audited statements. The MySQL text is unchanged. The PG text is written for that site and only its `?` placeholders are rebound.
- `portableSQL(q)`: a statement audited as valid and equivalent on both dialects.
- `sqlDialect`: set to MySQL (PG is not enabled).
- `sqldb.InsertID` replaces `LastInsertId` (PG `RETURNING id`).

The ledger (`dbmigrate inventory`) counts a site as native only when its query argument is wrapped in one of these.

## Converted: 57 sites, 9 files (15 dialect variants, 42 portable)

| file | sites | PG-specific decisions (schema-grounded) |
|---|---|---|
| `backoffice_auth.go` | 14 | `NOW()` on naive `datetime` columns (`bo_password_reset_tokens` / `bo_member_invitation_tokens` `invalidated_at`, `expires_at`) becomes `(now() AT TIME ZONE 'UTC')`; `INSERT IGNORE` becomes `ON CONFLICT DO NOTHING`; bootstrap admin insert uses `InsertID`; `bo_sessions.expires_at` argument passed as `.UTC()` |
| `backoffice_middleware.go` | 2 | `last_seen_at` (timestamp→timestamptz) uses `now()`; `expires_at` (datetime) is compared with `(now() AT TIME ZONE 'UTC')`; the moving expiry argument is `.UTC()` |
| `backoffice_preferences.go` | 5 | upserts use the real unique keys: `uq_user_pref(user_id,restaurant_id,pref_key)` and `uq_user_menu_pref(user_id,restaurant_id,menu_id,pref_key)` |
| `backoffice_rbac.go` | 4 | portable (smallint 0/1 flags) |
| `backoffice_roles.go` | 14 | upserts on `PRIMARY(role_slug,section_key)` and `PRIMARY(user_id,restaurant_id)`; the user insert uses `InsertID` |
| `backoffice_settings.go` | 4 | upserts on `PRIMARY(restaurant_id)`; the 2 JSON columns are bound with explicit `CAST(? AS jsonb)` |
| `mcp_oauth_routes.go` | 2 | `revoked_at` (timestamptz) uses `now()` |
| `mcp_oauth_store.go` | 10 | client upsert on `uq_mcp_oauth_clients_client_id`; `used_at`/`revoked_at` use `now()`; `bo_sessions.expires_at` argument is `.UTC()`; both `RowsAffected` uses update NULL → value, so changed equals matched (same result on both) |
| `tenant.go` | 2 | portable |

Adding `.UTC()` is behaviour-identical on MySQL: the driver already stores `time.Time` in UTC (no `loc` in the DSN).

## Remaining in core_backoffice: 346 sites in 54 files (27 dynamic)

The exact per-file list and lines are in `docs/dbmigrate/native-ledger-core.json`, generated from `sql-inventory-r6.json`. The largest are `backoffice_config.go` (43), `backoffice_platform.go` (33), the `backoffice_technical_sheet_*` group (~90, 13 of them dynamic), `backoffice_production_labour.go` (16), `backoffice_labour_cost.go` (12) and `modifications_api.go` (12, 2 dynamic). Suggested next groups:
- **group 2:** config and platform (`backoffice_config*`, `backoffice_platform`, `provision`, `bunny/chatgpt/minimax` configs, `bo_app_version`, `translations`, `server.go` (5, 3 dynamic));
- **group 3:** technical sheets;
- **group 4:** labour cost / production labour / hours / occupancy / calendar;
- **group 5:** the rest (modifications, events, widget, public_unsubscribe, misc).

## Semantic risks (open, not hidden)

1. **Collation (cutover-blocking):** every comparison on `_ci` columns here (emails, usernames, slugs, domains, role slugs) relies on the plan's `dbmigrate.ci_ai` ICU collation. The PAD SPACE trailing-space difference is still pending.
2. **INSERT IGNORE:** MySQL also downgrades an FK violation to a warning. PG `ON CONFLICT DO NOTHING` only skips conflicts, so a missing `restaurants.id=1` would raise on PG instead of being ignored silently. I kept this stricter behaviour deliberately.
3. **ON UPDATE:** upserts that update `updated_at` ON UPDATE columns depend on the PG trigger; the explicit-same-value gap is still pending.
4. **pgx time.Time encoding:** for naive `timestamp` columns this is neutralised by passing UTC values. Reads return UTC-located times on both dialects.
5. **Not executed:** none of the PG statements has run yet. They need a reviewed local run against the staging DB once PG start is allowed.

