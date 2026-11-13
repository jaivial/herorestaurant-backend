# result-repair-vitess9

Model: claude-merge/claude-opus-5-5-acc2 (low).
- Source and build only. No tests or Playwright, no SQL writes, no activation, no network.
- go.mod and go.sum are unchanged.
- `go build -p 2 ./...` OK.
- A stray `wk-backfill` binary that `go build ./cmd/wk-backfill` dropped in the worktree root was moved to /tmp/villacarmen-bot-20261111/.

Input: on scratch, the new on_update_ts was applied and committed at 13:24, and pad-gate-check `onupdate_triggers` PASSES. The shadow-FK gate still fails, as expected.

## 1. wk-backfill trigger refusal relaxed (cmd/wk-backfill)
- Tables WITH `onupd%` triggers are now allowed only if `appdb.RequireOnUpdateTriggers` passes. That is the same read-only gate the server uses: the exact prosrc sha256 of on_update_ts and on_update_mark against the planner (new on_update_ts = fb5d4306…, which ignores `__wk`), plus the full trigger catalog.
- If the catalog differs: "refused: ON UPDATE catalog differs from planner". Tables without triggers behave as before.
- All other guards are unchanged: dry-run by default; `-apply` plus `-target-db` matching the connected database and not containing "prod"; per-row text guard; resumable.

## 2. Writer wiring batch (VC_PG_WK_KEYS; flag off = identical SQL and args)
**campaign_suppressions (channel, target), all 6 PG inserts:**
- campaign_bounce_sync.go: the bounce upsert and the pending_verify insert. These are `INSERT … SELECT`, so the keys are inserted before `FROM bookings` and the args at position 3;
- campaign_email_validation.go;
- public_unsubscribe.go;
- whatsapp_bot_unsubscribe.go;
- whatsapp_unreachable.go.

How they are wired:
- The `wkPG` variants add `channel__wk, target__wk` and use `ON CONFLICT (restaurant_id, channel__wk, target__wk)`.
- The variants were generated mechanically with asserted patterns (the exact ON CONFLICT and column-list text), so every site is the same shape.
- Helper `wkSupArgs(at, channel, target, args…)`: a key error returns nil args, so the statement fails and nothing is stored without keys.
- The key values are the Go-side channel and target that are bound, or the SQL literal 'email'/'whatsapp'. For the SELECT form, target is the bound `ev.Address`, the same value as `CAST(? AS text)`.

Coverage: 4 of 149 columns are now wired on every known PG insert:
- restaurant_domains.domain
- restaurant_uazapi_instances.instance_name
- campaign_suppressions.channel
- campaign_suppressions.target

Updates of these columns: none found in the triage.

**Not done this batch:**
- site_builder_domain_mappings (domain plus id PK; the inserts at instatic_manager.go:948, site_builder_registrar.go:303 and site_builder.go:1741 need a 2-key variant);
- restaurant_areas and restaurant_tables (premium.go:2161-2486, 9+ variants, plus the dynamic UPDATEs at 2675-2723);
- comida slugs (5 sites);
- analytics keys (3).

These are listed in the triage file.

## 3. LIKE sites (same flag)
**Wired: backoffice_invoices.go list search.**
- A prefix pattern `search%` on invoice_number, customer_email, customer_name, or name OR email by default; all utf8mb4_unicode_ci PAD.
- It produces an id set, then `AND id = ANY(?)`, so COUNT, ORDER and LIMIT stay in SQL.
- ErrTooBroad returns 422 "Search too broad, please refine the filters".

LIKE sites wired in total: admin_postres, stock list, comida postres, invoices list (4).

**Not done:**
- assistant catalog (per-table candidate builder needed);
- invoices 782/786 (the other handler);
- POS tickets 689, customers 105/112, reservations 31;
- checkcancel 126/139 (`LOWER(TRIM(customer_name))`, which needs TRIM-then-LOWER on the Go side before Like);
- dish allergens, AI usage, technical sheet, bookings and bookings search, stock options, group menus v2, comida 818/1111, group_menus.go:940.

## 4. The 133 "unknown" triage rows: docs/dbmigrate/wk-writer-triage-vitess9.tsv
The vitess8 TSV plus a `manual_class_vitess9` column. **This was a rule-assisted reading, not a full manual review.** I read the printed context of all 133 and coded the rules:

| class | touches key | no key |
|---|---|---|
| pg (second argument of nativeSQL/sqlFragment) | 12 | 15 |
| mysql (first argument) | 14 | 21 |
| native pair on one line (mysql + pg) | 11 | 24 |
| dynamic builder (strings.Join SET lists) | 0 | 8 |
| variable text (dialect decided by the caller) | 0 | 5 |
| false positive (a continuation line or other line) | 7 | 5 |
| needs reading | 1 | 10 |

Total 133.
- The dynamic builders (site_builder sites, pages and bindings, restaurants, bo_users, uazapi_servers, POSTRES) need column-aware wiring: a `__wk` SET item whenever a registered column is in the SET list.
- The "touches key" flag is still lexical. Those rows need confirmation per site.

## Inventory r116
2532 sites: 2511 native, unchanged.

## Gates
- `__wk`: DDL unapplied on scratch; writers 4 of 149 columns; LIKE 4 sites.
- Dynamic builders; sort parity; provisional bounds.
- GPL/provenance still open.
- Shadow FK (orphans).
- PG disabled, checkout 503, 0 handler QA, 1117 pending, 731 orphans, R5 untouched.
