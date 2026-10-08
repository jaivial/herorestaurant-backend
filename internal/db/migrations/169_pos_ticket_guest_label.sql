-- One account per comensal.
--
-- The POS could already split a table into several checks ("Separar comanda"),
-- but the checks were anonymous: with three guests on a table the waiter saw
-- "Cuenta 1 / Cuenta 2 / Cuenta 3" and no way to know which plate went to whom,
-- which is exactly when separate checks matter most (a table that wants to pay
-- apart, or one guest paying for their own share).
--
-- guest_label is the comensal's name as written on the check. It is optional and
-- never required: the account works exactly the same without it, so a table that
-- does not want to give names is not forced into a guest-management form.
--
-- 60 chars is enough for "Mesa 4 - Pérez, Rodríguez y Martínez" style labels
-- and short enough to print on a thermal receipt line without wrapping badly.
--
-- The column and the index are added behind information_schema guards, NOT a
-- bare ADD COLUMN. A plain ALTER is not idempotent, and this file's first
-- version was: the column was applied by hand to the dev database before the
-- server booted, and every boot after that failed with "Duplicate column name",
-- which crash-looped the backend and surfaced to the POS as a bare
-- "Error creating ticket". Migrations here run on every start, so they have to
-- be safe to re-run, including after a manual application.
SET @pos_guest_label_col = IF(
  (SELECT COUNT(*) FROM information_schema.COLUMNS
     WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'pos_tickets'
       AND COLUMN_NAME = 'guest_label') > 0,
  'SELECT 1',
  'ALTER TABLE pos_tickets ADD COLUMN guest_label VARCHAR(60) NULL AFTER ticket_number'
);
PREPARE pos_guest_label_stmt FROM @pos_guest_label_col;
EXECUTE pos_guest_label_stmt;
DEALLOCATE PREPARE pos_guest_label_stmt;

SET @pos_guest_label_idx = IF(
  (SELECT COUNT(*) FROM information_schema.STATISTICS
     WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'pos_tickets'
       AND INDEX_NAME = 'idx_pos_tickets_guest') > 0,
  'SELECT 1',
  'ALTER TABLE pos_tickets ADD INDEX idx_pos_tickets_guest (restaurant_id, visit_id, guest_label)'
);
PREPARE pos_guest_label_idx_stmt FROM @pos_guest_label_idx;
EXECUTE pos_guest_label_idx_stmt;
DEALLOCATE PREPARE pos_guest_label_idx_stmt;
