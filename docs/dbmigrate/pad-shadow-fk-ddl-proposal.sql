-- PROPOSED (repair-padgate1), NOT executed; review before any scratch apply
ALTER TABLE "bo_roles" ADD COLUMN "slug__pad" varchar(32) COLLATE "dbmigrate"."ci_ai" GENERATED ALWAYS AS (rtrim("slug", ' ')) STORED;
ALTER TABLE "bo_roles" ADD CONSTRAINT "uqpad__bo_roles__slug" UNIQUE ("slug__pad");
ALTER TABLE "site_builder_sites" ADD COLUMN "id__pad" varchar(36) COLLATE "dbmigrate"."ci_ai" GENERATED ALWAYS AS (rtrim("id", ' ')) STORED;
ALTER TABLE "site_builder_sites" ADD CONSTRAINT "uqpad__site_builder_sites__id" UNIQUE ("id__pad");
ALTER TABLE "site_builder_pages" ADD COLUMN "id__pad" varchar(36) COLLATE "dbmigrate"."ci_ai" GENERATED ALWAYS AS (rtrim("id", ' ')) STORED;
ALTER TABLE "site_builder_pages" ADD CONSTRAINT "uqpad__site_builder_pages__id" UNIQUE ("id__pad");
ALTER TABLE "site_builder_versions" ADD COLUMN "id__pad" varchar(36) COLLATE "dbmigrate"."ci_ai" GENERATED ALWAYS AS (rtrim("id", ' ')) STORED;
ALTER TABLE "site_builder_versions" ADD CONSTRAINT "uqpad__site_builder_versions__id" UNIQUE ("id__pad");
ALTER TABLE "bo_member_invitation_tokens" ADD COLUMN "role_slug__pad" varchar(32) COLLATE "dbmigrate"."ci_ai" GENERATED ALWAYS AS (rtrim("role_slug", ' ')) STORED;
-- raw FK fk_bo_member_invitation_tokens_role (MySQL name) must not exist on PG for bo_member_invitation_tokens.role_slug
ALTER TABLE "bo_member_invitation_tokens" ADD CONSTRAINT "fkpad__bo_member_invitation_tokens__role_slug" FOREIGN KEY ("role_slug__pad") REFERENCES "bo_roles" ("slug__pad") MATCH SIMPLE ON UPDATE NO ACTION ON DELETE RESTRICT;
ALTER TABLE "bo_role_permissions" ADD COLUMN "role_slug__pad" varchar(32) COLLATE "dbmigrate"."ci_ai" GENERATED ALWAYS AS (rtrim("role_slug", ' ')) STORED;
-- raw FK fk_bo_role_permissions_role (MySQL name) must not exist on PG for bo_role_permissions.role_slug
ALTER TABLE "bo_role_permissions" ADD CONSTRAINT "fkpad__bo_role_permissions__role_slug" FOREIGN KEY ("role_slug__pad") REFERENCES "bo_roles" ("slug__pad") MATCH SIMPLE ON UPDATE NO ACTION ON DELETE CASCADE;
ALTER TABLE "site_builder_assets" ADD COLUMN "site_id__pad" varchar(36) COLLATE "dbmigrate"."ci_ai" GENERATED ALWAYS AS (rtrim("site_id", ' ')) STORED;
-- raw FK fk_asset_site (MySQL name) must not exist on PG for site_builder_assets.site_id
ALTER TABLE "site_builder_assets" ADD CONSTRAINT "fkpad__site_builder_assets__site_id" FOREIGN KEY ("site_id__pad") REFERENCES "site_builder_sites" ("id__pad") MATCH SIMPLE ON UPDATE NO ACTION ON DELETE CASCADE;
ALTER TABLE "site_builder_bindings" ADD COLUMN "page_id__pad" varchar(36) COLLATE "dbmigrate"."ci_ai" GENERATED ALWAYS AS (rtrim("page_id", ' ')) STORED;
-- raw FK fk_binding_page (MySQL name) must not exist on PG for site_builder_bindings.page_id
ALTER TABLE "site_builder_bindings" ADD CONSTRAINT "fkpad__site_builder_bindings__page_id" FOREIGN KEY ("page_id__pad") REFERENCES "site_builder_pages" ("id__pad") MATCH SIMPLE ON UPDATE NO ACTION ON DELETE CASCADE;
ALTER TABLE "site_builder_bindings" ADD COLUMN "site_id__pad" varchar(36) COLLATE "dbmigrate"."ci_ai" GENERATED ALWAYS AS (rtrim("site_id", ' ')) STORED;
-- raw FK fk_binding_site (MySQL name) must not exist on PG for site_builder_bindings.site_id
ALTER TABLE "site_builder_bindings" ADD CONSTRAINT "fkpad__site_builder_bindings__site_id" FOREIGN KEY ("site_id__pad") REFERENCES "site_builder_sites" ("id__pad") MATCH SIMPLE ON UPDATE NO ACTION ON DELETE CASCADE;
ALTER TABLE "site_builder_collections" ADD COLUMN "site_id__pad" varchar(36) COLLATE "dbmigrate"."ci_ai" GENERATED ALWAYS AS (rtrim("site_id", ' ')) STORED;
-- raw FK fk_collection_site (MySQL name) must not exist on PG for site_builder_collections.site_id
ALTER TABLE "site_builder_collections" ADD CONSTRAINT "fkpad__site_builder_collections__site_id" FOREIGN KEY ("site_id__pad") REFERENCES "site_builder_sites" ("id__pad") MATCH SIMPLE ON UPDATE NO ACTION ON DELETE CASCADE;
ALTER TABLE "site_builder_domain_mappings" ADD COLUMN "site_id__pad" varchar(36) COLLATE "dbmigrate"."ci_ai" GENERATED ALWAYS AS (rtrim("site_id", ' ')) STORED;
-- raw FK fk_domain_site (MySQL name) must not exist on PG for site_builder_domain_mappings.site_id
ALTER TABLE "site_builder_domain_mappings" ADD CONSTRAINT "fkpad__site_builder_domain_mappings__site_id" FOREIGN KEY ("site_id__pad") REFERENCES "site_builder_sites" ("id__pad") MATCH SIMPLE ON UPDATE NO ACTION ON DELETE CASCADE;
ALTER TABLE "site_builder_pages" ADD COLUMN "site_id__pad" varchar(36) COLLATE "dbmigrate"."ci_ai" GENERATED ALWAYS AS (rtrim("site_id", ' ')) STORED;
-- raw FK fk_page_site (MySQL name) must not exist on PG for site_builder_pages.site_id
ALTER TABLE "site_builder_pages" ADD CONSTRAINT "fkpad__site_builder_pages__site_id" FOREIGN KEY ("site_id__pad") REFERENCES "site_builder_sites" ("id__pad") MATCH SIMPLE ON UPDATE NO ACTION ON DELETE CASCADE;
ALTER TABLE "site_builder_publish_queue" ADD COLUMN "site_id__pad" varchar(36) COLLATE "dbmigrate"."ci_ai" GENERATED ALWAYS AS (rtrim("site_id", ' ')) STORED;
-- raw FK fk_queue_site (MySQL name) must not exist on PG for site_builder_publish_queue.site_id
ALTER TABLE "site_builder_publish_queue" ADD CONSTRAINT "fkpad__site_builder_publish_queue__site_id" FOREIGN KEY ("site_id__pad") REFERENCES "site_builder_sites" ("id__pad") MATCH SIMPLE ON UPDATE NO ACTION ON DELETE CASCADE;
ALTER TABLE "site_builder_publish_queue" ADD COLUMN "version_id__pad" varchar(36) COLLATE "dbmigrate"."ci_ai" GENERATED ALWAYS AS (rtrim("version_id", ' ')) STORED;
-- raw FK fk_queue_version (MySQL name) must not exist on PG for site_builder_publish_queue.version_id
ALTER TABLE "site_builder_publish_queue" ADD CONSTRAINT "fkpad__site_builder_publish_queue__version_id" FOREIGN KEY ("version_id__pad") REFERENCES "site_builder_versions" ("id__pad") MATCH SIMPLE ON UPDATE NO ACTION ON DELETE CASCADE;
ALTER TABLE "site_builder_versions" ADD COLUMN "site_id__pad" varchar(36) COLLATE "dbmigrate"."ci_ai" GENERATED ALWAYS AS (rtrim("site_id", ' ')) STORED;
-- raw FK fk_version_site (MySQL name) must not exist on PG for site_builder_versions.site_id
ALTER TABLE "site_builder_versions" ADD CONSTRAINT "fkpad__site_builder_versions__site_id" FOREIGN KEY ("site_id__pad") REFERENCES "site_builder_sites" ("id__pad") MATCH SIMPLE ON UPDATE NO ACTION ON DELETE CASCADE;
