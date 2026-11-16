# result-repair-vitess23

Model: claude-merge/claude-opus-5-5-acc2 (low).
- Read-only analysis. No code changed and no build needed.
- No SQL run, no network. go.mod unchanged. backoffice_stock_documents.go:680 is untouched.

## 1. Real routes and gates (internal/api/server.go, all under /api/admin)
| route | gate chain |
|---|---|
| GET /stock/items, GET /stock/item-options (:405/406) | requireBOSession → stockViewGate = requireBOStockPermission(view) |
| GET/POST /stock/categories, PATCH/DELETE /stock/categories/{id} (:401-404) | view / stockItemsGate (items_manage) |
| GET/POST /stock/suppliers, PATCH/DELETE /stock/suppliers/{id}, PUT /stock/suppliers/{id}/aliases (:422-427) | view / items_manage |
| GET /comida/technical-sheets (:452) | sheetsViewGate = requireBOStockPermission(sheets_view) |
| GET/POST /pos/customers (:607/608) | requireBOSession → requireBOPOSFeature → posSellGate = requireBOPOSPermission(sell) |
| GET/POST/PUT /tables, DELETE /tables/{id} (:986-989) | reservasGate (section). Areas go through the same route with `entity` = "area"; the default entity is "table" (backoffice_premium.go:1631). |
| GET/POST /comida/platos/categorias (:377/378) | menusGate (section) |

The routes in vitess22 were correct.

## 2. Why the 403s happen
`requireBOStockPermission` (backoffice_stock.go:111) and `requireBOPOSPermission` (backoffice_pos.go:85) **first** apply an app-version gate:
`appCapabilityAllowed(stock|pos, a.User.AppVersion)`, which requires **≥ 0.2** (bo_app_version.go:65-66).

`AppVersion` comes from `bo_user_restaurants.app_version` for the active restaurant (bo_app_version.go:170). It is **not** affected by is_superadmin, and it defaults to **0.1**: that is the column default, and also the value when no row exists.
- The QA seed created the membership with the default, so app_version = 0.1.
- So stock items, item options, technical sheets (stock capability) and POS customers return 403 **before** any permission lookup, even for a superadmin.
- `X-Restaurant-Id` does not change it (the session's active restaurant is used).

Behind that gate:
- **Stock and sheets:** an explicit `stock_role_permissions` row for (restaurant, role, key) decides. With no row, root/admin are allowed.
- **POS:** the same with `pos_role_permissions`. `requireBOPOSFeature` additionally needs a recurring_invoices 'pos' feature, except for role root. A superadmin is root, so it passes.

**What would satisfy the gates on scratch:** `bo_user_restaurants.app_version` ≥ '0.2' (e.g. '0.4') for user 207 and restaurant 1, and no explicit deny rows for role 'root'.
- That is a scratch write: a one-row UPDATE, under the GO.
- Alternatively, re-seed with an app_version option. Not added; tell me if you want qa-seed-scratch to take `-app-version`.
- Then log in again: app_version is loaded at login and cached in the session.

Read-only check file: `docs/dbmigrate/qa-gates-check-vitess23.sql` (not run).

## 3. Corrected QA lines
The paths are unchanged from vitess22. After the app_version fix and a fresh login (`C="--header=Cookie: bo_session=…"`):
```
wget -q -O - "$C" "http://127.0.0.1:18080/api/admin/stock/items?q=$q"
wget -q -O - "$C" "http://127.0.0.1:18080/api/admin/stock/item-options?q=$q"
wget -q -O - "$C" "http://127.0.0.1:18080/api/admin/comida/technical-sheets?q=$q"
wget -q -O - "$C" "http://127.0.0.1:18080/api/admin/pos/customers?q=$q"
wget -q -O - "$C" "http://127.0.0.1:18080/api/admin/stock/categories"
wget -q -O - "$C" "http://127.0.0.1:18080/api/admin/stock/suppliers"
wget -q -O - "$C" "http://127.0.0.1:18080/api/admin/tables"
wget -q -O - "$C" "http://127.0.0.1:18080/api/admin/comida/platos/categorias"
```
Writes:
```
wget -q -O - "$C" --header='Content-Type: application/json' --post-data='{"name":"QA Cáfe ß","sortOrder":0}' http://127.0.0.1:18080/api/admin/stock/categories
wget -q -O - "$C" --header='Content-Type: application/json' --post-data='{"entity":"area","name":"QA Área"}' http://127.0.0.1:18080/api/admin/tables
wget -q -O - "$C" --header='Content-Type: application/json' --post-data='{"entity":"table","area_id":<areaId>,"name":"QA Mesa","numero_mesa":"QA-1"}' http://127.0.0.1:18080/api/admin/tables
wget -q -O - "$C" --header='Content-Type: application/json' --post-data='{"name":"QA Proveedor ñ"}' http://127.0.0.1:18080/api/admin/stock/suppliers
```
- busybox wget has no PATCH, PUT or DELETE. The updates and cleanups (PATCH categories, PUT tables, DELETE …) need curl, if present in the container, or a static curl copied in.
- After each write: `PADQA_WK=1 pad-gate-check` (wk_keys PASS).

## 4. LIKE on ci_ai columns outside the wk helpers
A grep for `LIKE`, `ILIKE`, `~*` and `SIMILAR TO` over internal/ and cmd/, excluding `_test.go` and `COLLATE "C"`, found:
- **App PG texts:** none are left without an explicit `COLLATE "C"` (the pre-flag path) or the wk path. Every hit without "C" is a **MySQL** first argument or a comment:
  - campaign_auto_retake.go:42, public_unsubscribe.go:333, backoffice_group_menus.go:145 and campaign_email_guard.go:328 have PG twins with `COLLATE "C"`;
  - the search sites have "C" or wk twins.
- **Migrations** (internal/db/migrations): MySQL-only; no LIKE in non-test files.
- **cmd/:**
  - cdn-sweep:98-107 runs on `information_schema` (sql_identifier, deterministic): OK;
  - group-menu-arroz-cleanup:365 is the MySQL text (its PG twin uses "C");
  - wk-backfill:177 is on `pg_trigger.tgname` (type name): OK;
  - the other tools have no LIKE.
- **Background workers:** covered by the same grep (internal/api); none.

So the failure you hit (`UPDATE … WHERE email LIKE …` on a ci_ai column) is a property of ad-hoc SQL on scratch. **Manual SQL must use `col COLLATE "C" LIKE …`** or `=`. No app site is affected, as far as a lexical scan can tell; dynamic SQL built from fragments was covered by the per-fragment hits.

## Gates
Unchanged from vitess22.
