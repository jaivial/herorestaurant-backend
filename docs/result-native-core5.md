# Native PG port: core group 5 checkpoint (partial)

## What's done
- Build: `go build -p 2 ./...` exits 0, run under the inherited cgroup (CPU 200%, 6G). Nothing was runtime-verified: no SQL execution, no tests, no vet.
- The labour overlap date bind now compares `CAST(effective_from AS timestamp)` with `CAST(? AS timestamptz) AT TIME ZONE 'UTC'`. This keeps MySQL's datetime-vs-param comparison for Madrid-midnight values bound as UTC, so non-midnight times are not truncated. It compiles. Not executed.
- 11 sites converted to native PG in 8 files:
  - **salon_condesa (2):** upsert is `ON CONFLICT (restaurant_id, date)`, matching uniq_salon_condesa_restaurant_date.
  - **opening_hours (2):** upsert is `ON CONFLICT (restaurant_id, dateselected)`, matching uniq_openinghours_restaurant_date. The DELETE is portable.
  - **mobility_config (2):** `"reservationDate"` is quoted. Upsert is `ON CONFLICT (restaurant_id, "reservationDate")`.
  - **guest_count_flags (1)** and **marketing_advertising (1):** portable. The DISTINCT runs over CI columns, which depends on the planned ICU nondeterministic collation (CI ledger, pending).
  - **checkcancel (1):** camelCase columns are quoted. PG17 does not allow LIKE on nondeterministic collations, so the column gets an explicit `COLLATE "C"` after LOWER(TRIM()). This changes accent-insensitivity compared with MySQL general_ci. **Pending, recorded as a CI/accent delta.**
  - **modification_checker (1):** CONCAT becomes `||` with the same COALESCE. `reservation_date >= NOW()` keeps the MySQL date-as-midnight-datetime comparison: `CAST(reservation_date AS timestamp) >= now() AT TIME ZONE 'UTC'`.
  - **rice_types (1):** IF/FORMAT become CASE and `to_char(ROUND(x,0),'FM999,999,990')`. ROUND rounds half away from zero, and decimal(10,2) fits within the mask. MySQL FORMAT locale is en_US by default. **Pending manual QA** of the rendered strings.
- Ledger evidence:
  - Inventory r10: `/tmp/villacarmen-bot-20261111/sql-inventory-r10.json`. Repo has 2476 sites, 351 native, 308 dynamic, portable_audit_failures 0.
  - Exact core remainder: `/tmp/villacarmen-bot-20261111/core5-remaining-r10.json`.

## Remaining core: 54 sites
| file | sites |
|---|---|
| modifications_api | 12 |
| ad_tools | 8 |
| events | 6 |
| beverage_options | 6 |
| backoffice_accounting_exports | 5 |
| public_unsubscribe | 5 |
| n8n_reminder | 5 |
| modification_history | 4 |
| widget_routes | 2 |
| server.go (wines catalog fragments) | 1 |

Core SQL is therefore **not** fully ported yet. Known issues per file:
- **modification_history:** contains MySQL runtime DDL. Decide whether PG skips it because the migration schema already exists.
- **public_unsubscribe** and **backoffice_accounting_exports:** contain INSERT IGNORE. Needs the FK/width/IGNORE-delta handling.
- **server.go:** wine fragments need review.

## Open semantics (unchanged, none waived)
- CI/accent/PAD SPACE, including the new checkcancel LIKE delta.
- 190 range narrowings.
- ON UPDATE gap on 136 tables, which affects these upserts' updated_at.
- View recent_conversations.
- Storage adapter QA.
- 4 NOT VALID FKs covering 731 orphans.
- 1117 pending, ready:false.
- Stripe CAST AS UNSIGNED and JSON amount render.
- div_precision_increment: only the restored container's default (4) is known, not production.
- FOR KEY SHARE EXISTS race review.

No DB writes, activation, providers, sends, commits or deploys. The server still refuses PG.

## Next
1. Finish the 54 core sites above.
2. Then the bookings domain (160 sites in 24 files).

