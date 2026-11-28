# result-repair-vitess18

Model: claude-merge/claude-opus-5-5-acc2 (low).
- Source and build only. No tests or Playwright, no SQL writes, no activation, no network.
- go.mod and go.sum are unchanged.
- `go build -p 2 ./...` OK with /home/linuxbrew/.linuxbrew/bin/go.
- Backups: /tmp/v18-pre.bak.tar and /tmp/v18-lk.bak.tar.
- backoffice_stock_documents.go:680 is untouched.

## 1. Per-site row limits: internal/api/wk_bounds.go (one config file)
- Coordinator measurement, 15:48 (read-only on scratch): bound = 4 × the largest per-tenant candidate set.
- Technical floor 1000: tables measured at 0 rows must still serve new data.
- Bookings sites are at least 10000.

| candidate table | measured | bound |
|---|---|---|
| bookings (general search, list prefix, invoice lookup, POS reservations, checkcancel) | 2355 (38/day, checkcancel 11) | **10000** |
| stock_items | 479 | 1916 |
| group_menu_section_dishes_v2 (group_menus TitleJoin) | 308 | 1232 |
| whatsapp_message_logs (AI usage) | 677 per 31 days (70/1d, 256/7d) | 2708 |
| group_menu_wines_v2 | 42 | 1000 (floor) |
| comida_items | 9 platos | 1000 |
| invoices | 6 | 1000 |
| menus | 10 | 1000 |
| all others (POSTRES, pos_customers, stock_recipes, analytics_customers, catalog tables) | 0 or not measured | 1000 |

- All 23 `mysqlcoll.Bound(0, wkLikeMinBound)` call sites now use `wkBound(<candidate table>)`. The generic catalog uses `wkBound(spec.table)`.
- The `wkLikeMinBound` placeholder is removed.
- **Memory cap:** `wkLikeMaxBytes` = 64 MB of candidate text per scan, in all LIKE helpers. Above it the result is ErrTooBroad (422).
  - AI usage measured 164 KB over the largest 31-day window, about 240 B per row, so the worst case is 2708 × 240 B ≈ 0.65 MB.
- Re-measure after every import with docs/dbmigrate/like-bounds-measure-vitess17.sql and update this table only.

## 2. Lookups
- Converter extended to double-quoted Go strings, escaped identifiers and alias-qualified columns.
- **Converted: 20** (`docs/dbmigrate/wk-lookups-converted-vitess18.tsv`):
  - menu_visibility.menu_key ×7
  - site_builder ids ×8 (assets, bindings, domain mappings, pages, sites, versions.site_id)
  - bo_sessions.token_sha256
  - bo_users.email
  - pos_visits.open_idempotency_key
  - restaurant_domains.domain (tenant.go)
- **Left: 40** (`docs/dbmigrate/wk-lookups-remaining-vitess18.tsv`):

| reason | rows |
|---|---|
| column-to-column or other form | 16 |
| inside an already-flagged writer text | 15 |
| query in a variable or call form | 5 |
| slice args | 2 |
| unresolved table (user_phone, seo_slug) | 2 |

Notes on the 40:
- Column-to-column comparisons (e.g. `pc.name = v.tipo`, `sa.supplier_name = sup.name`) can only use keys when BOTH columns are registered with the same collation. Otherwise they correctly stay on ci_ai, as a recorded approximation.
- Lookups inside flagged writer texts need the ON variant edited with argument positions mapped through the key helpers. That is a manual follow-up.

## 3. Manual local QA plan (docs only): flag on, against scratch PG
**Hard constraint found in source.** `cmd/server/main.go:34` refuses `DB_DRIVER=postgres` with log.Fatalf ("not runtime-verified (cutover blocked)").
- The built bot/server **cannot** start on PG without changing that gate, which is not authorized.
- So handler-level QA over HTTP or WhatsApp is not possible yet.
- What CAN be done without lifting it is a manual diagnostic over the existing read-only commands, plus an optional new manual CLI described below.

**A. Isolation (all steps):**
- Run only inside the scratch PG container's network namespace, or with `unshare -rn` (no network).
- Inherited cgroup: `systemd-run --user --scope -p CPUQuota=200% -p MemoryMax=6G -- <cmd>`.
- DSN from env, never printed.
- No WhatsApp, provider, Stripe or email config present (unset; the server is not started anyway).
- Read-only session for read checks (`default_transaction_read_only=on`; the tools refuse otherwise).

**B. Readiness, already possible:**
`PADQA_WK=1 pad-gate-check` (business): `pad_key_indexes`, `onupdate_triggers` and `wk_keys` (151 columns, 134 ixwk indexes, CheckStoredKeys = 0). Expected: PASS except shadow FK (orphans).

**C. Read paths with the flag on (proposal: extend `pad-gate-check -read-qa`, a manual diagnostic, not a test):**
- set `VC_PG_WK_KEYS=1` in the diagnostic process, inside the existing READ ONLY transaction with the non-SELECT guard;
- call the real helpers for one operator-chosen restaurant:
  - `wkLikeMatchIDs` for each LIKE site's candidate query with a literal pattern (e.g. an accented and an unaccented spelling);
  - `wkBookingSearchIDs`;
  - `gmJoinComidaItems` over the existing dish rows;
  - a converted lookup with `wkKeyArg` (e.g. restaurant_domains.domain);
- print **counts only**: id-set sizes, TooBroad true/false, and the same counts from the old ci_ai path, for comparison;
- compare with MySQL source counts for the same patterns. The coordinator runs the twin SELECTs with LIKE under the real collation.

**D. Write paths:** they need scratch writes.
- Out of scope without an explicit GO.
- If granted later: scratch only, in a transaction, ROLLBACK. Call one wired writer helper per table class, then CheckStoredKeys on that table inside the same tx, and roll back.

**E. What cannot be verified with this plan:**
- full HTTP handlers, auth, sessions and the server lifecycle (refused on PG);
- concurrency and locking under the flag;
- WhatsApp inbound and outbound flows and providers (forbidden; not configured);
- external or raw-SQL writers bypassing the helpers (only detectable afterwards, through CheckStoredKeys);
- exact MySQL equality for the 40 remaining lookups (approximation);
- the GPL/provenance question.

## Inventory r125
2535 sites, 2512 native, unchanged.

## Gates
- Flag off.
- Server PG refusal (cutover blocked; unchanged).
- 40 lookups.
- Stock document insert bug.
- GPL/provenance still open.
- Shadow FK.
- 0 handler QA, 1117 pending, 731 orphans, R5 untouched.
