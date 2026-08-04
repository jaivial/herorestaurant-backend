-- 075: POS control-rail metadata on visits, tickets and ticket lines.
--
-- Covers rail features: Aparcar (02), Salón (04), Juntar mesas (05), Cliente (08),
-- Empleado (14), Invita (13), Barra (17) and Comentario (18).
--
-- FK types verified against the live schema before writing:
--   pos_visits.id BIGINT UNSIGNED, pos_tickets.id BIGINT UNSIGNED,
--   pos_ticket_lines.id BIGINT UNSIGNED, restaurants.id INT, bo_users.id INT,
--   restaurant_members.id INT, restaurant_areas.id BIGINT.
--
-- Idempotent: every ALTER is guarded by an information_schema check.

SET @dbname = DATABASE();

-- ---------------------------------------------------------------------------
-- pos_visits: parked state (02), merge membership (05), customer (08), BAR (17)
-- ---------------------------------------------------------------------------
SET @s = (SELECT IF((SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='pos_visits' AND COLUMN_NAME='parked_at') > 0,
  'SELECT 1',
  'ALTER TABLE pos_visits ADD COLUMN parked_at DATETIME NULL'));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @s = (SELECT IF((SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='pos_visits' AND COLUMN_NAME='parked_note') > 0,
  'SELECT 1',
  'ALTER TABLE pos_visits ADD COLUMN parked_note VARCHAR(300) NULL'));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @s = (SELECT IF((SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='pos_visits' AND COLUMN_NAME='merged_into_visit_id') > 0,
  'SELECT 1',
  'ALTER TABLE pos_visits ADD COLUMN merged_into_visit_id BIGINT UNSIGNED NULL'));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @s = (SELECT IF((SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='pos_visits' AND COLUMN_NAME='customer_name') > 0,
  'SELECT 1',
  'ALTER TABLE pos_visits ADD COLUMN customer_name VARCHAR(180) NULL'));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @s = (SELECT IF((SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='pos_visits' AND COLUMN_NAME='customer_tax_id') > 0,
  'SELECT 1',
  'ALTER TABLE pos_visits ADD COLUMN customer_tax_id VARCHAR(40) NULL'));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @s = (SELECT IF((SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='pos_visits' AND COLUMN_NAME='customer_address') > 0,
  'SELECT 1',
  'ALTER TABLE pos_visits ADD COLUMN customer_address VARCHAR(300) NULL'));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- Barra: add BAR to the channel enum, keeping existing values and default.
SET @s = (SELECT IF((SELECT COLUMN_TYPE FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='pos_visits' AND COLUMN_NAME='channel') LIKE '%BAR%',
  'SELECT 1',
  "ALTER TABLE pos_visits MODIFY COLUMN channel ENUM('DINE_IN','TAKEAWAY','DELIVERY','BAR') NOT NULL DEFAULT 'DINE_IN'"));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- Juntar mesas: MERGED is a terminal, non-billable visit state.
SET @s = (SELECT IF((SELECT COLUMN_TYPE FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='pos_visits' AND COLUMN_NAME='status') LIKE '%MERGED%',
  'SELECT 1',
  "ALTER TABLE pos_visits MODIFY COLUMN status ENUM('OPEN','CLOSED','CANCELLED','MERGED') NOT NULL DEFAULT 'OPEN'"));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @s = (SELECT IF((SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS
  WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='pos_visits' AND INDEX_NAME='idx_pos_visits_merged') > 0,
  'SELECT 1',
  'ALTER TABLE pos_visits ADD KEY idx_pos_visits_merged (restaurant_id, merged_into_visit_id)'));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @s = (SELECT IF((SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS
  WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='pos_visits' AND INDEX_NAME='idx_pos_visits_parked') > 0,
  'SELECT 1',
  'ALTER TABLE pos_visits ADD KEY idx_pos_visits_parked (restaurant_id, status, parked_at)'));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- ---------------------------------------------------------------------------
-- pos_tickets: operator attribution (14) and ticket-level note (18)
-- ---------------------------------------------------------------------------
SET @s = (SELECT IF((SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='pos_tickets' AND COLUMN_NAME='operator_member_id') > 0,
  'SELECT 1',
  'ALTER TABLE pos_tickets ADD COLUMN operator_member_id INT NULL'));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @s = (SELECT IF((SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='pos_tickets' AND COLUMN_NAME='note') > 0,
  'SELECT 1',
  'ALTER TABLE pos_tickets ADD COLUMN note VARCHAR(500) NULL'));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @s = (SELECT IF((SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS
  WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='pos_tickets' AND INDEX_NAME='idx_pos_tickets_operator') > 0,
  'SELECT 1',
  'ALTER TABLE pos_tickets ADD KEY idx_pos_tickets_operator (restaurant_id, operator_member_id, status)'));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @s = (SELECT IF((SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLE_CONSTRAINTS
  WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='pos_tickets' AND CONSTRAINT_NAME='fk_pos_tickets_operator_member') > 0,
  'SELECT 1',
  'ALTER TABLE pos_tickets ADD CONSTRAINT fk_pos_tickets_operator_member FOREIGN KEY (operator_member_id) REFERENCES restaurant_members(id) ON DELETE SET NULL'));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- ---------------------------------------------------------------------------
-- pos_ticket_lines: comp/invitación (13) and operator attribution (14).
-- `notes` already exists on this table and serves Comentario (18).
-- ---------------------------------------------------------------------------
SET @s = (SELECT IF((SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='pos_ticket_lines' AND COLUMN_NAME='comped_at') > 0,
  'SELECT 1',
  'ALTER TABLE pos_ticket_lines ADD COLUMN comped_at DATETIME NULL'));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @s = (SELECT IF((SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='pos_ticket_lines' AND COLUMN_NAME='comp_reason') > 0,
  'SELECT 1',
  'ALTER TABLE pos_ticket_lines ADD COLUMN comp_reason VARCHAR(500) NULL'));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @s = (SELECT IF((SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='pos_ticket_lines' AND COLUMN_NAME='comped_by') > 0,
  'SELECT 1',
  'ALTER TABLE pos_ticket_lines ADD COLUMN comped_by INT NULL'));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @s = (SELECT IF((SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='pos_ticket_lines' AND COLUMN_NAME='operator_member_id') > 0,
  'SELECT 1',
  'ALTER TABLE pos_ticket_lines ADD COLUMN operator_member_id INT NULL'));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @s = (SELECT IF((SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS
  WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='pos_ticket_lines' AND INDEX_NAME='idx_pos_ticket_lines_comped') > 0,
  'SELECT 1',
  'ALTER TABLE pos_ticket_lines ADD KEY idx_pos_ticket_lines_comped (restaurant_id, comped_at)'));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @s = (SELECT IF((SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLE_CONSTRAINTS
  WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='pos_ticket_lines' AND CONSTRAINT_NAME='fk_pos_ticket_lines_operator_member') > 0,
  'SELECT 1',
  'ALTER TABLE pos_ticket_lines ADD CONSTRAINT fk_pos_ticket_lines_operator_member FOREIGN KEY (operator_member_id) REFERENCES restaurant_members(id) ON DELETE SET NULL'));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;
