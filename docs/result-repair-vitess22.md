# result-repair-vitess22

Model: claude-merge/claude-opus-5-5-acc2 (low).
- Source and build only. Nothing was run.
- No tests or Playwright, no network, no production.
- go.mod and go.sum are unchanged (golang.org/x/crypto/bcrypt was already required).
- `go build -p 2 ./...` OK.
- backoffice_stock_documents.go:680 is untouched. No secret is printed anywhere.

Input (16:53):
- vitess21 verified: bot store driver=postgres postgres=true; public and admin-token endpoints 200; the leftover sqlite file was removed.
- Parent GO covers reads, plus writes only in rollback transactions or on the disposable scratch.

## 1. cmd/qa-seed-scratch (built: /tmp/villacarmen-bot-20261111/qa-seed-scratch, static)
**How login works** (internal/api/backoffice_auth.go):
- `POST /api/admin/login` with `{"identifier","password"}` looks up `bo_users` by `LOWER(TRIM(email))` or username.
- It checks `bcrypt.CompareHashAndPassword(password_hash)`.
- Restaurants come from `bo_user_restaurants` (unless is_superadmin). The role is `bo_user_restaurants.role` (default admin), and sections come from the existing bo_roles/section rows for that role and app_version.
- It then inserts `bo_sessions` (token_sha256 + `token_sha256__wk` under the flag) and sets the `bo_session` cookie.

**What the seed creates, and nothing else, in one transaction:**
- `bo_users`: email `qa-scratch-<8 hex>@invalid.local`, username `qa-scratch-<8 hex>`, name "QA scratch", is_superadmin=0, must_change_password=0.
  - password_hash = bcrypt(DefaultCost) of a 24-byte crypto/rand password (base64url);
  - `email__wk` and `username__wk` set with `mysqlcoll.ColumnKey`, because scratch has the __wk DDL and the guard requires CheckStoredKeys = 0.
- `bo_user_restaurants`: (user, `-restaurant` [default 1], role 'admin'), app_version by column default.
- No role, permission or section rows are created; it reuses the existing 'admin' role data.

**Refusals:** it refuses unless all of these hold:
- `QA_SCRATCH_SEED=1`;
- `QA_SCRATCH_DB_NAME` equals `current_database()` and does not contain "prod";
- `DATABASE_URL` is postgres:// on a unix socket or loopback;
- the restaurant exists;
- no state file exists yet.

**Outputs:**
- Login secret: `-secret-file` (default /tmp/qa-scratch-login.txt), created O_EXCL with mode **0600**, holding `identifier=…` and `password=…`. It is never printed; stdout shows only the user id, restaurant and file path.
- State: `-state-file` (default /tmp/qa-scratch-seed.json), 0600, holding user_id, restaurant_id, email, created_at and database.

**Cleanup (`-cleanup`):** same guards. It verifies that the state matches the row (id, the email prefix qa-scratch- and the database), then in one transaction deletes, in order:
1. that user's bo_sessions;
2. the bo_user_restaurants row;
3. the bo_users row.

It prints the counts and removes the state file. Delete the login file by hand.
- If other rows reference the user (e.g. audit rows created by write flows), the bo_users delete fails and the transaction rolls back. Clean those first (§2).

Run inside the scratch container (network none):
```
docker cp /tmp/villacarmen-bot-20261111/qa-seed-scratch instinct-villacarmen-padqa-20261112:/tmp/qa-seed-scratch
docker exec instinct-villacarmen-padqa-20261112 sh -c 'env -i QA_SCRATCH_SEED=1 QA_SCRATCH_DB_NAME=<scratch_db> DATABASE_URL="postgres://<qa_user>@/<scratch_db>?host=/var/run/postgresql&sslmode=disable" /tmp/qa-seed-scratch'
```

## 2. Manual QA script (inside the container; server from vitess20/21 on 127.0.0.1:18080)
**Login.** This captures the cookie without printing the password:
```
. /tmp/qa-scratch-login.txt          # sets identifier, password in this shell only
wget -q -S -O /dev/null --header='Content-Type: application/json' \
  --post-data="{\"identifier\":\"$identifier\",\"password\":\"$password\"}" \
  http://127.0.0.1:18080/api/admin/login 2>&1 | sed -n 's/.*Set-Cookie: bo_session=\([^;]*\).*/\1/p' > /tmp/qa-cookie && chmod 600 /tmp/qa-cookie
unset password; C="--header=Cookie: bo_session=$(cat /tmp/qa-cookie)"
```
- The login writes one bo_sessions row (removed by -cleanup or logout).
- Every authenticated request updates bo_sessions.last_seen_at, so even reads write to scratch.

**Reads:** run each twice, plain vs accented (cafe/café, jose/josé, nino/niño). Record the counts and compare them with the MySQL source for the same terms. Expectation with VC_PG_WK_KEYS=1: equal counts under unicode_ci and general_ci.
```
for q in cafe caf%C3%A9; do
  wget -q -O - "$C" "http://127.0.0.1:18080/api/admin/bookings/search?name=$q"
  wget -q -O - "$C" "http://127.0.0.1:18080/api/admin/bookings?date=<YYYY-MM-DD>&q=$q"
  wget -q -O - "$C" "http://127.0.0.1:18080/api/admin/stock/items?q=$q"
  wget -q -O - "$C" "http://127.0.0.1:18080/api/admin/stock/item-options?q=$q"
  wget -q -O - "$C" "http://127.0.0.1:18080/api/admin/invoices?search=$q&search_by=name"
  wget -q -O - "$C" "http://127.0.0.1:18080/api/admin/pos/customers?q=$q"
  wget -q -O - "$C" "http://127.0.0.1:18080/api/admin/comida/technical-sheets?q=$q"
  wget -q -O - "$C" "http://127.0.0.1:18080/api/admin/comida/platos?search=$q"
  wget -q -O - "$C" "http://127.0.0.1:18080/api/admin/comida/postres?search=$q"
  wget -q -O - "$C" "http://127.0.0.1:18080/api/admin/comida/vinos?search=$q"
done
wget -q -O - "$C" http://127.0.0.1:18080/api/admin/group-menus-v2
```
- Look for 422 (too broad), 500s, and server-log SQL errors such as an unknown __wk column or a parameter count mismatch.
- A 403 means a section limit of the 'admin' role; it is not a PG error.

**Writes:** scratch only, flag on, wired writers with no outbound provider. Each write is followed by:
1. `PADQA_WK=1 pad-gate-check`, expecting business.wk_keys = PASS (CheckStoredKeys = 0 for all 151 columns);
2. a count check: GET the list again, or `pad-gate-check -wk-qa`.

| # | flow (HTTP) | wired writer | changes | cleanup |
|---|---|---|---|---|
| W1 | POST /api/admin/stock/categories {"name":"QA Cáfe ß","sortOrder":0} | stock_categories.name INSERT | 1 row | DELETE /api/admin/stock/categories/{id} |
| W2 | PATCH /api/admin/stock/categories/{id} {"name":"QA Cafe ss"} | changed-rows UPDATE ($n) | same row | as W1 |
| W3 | POST /api/admin/tables: area {"entity":"area","name":"QA Área"}, then table {"entity":"table","area_id":…,"name":"QA Mesa","numero_mesa":"QA-1"} (verify fields in handleBOPremiumTablesCreate) | restaurant_areas/tables INSERT variants | 1 area + 1 table | DELETE /api/admin/tables/{id} (table, then area) |
| W4 | PUT /api/admin/tables (rename) | tables UPDATE (name__wk COALESCE) | same table | as W3 |
| W5 | POST /api/admin/stock/suppliers {"name":"QA Proveedor ñ"}, then PUT .../{id}/aliases | stock_suppliers.name, supplier_aliases (3 keys) | supplier + aliases | DELETE /api/admin/stock/suppliers/{id} |
| W6 | POST /api/admin/pos/customers (POS feature) | pos_customers email/phone | 1 customer | no hard delete in the API; record the id (scratch is disposable) |
| W7 | POST /api/admin/comida/platos/categorias | comida_plato_categories.slug (InsertID) | 1 category | DELETE /api/admin/comida/categorias/{id} if applicable |

After each rename, the list must find the row with both the accented and the plain spelling.

**Excluded writes:**
- domains/registrar, Stripe, UAZAPI, campaigns and email (providers);
- fiscal documents and POS checkout (money);
- bookings with notifications (outbox);
- the stock-documents insert (known bug, decision).

**Cleanup order:**
1. delete the W-rows through their DELETE endpoints (soft-deleted rows stay; scratch is disposable);
2. `POST /api/admin/logout`;
3. `qa-seed-scratch -cleanup`;
4. remove /tmp/qa-scratch-login.txt and /tmp/qa-cookie.

Audit or history rows referencing the QA user block the user delete. On scratch only, remove them per table, or keep the user and record it.

## Gates
- QA is scratch-only, with documented waivers.
- About 29 approximate lookups.
- Stock document insert bug.
- GPL/provenance still open.
- 1117 pending and 731 orphans untouched.
- R5 untouched.
