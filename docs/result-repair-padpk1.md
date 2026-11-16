# Result: repair-padpk1 (18 PAD primary-key caller parity; code-only, nothing applied; NOT complete)

## Keys
- The 18 keys are the derived-padplan PRIMARY proposals with PAD text.
- reservation_location_occupancy (enum-only PK) is excluded and stays gated.

| PK (parts; pad = rtrim+ci_ai) | supplementary index |
|---|---|
| bo_role_permissions (role_slug, section_key) | ixpad__bo_role_permissions__PRIMARY |
| bo_roles (slug) | ixpad__bo_roles__PRIMARY |
| conversation_sessions (id) | ixpad__conversation_sessions__PRIMARY |
| platform_settings (setting_key) | ixpad__platform_settings__PRIMARY |
| pos_daily_sequences (restaurant_id, business_date, sequence_type) | ixpad__pos_daily_sequences__PRIMARY |
| pos_role_permissions (restaurant_id, role_slug, permission_key) | ixpad__pos_role_permissions__PRIMARY |
| restaurant_ai_provider_keys (restaurant_id, provider) | ixpad__restaurant_ai_provider_keys__PRIMARY |
| schema_migrations (id) | ixpad__schema_migrations__PRIMARY (MySQL bootstrap only; PG skips migrations) |
| site_builder_{assets,bindings,collections,domain_mappings,pages,publish_queue,sites,versions} (id) | ixpad__site_builder_<t>__PRIMARY |
| stock_role_permissions (restaurant_id, role_slug, permission_key) | ixpad__stock_role_permissions__PRIMARY |
| whatsapp_bot_sessions (restaurant_id, user_phone) | ixpad__whatsapp_bot_sessions__PRIMARY |

## Future DDL (NOT executed; supplementary; the raw PK is kept as the PK and FK target)
```
CREATE UNIQUE INDEX "ixpad__bo_role_permissions__PRIMARY" ON "bo_role_permissions" ((rtrim("role_slug", ' ')) COLLATE "dbmigrate"."ci_ai", (rtrim("section_key", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__bo_roles__PRIMARY" ON "bo_roles" ((rtrim("slug", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__conversation_sessions__PRIMARY" ON "conversation_sessions" ((rtrim("id", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__platform_settings__PRIMARY" ON "platform_settings" ((rtrim("setting_key", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__pos_daily_sequences__PRIMARY" ON "pos_daily_sequences" ("restaurant_id", "business_date", (rtrim("sequence_type", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__pos_role_permissions__PRIMARY" ON "pos_role_permissions" ("restaurant_id", (rtrim("role_slug", ' ')) COLLATE "dbmigrate"."ci_ai", (rtrim("permission_key", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__restaurant_ai_provider_keys__PRIMARY" ON "restaurant_ai_provider_keys" ("restaurant_id", (rtrim("provider", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__schema_migrations__PRIMARY" ON "schema_migrations" ((rtrim("id", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__site_builder_assets__PRIMARY" ON "site_builder_assets" ((rtrim("id", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__site_builder_bindings__PRIMARY" ON "site_builder_bindings" ((rtrim("id", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__site_builder_collections__PRIMARY" ON "site_builder_collections" ((rtrim("id", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__site_builder_domain_mappings__PRIMARY" ON "site_builder_domain_mappings" ((rtrim("id", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__site_builder_pages__PRIMARY" ON "site_builder_pages" ((rtrim("id", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__site_builder_publish_queue__PRIMARY" ON "site_builder_publish_queue" ((rtrim("id", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__site_builder_sites__PRIMARY" ON "site_builder_sites" ((rtrim("id", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__site_builder_versions__PRIMARY" ON "site_builder_versions" ((rtrim("id", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__stock_role_permissions__PRIMARY" ON "stock_role_permissions" ("restaurant_id", (rtrim("role_slug", ' ')) COLLATE "dbmigrate"."ci_ai", (rtrim("permission_key", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__whatsapp_bot_sessions__PRIMARY" ON "whatsapp_bot_sessions" ("restaurant_id", (rtrim("user_phone", ' ')) COLLATE "dbmigrate"."ci_ai");
```
The gate (internal/db/padkeys.go) now has 134 entries (116 + 18 under "padpk1").

## Callers patched (PG text only; MySQL unchanged)
- **RBAC:**
  - rbac.go:231/255/287/317;
  - member_access_flows.go:582;
  - roles.go:553 ON CONFLICT (role_slug, section_key) → expression target;
  - bo_roles FK joins br.slug = t.role_slug / ur.role at member_access_flows.go 892→916 (portable→native), 1201, 1303, 1836, and middleware.go:159 (CASE expression), now both sides rtrim+ci_ai.
- **POS sequence:** pos.go:664 ON CONFLICT and :669/:695 ('TICKET' constant).
- **POS/stock role permissions:**
  - pos.go:70;
  - pos_admin.go:173 and :220 ON CONFLICT;
  - stock.go:95;
  - stock_permissions.go:19 and :108 ON CONFLICT.
- **conversation_sessions:** conversation_history.go:152 and :286 (PG twins of :146/:279).
- **site_builder ids (22 PG statements):**
  - sites 481/1075/1507/1561/1713/1871/1893, registrar.go:253;
  - pages 625/658/753/773;
  - assets 932/966/981;
  - bindings 1389/1456/1471;
  - versions 1155;
  - mappings 1774/1789/1814/1841.
- **platform_settings:** stripe_connect_fees.go:62 IN list (PG pad) and :198 ON CONFLICT. The returned raw key is still matched against Go constants, as in the source.
- **restaurant_ai_provider_keys:** whatsapp_bot_ai_config.go:98 and :221 ON CONFLICT. Keys and secrets were not read.

**Not patched (explicit):**
- **whatsapp_bot_sessions callers** (whatsapp_bot_history.go:48/55, human_takeover.go:85/92/107) run on botStateDB (st.Postgres branch, possibly the bot_context schema or SQLite). The business-schema index gate does not cover that store. **BLOCKED** until the botstore PG schema has the matching ixpad index and its own gate. Provider user_phone is never trimmed.
- **schema_migrations:** MySQL-only.
- **instatic_manager.go:944:** inserts a mapping with a generated id; its conflict is on domain (already patched).

No ON CONFLICT targets the raw PK on PG any more for these keys; the arbiter is the supplementary expression index. Exact-equal rows also collide on it, so the raw PK cannot raise first. No new IsUniqueViolationOn names are involved; the existing handlers are generic.

## FK child write paths (all 11) — source trace
| FK | child writer | source of the FK value | PG risk if the client sends a padded id |
|---|---|---|---|
| bo_member_invitation_tokens.role_slug→bo_roles | member_access_flows.go:640 | normalizeBORole (lower+TrimSpace+validator) and a bo_roles existence check | none (canonical) |
| bo_role_permissions.role_slug→bo_roles | roles.go:547 | role from the create path (normalized slug) | none |
| site_builder_{assets,collections,pages,versions,bindings,domain_mappings,publish_queue}.site_id→sites | site_builder.go:897/1119/548-path/1364/1739, instatic/registrar | chi.URLParam "siteId", RAW. The ownership check (now PAD-aware) passes, then the child INSERT binds the raw param | MySQL accepts the FK (PAD SPACE); PG raw FK (ci_ai NO PAD) rejects it → 500, not silent corruption |
| site_builder_bindings.page_id→pages | site_builder.go:1364 | request body page id, RAW | same as above |
| site_builder_publish_queue.version_id→versions | no Go writer | — | legacy only |

**Design (proposal; not implemented, so stored child bytes match MySQL for all non-padded input):**
- **Option 1 (preferred, minimal):** after the PAD-aware ownership lookup, read back the parent's RAW id (`SELECT id … WHERE rtrim(id)=rtrim(?)`) and bind that for the child INSERT.
  - The logical parent is preserved and the FK is valid on both engines.
  - The child bytes differ from MySQL only when the client sent a padded id: MySQL would store the padded bytes, Option 1 stores the parent's exact bytes.
  - The client's old raw value is not needed for identity and can be logged at debug level only.
  - This is a deliberate technical deviation and needs reviewer sign-off before coding; there are about 7 call sites.
- **Option 2 (byte-exact):** a generated STORED shadow column (rtrim(id,' ')) on parent and child, with the FKs re-pointed to the shadow pair (the parent shadow gets UNIQUE). Raw child bytes are kept exactly. This is a forward schema migration plus import-plan change, and stays unapplied.
- **Current data:** probes show 0 padded values. That is no guarantee for the future; generated UUID writers cannot produce padding, but route params can.

## Provider-derived PKs
- **conversation_sessions.id = "sess_<rid>_<senderNumber>_<unix>"** (conversation_history.go:289). senderNumber comes from the request/provider; reads are PAD-aware on PG now. The id is not trimmed.
- **whatsapp_bot_sessions.user_phone = msg.Sender**, the provider value, never trimmed. Its PG store gate is blocked, as above.
- **Rejecting edge spaces at write time is NOT adopted:** it would refuse IDs the source accepts. Value-preserving SQL identity plus the supplementary unique index is used instead.

## Build / inventory
- go build -p 2 ./... OK.
- Inventory r104: 2501 sites (conversions only), all native, 0 portable audit failures.

## Gates (not claimed)
- **BLOCKED:** the botstore whatsapp_bot_sessions gate.
- **Needs sign-off:** the FK child-path decision (Option 1 vs 2).
- **Still open:** enum PK, general_ci/ICU, LIKE accents, enum input, numeric precision, triggers.
- **Unapplied:** all 134 indexes.
- **Still unverified:** the gate rendering regex.
- **Unchanged:**
  - checkout replay still does not compare payload;
  - PG server disabled; tools report-only and index-gated;
  - R5 untouched; 1117 pending / 731 orphans; checkout 503; 0 handler QA; 17 legitimate MySQL-only;
  - no SQL, DB writes or provider calls.
- **Next:** manual scratch-index QA needs coordinator review and parent go-ahead.
