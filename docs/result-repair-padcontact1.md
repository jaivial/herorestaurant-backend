# Result: repair-padcontact1 (8 contact identity keys + PK audit; code-only, nothing applied)

## Exact keys (schema.json + derived LOCAL metadata; PAD SPACE)
| # | index | key order | collation | NULL |
|---|---|---|---|---|
| 1 | bo_users.uniq_bo_users_email | email | unicode_ci | NN |
| 2 | bo_users.uniq_bo_users_username | username | unicode_ci | NULL |
| 3 | campaign_recipients.uq_campaign_target | campaign_id, channel, target | unicode_ci | NN |
| 4 | campaign_suppressions.uq_suppression | restaurant_id, channel, target | unicode_ci | NN |
| 5 | no_marketing.idx_no_marketing_phone | contact_phone (GLOBAL, no restaurant_id) | **general_ci** (ci_ai approximation is additionally ungrounded for general_ci; gated) | NN |
| 6 | pos_customers.uq_pos_customers_email | restaurant_id, email | unicode_ci | NULL |
| 7 | pos_customers.uq_pos_customers_phone | restaurant_id, phone | unicode_ci | NULL |
| 8 | restaurant_members.uniq_restaurant_members_restaurant_email | restaurant_id, email | unicode_ci | NULL |

## Required future DDL (NOT executed; additive; raw R5 indexes unchanged)
```
CREATE UNIQUE INDEX "ixpad__bo_users__uniq_bo_users_email" ON "bo_users" ((rtrim("email", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__bo_users__uniq_bo_users_username" ON "bo_users" ((rtrim("username", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__campaign_recipients__uq_campaign_target" ON "campaign_recipients" ("campaign_id", (rtrim("channel", ' ')) COLLATE "dbmigrate"."ci_ai", (rtrim("target", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__campaign_suppressions__uq_suppression" ON "campaign_suppressions" ("restaurant_id", (rtrim("channel", ' ')) COLLATE "dbmigrate"."ci_ai", (rtrim("target", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__no_marketing__idx_no_marketing_phone" ON "no_marketing" ((rtrim("contact_phone", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__pos_customers__uq_pos_customers_email" ON "pos_customers" ("restaurant_id", (rtrim("email", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__pos_customers__uq_pos_customers_phone" ON "pos_customers" ("restaurant_id", (rtrim("phone", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__restaurant_members__uniq_restaurant_members_res_bcbe3c17" ON "restaurant_members" ("restaurant_id", (rtrim("email", ' ')) COLLATE "dbmigrate"."ci_ai");
```
- The gate (internal/db/padkeys.go) now has 32 entries; the 8 new ones are under "padcontact1".
- The no_marketing index is future/read-only parity only. There is no Go writer for no_marketing anywhere in the repo, and none was invented.

## Caller catalogue
Method: every Go string literal that names the table, scanned for comparisons on the key, ON CONFLICT, ODKU or IGNORE (/tmp/litscan.py), plus repo greps for LOWER/TRIM forms and unique-error names.

| site | key | operation | treatment |
|---|---|---|---|
| backoffice_auth.go:510 | bo_users.email | lookup before superadmin bootstrap insert (:521) | PATCHED: PG pad |
| backoffice_auth.go:101/105 (login), member_access_flows.go:359/448, roles.go:681/709 | bo_users.email | LOWER(TRIM(email)) = LOWER(TRIM(?)) | already pad-neutral: both engines' TRIM removes the trailing spaces PAD SPACE ignores, and LOWER is redundant under ci. Unchanged. Not index-usable (pre-existing) |
| member_access_flows.go:545 | bo_users.username | LOWER(TRIM(COALESCE(username,''))) = LOWER(TRIM(?)) | pad-neutral; unchanged |
| member_access_flows.go:380/457 | username | UPDATE SET (WHERE id) | raw, unchanged |
| roles.go:340/854, bo_app_version.go:260, fichaje.go:1894 | restaurant_members.email = bo_users.email | LOWER(TRIM(m.email)) = LOWER(TRIM(u.email)) join | pad-neutral; unchanged |
| members.go:319/331, member_access_flows.go:183, pos_pin.go:219 | restaurant_members.email | INSERT / UPDATE SET (email lower+TrimSpace at members.go:271) | raw, unchanged; there is no equality lookup on the email key |
| backoffice_pos_customers.go:85 | pos_customers.phone/email | duplicate-conflict probe | PATCHED: PG pad on both, restaurant_id scoped |
| pos_customers.go:159/161, :186, :207 | phone/email | INSERT ("duplicate" substring then probe), UPDATE SET, anonymise NULL | raw; NULLs stay distinct |
| pos_customers.go:105/112 | email/phone LIKE search | not identity | unchanged (existing C/lower LIKE contract) |
| backoffice_campaigns.go:1290/1296 | campaign_recipients target | INSERT IGNORE / ON CONFLICT | PATCHED: the PG target is the pad expression list |
| backoffice_campaigns.go:880 | campaign_recipients | SELECT channel,target into a Go map (preview "already in ledger" counts) | GATE, not changed: a Go string-key map is stricter than the DB collation on BOTH engines (pre-existing). Preview counts only; the real insert is DB-arbitrated |
| campaign_bounce_sync.go:294/300, :309/:314 | campaign_suppressions | ODKU / IGNORE vs ON CONFLICT | PATCHED (2 PG targets) |
| campaign_email_validation.go:119, public_unsubscribe.go:177, whatsapp_bot_unsubscribe.go:119, whatsapp_unreachable.go:78 | campaign_suppressions | ON CONFLICT DO NOTHING | PATCHED |
| public_unsubscribe.go:201 | campaign_suppressions | already-suppressed lookup | PATCHED: PG pad on channel+target (multi-line portableSQL→nativeSQL) |
| campaign_suppression_target.go:125 | campaign_suppressions | batched DB-collation probe (all send paths) | PATCHED: PG pad on channel and target vs probe |
| backoffice_campaigns.go:92-133 | no_marketing | legacy UNION read with TRIM | unchanged (pad-neutral) |

- Invalid UTF-8, NUL, oversize and fail-closed behaviour in campaignSuppressionKey/target handling is untouched. Transactional booking messages are not marketing-gated (unchanged).
- None of these 8 keys has an IsUniqueViolationOn call (repo grep).
- No Go EqualFold was added; identity stays DB-decided.

## stockAliasDescription (revises padmixed1)
- padmixed1 mapped an input whose 255th byte fell inside a UTF-8 sequence to a NEW, shorter key. That was a new identity that could collide with another alias sharing the prefix.
- **Now:**
  - the byte-exact 255 cut is kept for every source-storable key;
  - an input whose cut would split a rune returns "" (fail closed).
- **Callers:**
  - stock_suppliers.go:273 → 400 "Alias description too long (255-byte limit falls inside a character)";
  - stock_documents.go:161 → no alias lookup (line stays NEEDS_MATCH);
  - stock_documents.go:686 → alias upsert skipped (the document line is still confirmed).
- Before, both of these failed with a DB error, ignored inside a savepoint.

## Primary-key PAD audit (complete source pass; design only)
Readers and writers come from the literal scan. They are the actual SQL sites with key equality.

| PK table.key | writers (value origin) | key-equality readers | FK children | risk |
|---|---|---|---|---|
| bo_roles.slug | roles.go:538 (normalizeBORole: lower+TrimSpace+validator) | member_access_flows.go:582/892/1174/1201/1276/1303/1809/1836, middleware.go:132/159, rbac.go:255/287 | invitation_tokens.role_slug RESTRICT, bo_role_permissions.role_slug CASCADE | low (validated slug) |
| bo_role_permissions (role_slug, section_key) | roles.go:547/551 | rbac.go:231/317, roles.go:549/553 | → bo_roles | low |
| conversation_sessions.id | conversation_history.go:289 generated "sess_<rid>_<sender>_<unix>" | conversation_history.go:146/152 (id from REQUEST), :279/:286 | - | **medium: a client-supplied lookup id; a padded request id matches on MySQL but not on PG** |
| platform_settings.setting_key | stripe_connect_fees.go:198 (constants) | :62 IN(constants) | - | none |
| pos_daily_sequences (rid, business_date, sequence_type) | pos.go:664/695 (constants) | pos.go:669 | - | none |
| pos_role_permissions (rid, role_slug, permission_key) | pos_admin.go:220 | pos.go:70, pos_admin.go:173 (normalizeBORole) | - | low |
| restaurant_ai_provider_keys (rid, provider) | whatsapp_bot_ai_config.go:219/220 (botIsKeyProvider allowlist) | :98 botProviderKey (internal constant) | - | low; secrets not read |
| schema_migrations.id | migrations.go:83 (MySQL bootstrap only) | migrations.go:72 | - | n/a on PG (skipped) |
| site_builder_sites.id | site_builder.go:284 (uuid.New) | site_builder.go:481/1075/1507/1561/1713/1866/1871/1893, instatic_manager.go:946, registrar.go:253 | assets/bindings/collections/mappings/pages/publish_queue/versions .site_id CASCADE | low (generated; route ids in URLs) |
| site_builder_pages.id | site_builder.go:304/588 (uuid) | :621/:625/:658/:753/:773 | bindings.page_id CASCADE | low |
| site_builder_versions.id | :1119/:1547 | :1150/:1155 | publish_queue.version_id CASCADE | low |
| site_builder_assets / bindings / domain_mappings .id | site_builder.go:897/1364/1739, registrar:299, instatic:944 (uuid) | assets :927/:932/:966/:981; bindings :1389/:1456/:1471; mappings :1774/:1789/:1810/:1814/:1841, registrar:301, instatic:946 | - | low |
| site_builder_collections.id, site_builder_publish_queue.id | no Go writer (repo-wide) | none | - | legacy/import only |
| stock_role_permissions (rid, role_slug, permission_key) | stock_permissions.go:106/107 | stock.go:95, stock_permissions.go:19/107/108 | - | low |
| whatsapp_bot_sessions (rid, user_phone) | whatsapp_bot_history.go:48/53 (msg.Sender, provider-derived), human_takeover.go:85/90 | history:50/55, human_takeover:87/92/107 | - | low-medium (provider identity) |

Values in these PK URL/lookup paths come from request path params. A padded parameter is the parity gap, and is worth fixing where a lookup takes client text: conversation_sessions, role slugs. Role slugs are already normalized by TrimSpace; conversation ids are not.

**Design options (value-preserving; no decision needed until data is probed):**
- (A) Keep the raw PK and FKs. Add a supplementary UNIQUE ixpad index per PK and pad the client-facing lookups. FK equality stays raw ci_ai (NO PAD). This is safe only if the probe shows no padded parent or child values, and writers keep refusing or normalizing them.
- (B) A generated canonical shadow column (rtrim(col,' ') STORED) with a UNIQUE constraint, and FKs re-pointed to the shadow pair. This changes schema and API-invisible columns, needs a migration and caller review, and is a heavier proposal.
- (C) Fail-closed request validation: reject client ids with edge spaces. This is a technical behaviour change on both engines and is safe only for generated ids (uuid/sess_), since such an id can never legitimately be padded.

**Recommendation:** (A) together with (C) for generated-id lookups (conversation_sessions, site_builder ids), after the read-only probes below show 0 padded values. (B) only if the probes find padded legacy values.

## Prepared READ-ONLY probes (for the coordinator; not executed; return counts only, no values)
```sql
-- padded / colliding identity counts (PG copy, schema newvillacarmen_business)
SELECT 'bo_users.email', count(*) FILTER (WHERE email <> rtrim(email,' ')), count(*) - count(DISTINCT rtrim(email,' ') COLLATE dbmigrate.ci_ai) FROM bo_users;
SELECT 'bo_users.username', count(*) FILTER (WHERE username <> rtrim(username,' ')), count(username) - count(DISTINCT rtrim(username,' ') COLLATE dbmigrate.ci_ai) FROM bo_users;
SELECT 'campaign_recipients', count(*) FILTER (WHERE target <> rtrim(target,' ') OR channel <> rtrim(channel,' ')), count(*) - count(DISTINCT (campaign_id, rtrim(channel,' ') COLLATE dbmigrate.ci_ai, rtrim(target,' ') COLLATE dbmigrate.ci_ai)) FROM campaign_recipients;
SELECT 'campaign_suppressions', count(*) FILTER (WHERE target <> rtrim(target,' ') OR channel <> rtrim(channel,' ')), count(*) - count(DISTINCT (restaurant_id, rtrim(channel,' ') COLLATE dbmigrate.ci_ai, rtrim(target,' ') COLLATE dbmigrate.ci_ai)) FROM campaign_suppressions;
SELECT 'no_marketing', count(*) FILTER (WHERE contact_phone <> rtrim(contact_phone,' ')), count(*) - count(DISTINCT rtrim(contact_phone,' ') COLLATE dbmigrate.ci_ai) FROM no_marketing;
SELECT 'pos_customers', count(*) FILTER (WHERE email <> rtrim(email,' ') OR phone <> rtrim(phone,' ')) FROM pos_customers;
SELECT 'restaurant_members', count(*) FILTER (WHERE email <> rtrim(email,' ')), count(email) - count(DISTINCT (restaurant_id, rtrim(email,' ') COLLATE dbmigrate.ci_ai)) FROM restaurant_members;
-- PK/FK padded values
SELECT 'conversation_sessions.id', count(*) FILTER (WHERE id <> rtrim(id,' ')) FROM conversation_sessions;
SELECT 'whatsapp_bot_sessions.user_phone', count(*) FILTER (WHERE user_phone <> rtrim(user_phone,' ')) FROM whatsapp_bot_sessions;
SELECT 'bo_roles.slug', count(*) FILTER (WHERE slug <> rtrim(slug,' ')) FROM bo_roles;
SELECT 'fk_child_role_slug', (SELECT count(*) FROM bo_member_invitation_tokens WHERE role_slug <> rtrim(role_slug,' ')) + (SELECT count(*) FROM bo_role_permissions WHERE role_slug <> rtrim(role_slug,' '));
SELECT 'site_builder_ids', (SELECT count(*) FROM site_builder_sites WHERE id <> rtrim(id,' ')) + (SELECT count(*) FROM site_builder_pages WHERE id <> rtrim(id,' ') OR site_id <> rtrim(site_id,' ')) + (SELECT count(*) FROM site_builder_versions WHERE id <> rtrim(id,' '));
```
A MySQL source twin replaces rtrim(x,' ') with TRIM(TRAILING ' ' FROM x) and drops the COLLATE.

## Build / inventory
- go build -p 2 ./... OK.
- Inventory r99: 2501 sites (conversions only), all native, 0 portable audit failures.

## Coverage / gates
- Unique keys with callers patched: 32 (5 + 10 + 9 + 8).
- Remaining: 84 of the 116 PAD unique proposals, plus the 18 PKs (design only).
- All 32 indexes are unapplied, additive, and keep the raw R5 indexes.
- The gate's rendering regex is still unverified (manual QA).
- ci_ai is only an approximation (general_ci for no_marketing is ungrounded).
- No send/runtime execution.
- Checkout replay still does not compare payload (recorded).
- PG server disabled; tools report-only and index-gated.
- R5 untouched; 1117 pending / 731 orphans; checkout 503; 0 handler QA; 17 legitimate MySQL-only.
- No SQL, DB writes or provider calls.

## Next
1. The coordinator runs the read-only probes above.
2. Review of the 32 DDL statements.
3. Decide PK option A/C per the probe results.
4. The next key group.
