-- Defensive reconciliation: ensure the `menus` table exists and carries the
-- compatibility columns expected by the Go code introduced in #114.
--
-- Why this exists:
--   - Some legacy DBs only carry `menusDeGrupos` (the pre-#036 name). Migration
--     036 renames it to `menus`, but on databases that were imported from a
--     dump produced BEFORE 036 was applied, the rename never happened and the
--     runner skips 036 because either the rename preconditions were false at the
--     time (no menusDeGrupos yet) or the dump already lacked the table.
--   - The code added in #114 references the `menus` table and columns
--     (menu_type, is_draft, editor_version) as if they have always existed.
--
-- Behaviour:
--   - If `menusDeGrupos` exists and `menus` does not: rename.
--   - If `menus` exists: skip the rename (idempotent).
--   - Add menu_type / is_draft / editor_version columns to `menus` if missing
--     (same shape used by 085_menus_schema_compatibility.sql and
--     086_menus_stale_table_repair.sql).
--   - All steps are guarded with INFORMATION_SCHEMA + PREPARE/EXECUTE and are
--     no-ops on already-conformant databases (including the live prod).

SET @legacy_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menusDeGrupos'
);
SET @menus_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menus'
);

SET @ddl := IF(@legacy_exists = 1 AND @menus_exists = 0,
  'RENAME TABLE `menusDeGrupos` TO `menus`',
  'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- Re-read after the (possible) rename.
SET @menus_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menus'
);

-- menu_type
SET @col_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menus' AND COLUMN_NAME = 'menu_type'
);
SET @ddl := IF(@menus_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `menus` ADD COLUMN `menu_type` VARCHAR(64) NOT NULL DEFAULT ''closed_conventional''',
  'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- is_draft
SET @col_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menus' AND COLUMN_NAME = 'is_draft'
);
SET @ddl := IF(@menus_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `menus` ADD COLUMN `is_draft` TINYINT(1) NOT NULL DEFAULT 0',
  'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- editor_version
SET @col_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menus' AND COLUMN_NAME = 'editor_version'
);
SET @ddl := IF(@menus_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `menus` ADD COLUMN `editor_version` INT NOT NULL DEFAULT 1',
  'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;
