# Result: repair-padfk1 (FK PAD parity design + whatsapp_bot_sessions namespace fix; code-only, nothing applied; NOT complete)

## 1. whatsapp_bot_sessions: actual namespaces (from source)
- **MySQL default (BOT_CONTEXT_DRIVER=sqlite, the config default):**
  - botStateDB() returns the business MySQL pool, so sessions live in business MySQL whatsapp_bot_sessions (unicode_ci PAD SPACE);
  - SQLite never holds this table (botstore SQLite schema has no sessions; whatsapp_bot_context_sqlite.go:73-78);
  - SQLite SQL and the R5 SQLite roundtrip are untouched.
- **BOT_CONTEXT_DRIVER=postgres:**
  - sessions live in the PG bot store (botstore.go:329 whatsapp_bot_sessions: restaurant_id BIGINT, user_phone TEXT with the DB default (deterministic) collation, PK (restaurant_id, user_phone));
  - unified DB: schema bot_context (sqldb.SchemaBotContext, search_path pinned);
  - legacy separate bot DB: schema "", no dbmigrate.ci_ai, so refused below.
- **The business "public" homolog is not the runtime table on PG.** The padpk1 entry in the business gate was the wrong location. It is moved to RequiredBotPadKeyIndexes (internal/db/padkeys.go); the business gate is now 133 entries, the bot gate 1, 134 in total.
- **Fixed (fail closed):** newBotConversationStoreFor now refuses DB_DRIVER=postgres without BOT_CONTEXT_DRIVER=postgres. Without that, the botStateDB fallback would have run MySQL statement text against PG. The server PG refusal is unchanged.
- **Bot-store gate:** openBotConversationStoreSchema, when db.Postgres, calls appdb.RequireBotPadKeyIndexes on the bot pool after RequireMigrated. A PG bot store without the index is refused.
  - This is opt-in: PG bot store is not the default.
  - Any current PG bot-store deployment would be refused until the index exists. That is intended fail-closed behaviour; nothing is deployed.
- **PG statements (st.Postgres branches only; MySQL text unchanged; provider phone never trimmed):**
  - whatsapp_bot_history.go:55 ON CONFLICT (restaurant_id, (rtrim(user_phone, ' ')) COLLATE dbmigrate.ci_ai);
  - whatsapp_bot_human_takeover.go:92 the same;
  - human_takeover.go:107 pause read, pad equality.

Bot-context future DDL (run on the bot store schema, NOT business):
```
-- schema bot_context (unified DB; requires dbmigrate.ci_ai present in the same DB)
CREATE UNIQUE INDEX "ixpad__whatsapp_bot_sessions__PRIMARY" ON bot_context.whatsapp_bot_sessions ("restaurant_id", (rtrim("user_phone", ' ')) COLLATE "dbmigrate"."ci_ai");
```
- This imposes source PAD+ci identity on a TEXT column whose raw PK is deterministic.
- A collision probe on bot_context is required first (the existing probes cover the business copy only). Bot-context probe, count-only, coordinator:
  - `SELECT count(*) FROM (SELECT 1 FROM bot_context.whatsapp_bot_sessions GROUP BY restaurant_id, (rtrim(user_phone,' ') COLLATE dbmigrate.ci_ai) HAVING count(*)>1) g;`

## 2. FK children: value-preserving generated shadow keys (design; DDL not executed)
The 11 FKs, from schema.json. Every FK has ON UPDATE NO ACTION, all are single-column, and only bindings.page_id is NULL-able:

| child.col → parent.col | on delete |
|---|---|
| bo_member_invitation_tokens.role_slug → bo_roles.slug (varchar32) | RESTRICT |
| bo_role_permissions.role_slug → bo_roles.slug | CASCADE |
| site_builder_{assets,bindings,collections,domain_mappings,pages,publish_queue,versions}.site_id → site_builder_sites.id (varchar36) | CASCADE |
| site_builder_bindings.page_id (NULL) → site_builder_pages.id | CASCADE |
| site_builder_publish_queue.version_id → site_builder_versions.id | CASCADE |

**Source facts:**
- No Go code updates a parent key (bo_roles.slug, site_builder_{sites,pages,versions}.id) or a child FK column (repo grep). Parent ids are uuid.New / validated slugs.
- Child site_id/page_id come raw from the route/body (padpk1 trace).

**PG constraints checked:**
- **(a)** A generated column's expression may reference only base (non-generated) columns of its own table. Raw parent id/slug and raw child FK columns are base columns, so they are fine.
- **(b)** PG rejects FKs on generated referencing columns with ON UPDATE CASCADE/SET NULL/SET DEFAULT or ON DELETE SET NULL/SET DEFAULT ("invalid … action for foreign key constraint containing generated column"; documented in the generated-columns chapter and tablecmds.c).
  - The source actions here are ON UPDATE NO ACTION and ON DELETE CASCADE/RESTRICT, which are allowed on generated referencing columns.
  - **No blocker for these 11 FKs.**
  - Any FUTURE FK with ON UPDATE CASCADE cannot use this pattern; it would need a trigger-maintained ordinary shadow column instead (below).
- **(c)** A UNIQUE constraint on a STORED generated column is allowed, and an FK may reference it.
- **(d)** Nondeterministic-collation FKs: parent and child shadows use the identical explicit collation dbmigrate.ci_ai. Version-specific PG17/18 nondeterministic-FK checks must be confirmed in scratch QA, together with the ON DELETE CASCADE matching semantics under ci_ai.
- **(e)** MATCH SIMPLE (MySQL single-column semantics): a NULL child shadow (rtrim(NULL) = NULL) skips the check, as in MySQL.
- **(f)** MySQL NO ACTION is checked immediately; PG NO ACTION is checked at end of statement. This is a pre-existing plan-level difference (fkRule), unchanged.

**Shadow DDL proposal** (forward migration; raw columns and bytes unchanged; replaces only the 11 raw FKs on PG, since a raw FK would still reject PAD-equal children):
```
-- parents (business schema)
ALTER TABLE "bo_roles" ADD COLUMN "slug__pad" varchar(32) COLLATE "dbmigrate"."ci_ai" GENERATED ALWAYS AS (rtrim("slug", ' ')) STORED;
ALTER TABLE "bo_roles" ADD CONSTRAINT "uqpad__bo_roles__slug" UNIQUE ("slug__pad");
ALTER TABLE "site_builder_sites" ADD COLUMN "id__pad" varchar(36) COLLATE "dbmigrate"."ci_ai" GENERATED ALWAYS AS (rtrim("id", ' ')) STORED;
ALTER TABLE "site_builder_sites" ADD CONSTRAINT "uqpad__site_builder_sites__id" UNIQUE ("id__pad");
ALTER TABLE "site_builder_pages" ADD COLUMN "id__pad" varchar(36) COLLATE "dbmigrate"."ci_ai" GENERATED ALWAYS AS (rtrim("id", ' ')) STORED;
ALTER TABLE "site_builder_pages" ADD CONSTRAINT "uqpad__site_builder_pages__id" UNIQUE ("id__pad");
ALTER TABLE "site_builder_versions" ADD COLUMN "id__pad" varchar(36) COLLATE "dbmigrate"."ci_ai" GENERATED ALWAYS AS (rtrim("id", ' ')) STORED;
ALTER TABLE "site_builder_versions" ADD CONSTRAINT "uqpad__site_builder_versions__id" UNIQUE ("id__pad");
-- children: <col>__pad varchar(n) COLLATE "dbmigrate"."ci_ai" GENERATED ALWAYS AS (rtrim("<col>", ' ')) STORED, then
--   ALTER TABLE <child> DROP CONSTRAINT <raw fk>;   (plan FK not created on PG)
--   ALTER TABLE <child> ADD CONSTRAINT "fkpad__<child>__<col>" FOREIGN KEY ("<col>__pad") REFERENCES <parent>("<pcol>__pad") ON UPDATE NO ACTION ON DELETE <CASCADE|RESTRICT as source>;
-- for each of the 11 rows in the table above (bindings.page_id__pad stays NULL-able).
```
- **Impact:**
  - Every app SELECT/INSERT lists explicit columns (no SELECT * on these tables was found for INSERT); the generated columns are never written. SELECT * readers (queryOneAsMap) would gain the extra keys; none were found on these 4 parents and 7 children.
  - The import plan must create the shadows after the data load and validate the FKs (orphan check), keeping the raw rows byte-exact.
  - The existing ixpad__…__PRIMARY expression indexes stay as the PG upsert and lookup arbiters.

**Alternative only if a future FK needs ON UPDATE CASCADE:** an ordinary shadow column maintained by BEFORE INSERT/UPDATE triggers (shadow := rtrim(raw)), with an FK ON UPDATE CASCADE on the shadow. Risks:
- the cascade rewrites the child SHADOW only, so child raw bytes diverge from the parent until the trigger re-derives them, or never if raw is the source of truth;
- trigger ordering with cascades;
- import with triggers disabled needs a manual backfill.

Not needed for these 11; recorded, not designed further.

**Import/migration plan (manual, staged, not executed):**
1. Read-only probes: padded rows and pad-only FK matches (pad-collision-probes.sql). The coordinator reported 0 on current data, which is not a future guarantee.
2. On a scratch restore only (the parent asked for a GO at 08:53, with no response yet, so this is held): add parent shadows plus UNIQUE, child shadows and fkpad constraints, then validate.
3. Gate: extend the catalog validator to the shadow columns (attgenerated='s', collation, UNIQUE) and the fkpad constraints (contype 'f', confupdtype 'a', confdeltype per source). This is a next code step, not written yet, so the shadow design is not yet runtime-enforced.
4. Only then drop or omit the raw FKs on PG.

## Build / inventory
- go build -p 2 ./... OK.
- Inventory r105: 2501 sites (selectedSQL text only), all native, 0 portable audit failures.

## Gates (not claimed)
- **Still unapplied:** the shadow design (DDL and validator), the bot_context ixpad index and probe, and all 134 indexes.
- **Still open:**
  - ci_ai is only an approximation;
  - general_ci/ICU, LIKE accents, enum input, numeric precision and triggers;
  - checkout replay still does not compare payload.
- **Unchanged:** PG server disabled; tools report-only and index-gated; R5 untouched; 1117 pending / 731 orphans; checkout 503; 0 handler QA; 17 legitimate MySQL-only. No SQL, DB reads or writes, or provider calls.
- **Waiting:** scratch/manual QA waits for the parent's GO.
