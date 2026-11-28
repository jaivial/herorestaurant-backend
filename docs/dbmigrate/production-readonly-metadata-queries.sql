-- Read-only production metadata needed by the PG port (no row data, no credentials, no env).
-- Coordinator runs through the approved read-only source; agent has no network.
-- 1) session/global numeric + fulltext variables
SELECT @@GLOBAL.div_precision_increment, @@SESSION.div_precision_increment;
SELECT @@GLOBAL.innodb_ft_min_token_size, @@GLOBAL.innodb_ft_max_token_size, @@GLOBAL.innodb_ft_enable_stopword,
       @@GLOBAL.innodb_ft_server_stopword_table, @@SESSION.innodb_ft_user_stopword_table,
       @@GLOBAL.ft_boolean_syntax, @@GLOBAL.ngram_token_size;
-- 2) fulltext index definition (bookings DDL only; contains no secrets)
SHOW CREATE TABLE bookings;
SELECT INDEX_NAME, COLUMN_NAME, SEQ_IN_INDEX, INDEX_TYPE FROM INFORMATION_SCHEMA.STATISTICS
 WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bookings' AND INDEX_TYPE = 'FULLTEXT' ORDER BY INDEX_NAME, SEQ_IN_INDEX;
-- 3) stopword source: default list size, and name of any configured custom stopword table
SELECT COUNT(*) FROM INFORMATION_SCHEMA.INNODB_FT_DEFAULT_STOPWORD;
-- only if innodb_ft_server_stopword_table / user_stopword_table is non-empty: export that table's 'value' column (word list, not personal data)
-- 4) source anomalies: column presence only
SELECT TABLE_NAME, COLUMN_NAME, COLUMN_TYPE, IS_NULLABLE, COLUMN_DEFAULT, EXTRA FROM INFORMATION_SCHEMA.COLUMNS
 WHERE TABLE_SCHEMA = DATABASE() AND ((TABLE_NAME = 'bookings' AND COLUMN_NAME IN ('created_at','added_date'))
   OR TABLE_NAME = 'modification_history' OR (TABLE_NAME = 'menu_dishes_catalog' AND COLUMN_NAME IN ('name','title')));
SELECT INDEX_NAME, COLUMN_NAME FROM INFORMATION_SCHEMA.STATISTICS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'modification_history';
SELECT TABLE_NAME FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME IN ('DIA','FINDE','dia','finde');

