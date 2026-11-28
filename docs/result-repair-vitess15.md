# result-repair-vitess15

Model: claude-merge/claude-opus-5-5-acc2 (low).
- Source and build only. No tests or Playwright, no SQL writes, no activation, no network.
- go.mod and go.sum are unchanged.
- `go build -p 2 ./...` OK with /home/linuxbrew/.linuxbrew/bin/go.
- Backups: /tmp/v15-pre.bak.tar and /tmp/v15-lk.bak.tar.
- backoffice_stock_documents.go:680 stays documented and unfixed (main decision).

## 1. LIKE sites (flag VC_PG_WK_KEYS; off = identical SQL and args)
**Wired this batch: 7 sites.**
- comida listCatalogItems (comida_items nombre, descripcion, titulo; unicode_ci).
- comida wines list (group_menu_wines_v2, **utf8mb4_0900_ai_ci NO PAD**, per the schema).
- group menus v2 dish search (nombre and titulo; keeps `ORDER BY updated_at DESC, id DESC LIMIT ?`).
- bookings general search (handleBOBookingsSearch):
  - new helper `wkBookingSearchIDs`: (customer_name OR contact_email LIKE %name%) OR (contact_phone LIKE digits%), all under **utf8mb4_general_ci**;
  - the clause becomes `id = ANY(?)`; pagination and count stay in SQL.
- bookings list prefix search (backoffice_bookings.go): the same helper with a `q%` prefix. On MySQL the FULLTEXT branch is unchanged; on PG the flag path replaces the prefix clause.
- invoices bookings lookup (782/786): name and phone each AND-ed, as an id set per filter.
- POS reservations: name OR phone over that restaurant and date; `(?='' OR b.id = ANY(?))`.

Each wired site returns 422 "Search too broad" or a 500 on error, in the handler's existing style. Total LIKE sites wired: 15.

**Not wired, with reasons:**
- **assistant catalog:** `assistantLikeClause` is a dynamic multi-table builder with no DB access. It needs a per-table candidate query.
- **technical sheet:** `sheetListFrom` is a pure FROM/WHERE builder shared by list, count and WS search. It needs a signature change to receive an id set.
- **AI usage:** whatsapp_message_logs `content` is mediumtext in a time window. The candidate volume is likely above the provisional bound, so a measured bound is needed first; the query also runs on the bot store queryer.
- **group_menus.go:940:** the LEFT JOIN rewrite into two queries with `TitleJoin` is a larger restructuring of the row assembly.

## 2. POS ticket insert (assistant_pos_tools.go:72)
- Wired. After the PG UUID append, the args are `[rid, visit, ticket_number, key, uid]`, and `wkKeyArgs(-1, …, {ticket_number, creation_idempotency_key})` appends both keys.
- The PG flag text adds `ticket_number__wk` and `creation_idempotency_key__wk`.

## 3. Lookups (72 remaining)
- Converter extended: alias-qualified columns, `::text`, and several comparisons per statement, with table resolution through FROM/JOIN aliases.
- **Converted: 7.** bo_sessions.token_sha256, mcp_oauth_tokens.token_hash, chatgpt_plugin_tokens.token_hash, stock_supplier_aliases.supplier_name, bo_password_reset_tokens.token_sha256, bo_member_invitation_tokens (onboarding_guid, token_sha256).
- **Still remaining: 63 rows in the file** (72 minus 7 converted; 2 duplicate line entries merged): 36 retried and skipped, plus 27 not retried:

| reason | rows |
|---|---|
| query in a variable or call form | 27 |
| other comparison form (pattern not found) | 12 |
| inside an already-flagged writer text | 19 |
| unresolved `id` or `user_phone` table | 5 |
| JOIN or table not unique | 1 |
| slice args | 1 |

- File: `docs/dbmigrate/wk-lookups-remaining-vitess15.tsv`.

## 4. stock_items.name / stock_recipes.name sort keys: NOT done
- Wiring the 13 writers needs adding a second key to statements that are already flagged (the generators refuse "already wired"), plus a dynamic SET builder.
- Registering the columns before every writer is wired would make the startup guard and CheckStoredKeys fail.
- ORDER BY stays on the raw column; proposal unchanged (wk-sortkeys-proposal-vitess14.sql).

## Inventory r122
2534 sites, 2513 native, unchanged.

## Gates
**The flag must stay off.** Reasons:
- 4 LIKE sites;
- 63 lookups;
- the sort keys;
- review of the auto-wired writers.

Also open:
- stock document insert bug (documented, not fixed);
- GPL/provenance;
- shadow FK;
- PG disabled, checkout 503, 0 handler QA, 1117 pending, 731 orphans, R5 untouched.
