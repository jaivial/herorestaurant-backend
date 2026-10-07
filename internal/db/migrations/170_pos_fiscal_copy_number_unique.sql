-- Repairs the duplicate-key that stopped "Emitir duplicado".
--
-- 167 declared UNIQUE (series_id, series_number), but a duplicate copy keeps the
-- SAME series_number as its original on purpose -- it is the same document
-- printed a second time, which is exactly what a "duplicado" means. So the
-- constraint forbade the very thing the feature exists to do, and the
-- INSERT..SELECT that makes the copy byte-for-byte identical (same number, same
-- content hash, same previous hash) failed with a duplicate-key error every time.
--
-- 167 is corrected in place too; this exists so databases that already ran the
-- old version pick up the change instead of silently keeping the broken
-- constraint.
--
-- The order matters and is not cosmetic:
--   1. a plain index on series_id is added first, because MySQL satisfies the
--      series_id foreign key with whichever index leads with that column, and it
--      had silently borrowed uq_pos_fiscal_documents_number for that. Dropping
--      the unique key while it is still the FK's index fails with
--      "Cannot drop index: needed in a foreign key constraint" (error 1553);
--   2. only then is the unique key replaced.
-- Without the FK there would be a moment with no uniqueness at all, so the
-- replacement index is added in the same statement sequence as the drop rather
-- than in a separate migration someone can run halfway.
SET @pos_fiscal_series_idx = IF(
  (SELECT COUNT(*) FROM information_schema.STATISTICS
     WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'pos_fiscal_documents'
       AND INDEX_NAME = 'idx_pos_fiscal_documents_series') > 0,
  'SELECT 1',
  'ALTER TABLE pos_fiscal_documents ADD INDEX idx_pos_fiscal_documents_series (series_id)'
);
PREPARE pos_fiscal_series_idx_stmt FROM @pos_fiscal_series_idx;
EXECUTE pos_fiscal_series_idx_stmt;
DEALLOCATE PREPARE pos_fiscal_series_idx_stmt;

SET @pos_fiscal_dup_drop = IF(
  (SELECT COUNT(*) FROM information_schema.STATISTICS
     WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'pos_fiscal_documents'
       AND INDEX_NAME = 'uq_pos_fiscal_documents_number') > 0,
  'ALTER TABLE pos_fiscal_documents DROP INDEX uq_pos_fiscal_documents_number',
  'SELECT 1'
);
PREPARE pos_fiscal_dup_drop_stmt FROM @pos_fiscal_dup_drop;
EXECUTE pos_fiscal_dup_drop_stmt;
DEALLOCATE PREPARE pos_fiscal_dup_drop_stmt;

SET @pos_fiscal_dup_add = IF(
  (SELECT COUNT(*) FROM information_schema.STATISTICS
     WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'pos_fiscal_documents'
       AND INDEX_NAME = 'uq_pos_fiscal_documents_number') > 0,
  'SELECT 1',
  'ALTER TABLE pos_fiscal_documents ADD UNIQUE KEY uq_pos_fiscal_documents_number (series_id, series_number, copy_number)'
);
PREPARE pos_fiscal_dup_add_stmt FROM @pos_fiscal_dup_add;
EXECUTE pos_fiscal_dup_add_stmt;
DEALLOCATE PREPARE pos_fiscal_dup_add_stmt;
