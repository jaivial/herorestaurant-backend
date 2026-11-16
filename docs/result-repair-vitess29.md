# result-repair-vitess29 — wrong __wk key values (source/build only)

## Cause
The generator (vitess10–16, /tmp/wkgen*.py) picked the key value from the argument list by *column* position. In the INSERT texts that contain SQL literals between placeholders (`...,1,?,?,0,?` or `SELECT ?,id,?...`), the column position and the `?` position differ, so it picked a neighbouring argument. The earlier scan checked only that `__wk` text was present, not that the key value was the bound value. The insert positions (`at`) were correct at every site found below. Only the value was wrong.

## Fixes (3 real mismatches)
| file:line | column | was | now |
|---|---|---|---|
| internal/api/backoffice_pos_fiscal.go:396 | pos_fiscal_documents.full_number | nextNumber (series int, key of '1' = 0e2a) | fullNumber (the string bound to full_number) |
| internal/api/assistant_pos_tools.go:86 | pos_payments.idempotency_key | in.AmountCents | in.IdempotencyKey |
| internal/api/assistant_pos_tools.go:95 | pos_refunds.idempotency_key | mysqlUpperEnumArg(in.PaymentMethod) | in.IdempotencyKey |

At=26/5/6 confirmed by counting placeholders: 26 `?` before full_number__wk, 5 before the payments key, 6 before the refunds key. The flag-off SQL and args are unchanged.

The fiscal copy path at ~499-502 (InsertSelect duplicate) copies full_number__wk from the source row in SQL, so it stays consistent with the copied full_number. The source row's key is only as good as the original insert, which is now fixed. Rows already written with the bad key on the scratch must be recomputed with wk-backfill, or the scratch rows discarded; pad-gate-check will keep reporting key_mismatch until then.

## Audit (heuristic script /tmp/v29a.py + hand reading)
206 call sites (wkKeyArgs/wkKeyArg/wkArgs/wkSupArgs/wkSetItems in internal/api, internal/analytics, internal/bot, cmd/):
- 149 OK by script (value = expression bound at the same `?`; order and position match).
- 14 flagged MISMATCH: 3 real (fixed above). The other 11 are false positives where the key is a SQL literal mirrored in Go: pos.go:664 'TICKET'; backoffice_config.go:515/610 'global'; technical_sheet_duplicate:73 and ensure:120 'ud'; website_builder_handlers:1556 ''; site_builder.go:315 '/'. CTE/placeholder miscount cases, checked by hand and OK: accounting_exports:355 (hashText), booking_reminders:355 (key), floors_date:139 (wkScopeKeyText(date) at 4), backoffice_salons.go:320 (name at 7, then scope_key at 8; column order name__wk, scope_key__wk).
- 43 MANUAL (slice/nested/$n args), all read by hand and OK:
  - Nested inserts: pos_customers:208 (phone at 2, email at 4) and comida_categories:759 (slug at 2, food_type at 4).
  - `$n` texts with at=-1 appended in cols order: stock.go:350/473/785 (sku $10, name $11), stock_analytics:508, stock_suppliers:142.
  - Domain mappings: registrar:299 ([u,site,domain] → domain=[2], id=[0], appended) and instatic:944 ([u,sub,rid,label] → domain=[1], id=[0] at 2).
  - All premium area/table UPDATEs (nameArg at 1).
  - posChangedRowArgs(...)[0] = name at 1 for kitchen and split.
  - spArgs[0], values[0] (seo_slug), the bo_users username updates, the invitation guid, seo_item_routes slug, the checkout key at 3, the stock-exception replay key at 2, pos_products sku at 3, recipes name at 1, salons UPDATE name at 2, members email at 10, and the assistant ticket args[2]/[3].

The full per-site TSV is at /tmp/v29a-run.txt (columns: file, line, table, cols, bound expr, verdict).

## Build
- `go build -p 2 ./...` passes. It ran under the inherited cgroup (systemd-run --user is not permitted).
- go.mod is unchanged.
- sha256:
  - vc-server-qa 501b506a2b796b7e6bbd13d8eaf08a692b12d4ffdbe5eb0c9d741a06b37bfc8d
  - wk-backfill 32d87a0ba90182c26a1501c846ac6f7b6c04fe10fd276e4c90a54ef4121a27a2
  - pad-gate-check 446382dc9c55ba48e7c7b57c8b3062cdef0c5cb8b3047146ad22b7f2e9fac265
  - qa-http dac985b00d04b0f6a0babde05f7fd45c6da593be0a052b85cde440bf974b6714
  - qa-seed-scratch 8233f4edde9c342a12e0d84615781168f11af7513ff8c29aec97ad2c12dfa991

## Not verified
- Nothing has been run against a DB.
- The 149 "OK" sites rest on the heuristic script, not on reading each one by hand.
- Writers not wired through these helpers are outside this audit. Still unwired: backoffice_stock_documents.go:680 (user decision, untouched), backfill-translations:386, comida-stock-backfill:229/240.
- No HTTP QA has been done for payments, refunds or fiscal.

