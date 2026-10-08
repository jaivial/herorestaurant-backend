-- menus.menu_type and restaurant_menu_templates.menu_type become a documented
-- numeric code instead of a loose string compared literally across ~37 SQL
-- statements.
-- Coordination id: menu_type_numeric_v1
--   menus.menu_type / restaurant_menu_templates.menu_type (TINYINT UNSIGNED)
--     -> REST payloads (JSON number, legacy string still accepted on write)
--     -> backoffice + preact menu editors
--
-- WHAT EACH NUMERIC VALUE MEANS (single source of truth: internal/api/menutype.go)
--
--   0 = no type / unknown  (reserved: missing or unrecognized value, never
--       written on purpose so a bad row stays detectable)
--   1 = closed_conventional  - closed menu, conventional (current default)
--   2 = closed_group         - closed menu for groups
--   3 = a_la_carte           - carta / a la carta, conventional
--   4 = a_la_carte_group     - carta for groups
--   5 = a_la_carte_time      - carta by time slot
--   6 = special              - special menu (season / event)
--
-- ORDER MATTERS: the string -> number UPDATE must run BEFORE the ALTER, because
-- MySQL would otherwise cast 'special' to 0 and silently lose the type.
-- Every UPDATE is guarded by a per-value WHERE (and by the column still being a
-- string type) so re-running the file adds nothing and never overwrites a code.
-- An unrecognized string is deliberately left untouched: it becomes 0 and
-- becomes visible as "no type" instead of masquerading as the default. Only a
-- truly empty value becomes the default 1.

-- ── menus.menu_type ────────────────────────────────────────────────────────────
SET @menus_exists := (SELECT COUNT(*) FROM information_schema.TABLES
                      WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menus');
SET @menus_type_is_string := (SELECT COUNT(*) FROM information_schema.COLUMNS
                               WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menus'
                                 AND COLUMN_NAME = 'menu_type'
                                 AND DATA_TYPE IN ('varchar','char','text','tinytext','mediumtext','longtext'));

SET @ddl := IF(@menus_exists = 1 AND @menus_type_is_string = 1,
  'UPDATE menus SET menu_type = ''1'' WHERE TRIM(menu_type) = ''closed_conventional''', 'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @ddl := IF(@menus_exists = 1 AND @menus_type_is_string = 1,
  'UPDATE menus SET menu_type = ''2'' WHERE TRIM(menu_type) = ''closed_group''', 'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @ddl := IF(@menus_exists = 1 AND @menus_type_is_string = 1,
  'UPDATE menus SET menu_type = ''3'' WHERE TRIM(menu_type) = ''a_la_carte''', 'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @ddl := IF(@menus_exists = 1 AND @menus_type_is_string = 1,
  'UPDATE menus SET menu_type = ''4'' WHERE TRIM(menu_type) = ''a_la_carte_group''', 'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @ddl := IF(@menus_exists = 1 AND @menus_type_is_string = 1,
  'UPDATE menus SET menu_type = ''5'' WHERE TRIM(menu_type) = ''a_la_carte_time''', 'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @ddl := IF(@menus_exists = 1 AND @menus_type_is_string = 1,
  'UPDATE menus SET menu_type = ''6'' WHERE TRIM(menu_type) = ''special''', 'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- Empty (or whitespace only) source rows get the closed conventional default.
--
-- Empty values are matched with CHAR_LENGTH(TRIM(x)) = 0 on purpose: an empty
-- string inside the SET @ddl literal needs nested quotes, which MySQL reads as a
-- malformed literal and aborts the whole script (the ALTERs would never run).
SET @ddl := IF(@menus_exists = 1 AND @menus_type_is_string = 1,
  'UPDATE menus SET menu_type = ''1'' WHERE menu_type IS NULL OR CHAR_LENGTH(TRIM(menu_type)) = 0', 'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @menus_type_col := (SELECT COUNT(*) FROM information_schema.COLUMNS
                        WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menus'
                          AND COLUMN_NAME = 'menu_type');
SET @ddl := IF(@menus_exists = 1 AND @menus_type_col = 1,
  'ALTER TABLE menus MODIFY COLUMN menu_type TINYINT UNSIGNED NOT NULL DEFAULT 1', 'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- ── restaurant_menu_templates.menu_type (theme-per-menu-type map) ─────────────
SET @rmt_exists := (SELECT COUNT(*) FROM information_schema.TABLES
                    WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_menu_templates');
SET @rmt_type_is_string := (SELECT COUNT(*) FROM information_schema.COLUMNS
                             WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_menu_templates'
                               AND COLUMN_NAME = 'menu_type'
                               AND DATA_TYPE IN ('varchar','char','text','tinytext','mediumtext','longtext'));

SET @ddl := IF(@rmt_exists = 1 AND @rmt_type_is_string = 1,
  'UPDATE restaurant_menu_templates SET menu_type = ''1'' WHERE TRIM(menu_type) = ''closed_conventional''', 'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @ddl := IF(@rmt_exists = 1 AND @rmt_type_is_string = 1,
  'UPDATE restaurant_menu_templates SET menu_type = ''2'' WHERE TRIM(menu_type) = ''closed_group''', 'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @ddl := IF(@rmt_exists = 1 AND @rmt_type_is_string = 1,
  'UPDATE restaurant_menu_templates SET menu_type = ''3'' WHERE TRIM(menu_type) = ''a_la_carte''', 'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @ddl := IF(@rmt_exists = 1 AND @rmt_type_is_string = 1,
  'UPDATE restaurant_menu_templates SET menu_type = ''4'' WHERE TRIM(menu_type) = ''a_la_carte_group''', 'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @ddl := IF(@rmt_exists = 1 AND @rmt_type_is_string = 1,
  'UPDATE restaurant_menu_templates SET menu_type = ''6'' WHERE TRIM(menu_type) = ''special''', 'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @ddl := IF(@rmt_exists = 1 AND @rmt_type_is_string = 1,
  'UPDATE restaurant_menu_templates SET menu_type = ''1'' WHERE menu_type IS NULL OR CHAR_LENGTH(TRIM(menu_type)) = 0', 'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @rmt_type_col := (SELECT COUNT(*) FROM information_schema.COLUMNS
                      WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_menu_templates'
                        AND COLUMN_NAME = 'menu_type');
SET @ddl := IF(@rmt_exists = 1 AND @rmt_type_col = 1,
  'ALTER TABLE restaurant_menu_templates MODIFY COLUMN menu_type TINYINT UNSIGNED NOT NULL DEFAULT 1', 'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;
