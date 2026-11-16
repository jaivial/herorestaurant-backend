# pgnative-D — triage of the 72 PREPARE errors (source/build only)

Authority: Main 11:56/11:57. Worktree villacarmen-bot-pgnative-20261113 only. Nothing was run against any DB.

- **Evidence for class B:** the exported MySQL source schema (/tmp/villacarmen-bot-20261111/derived-localmeta-20261112/schema.json, information_schema of villacarmen_source). `docker exec` into instinct-villacarmen-mysql-source-20261111 was refused by the sandbox (no docker socket permission), so I did not re-query live information_schema. Re-check B with a read-only information_schema SELECT if needed.
- **Classes:**
  - A = real PG-text bug from the port, fixed.
  - B = the object is also missing in the MySQL source (pre-existing / lazily created / dead), or the statement belongs to another store or dialect; not fixed.
  - K = known and documented, not fixed.

## Fixes (A): 7 sites, 6 statements
| site | error | fix |
|---|---|---|
| backoffice_invoices.go:531, :658; backoffice_manual_billing.go:418 | cannot cast boolean to smallint | `CAST(CAST(? AS boolean) AS integer)`; the integer→smallint assignment cast applies on INSERT/UPDATE. A whole-tree grep found no other occurrence. |
| backoffice_pos_operations.go:577 | syntax error at `\` | `NULLIF(?,\'\')` → `NULLIF(?,'')`. portableSQL only rebinds, so the backslashes reached both dialects. MySQL also fails here: a backslash outside a string literal is a syntax error. Plainly broken on both, so this one portable text was fixed. No other `\'` in SQL strings (grep). |
| booking_reminders.go:296 | syntax error near "b" | The PG text used MySQL `TIMESTAMP(date, COALESCE(NULLIF(time,''),'00:00:00'))`. It is now `(b.reservation_date + COALESCE(b.reservation_time, TIME '00:00:00')) BETWEEN CAST(? AS timestamp) AND CAST(? AS timestamp)`. reservation_time is `time`, so the MySQL '' sentinel cannot occur on PG. |
| internal/analytics/service.go:753 | invalid enum value "" | `COALESCE(m.waste_reason,'')` → `COALESCE(CAST(m.waste_reason AS text),'')`. The scan target is a string, so the result is identical. |

## Not fixed
| site(s) | class | reason |
|---|---|---|
| backoffice_stock_documents.go:680 | K | known stock-insert bug, documented, user decision |
| assistant_llm.go:701 restaurants.phone | B | column absent in the MySQL source; the code comment already says both dialects fail and fall back to the base prompt |
| backoffice_bookings.go:980 closed_days | B | table absent in the source; inside TxStatementScope (error tolerated) |
| modifications_api.go:603 bookings.created_at | B | column absent in the source (comment: fails on both) |
| modifications_api.go:684, :757 modification_history.restaurant_id | B | the source table has id, booking_id, customer_phone, field_modified, old_value, new_value, modification_date only; the MySQL text has the same column |
| special_booking_snapshot.go:678 menu_dishes_catalog.name | B | column absent in the source (portable text, same on MySQL) |
| instatic_proxy.go:151 site_builder_domain_mappings.restaurant_id | B | column absent in the source; the MySQL text is identical |
| backoffice_pos.go:448; backoffice_pos_pin.go:62, :78, :182, :189, :245 restaurant_members.pos_pin_hash | B | column absent in the source (not created by any migration in the export) |
| website_builder_generator.go:82, :180; website_builder_handlers.go:305, :309, :349, :361, :444, :448, :515, :1188, :1278, :1321, :1337, :1461, :1545, :1552, :1832, :1863, :1874 (website_assets, website_publish_history, website_pages.is_homepage/meta_description) | B | tables/columns absent in the source; the MySQL texts use the same names (legacy website builder, dead on this data) |
| cmd/migrate-wine-images/main.go:113 "VINOS" | B | table absent in the source (legacy one-off tool) |
| whatsapp_bot_chat_images.go:72, :75 chat_images | B | bot context store (bot_context schema / SQLite), not public; the check ran with the public search_path |
| whatsapp_bot_context_sqlite.go:136, :152 (conversation_messages user_phone/role), :271, :281, :298, :493 pipeline_decisions, :329, :348, :351, :361 handoff_state, :381, :407, :422, :434 management_requests | B | bot context store. public.conversation_messages is the different MySQL-origin table; the bot_context one has these columns |
| whatsapp_bot_rag.go:189, :268, :287 bot_knowledge_fts; :271, :290 MATCH | B | bot context FTS. The MATCH lines are the SQLite branch (selectedSQL), never sent to PG |
| internal/botstore/botstore.go:165, :174; mysql.go:237, :243, :312, :439 bot_store_meta; cmd/botstore-migrate/main.go:283 bot_knowledge_fts | B | bot store (bot_context schema / MySQL bot store), not the business public schema |
| internal/botstore/migrate.go:159 sqlite_master; :365, :381, :384 sqlite_sequence | B | SQLite source side of the bot store migration |

## Counts
- 72 errors in total.
- A: 7 sites (6 distinct statements) fixed. backoffice_pos_operations.go:577 is the only portable text touched; it was broken on MySQL too.
- B: 64.
- K: 1.

## Build
- `go build -p 2 ./...` passes.
- vc-server-pgnative-static (CGO_ENABLED=0) sha256 1a9638cdb9e5b86360c599824492272358c523f95d2afed9a6a92350b51f5529

## Not verified
- The fixed statements have not been re-PREPAREd. Rerun pgnative-sqlcheck -prepare.
- B relies on the exported schema, not on a live information_schema query (docker access denied).
- The bot-context sites should be checked by running the tool with search_path=bot_context.
