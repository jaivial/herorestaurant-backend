-- Guest history for the POS.
--
-- analytics_customers already exists but is a PROJECTION: it is rebuilt from
-- reservations, invoices and paid tickets, keyed by whatever identity it can
-- guess (email, phone, tax id, or even a bare name). Writing POS links into it
-- would mean a rebuild can merge, split or drop the very person a waiter
-- attached to a check. So the POS gets its own small, authoritative record and
-- the history is DERIVED from pos_tickets, never copied.
--
-- pos_customers: one row per guest the restaurant chose to remember.
--   phone is stored as digits only so "+34 600 11 22 33" and "600112233" do
--   not become two people; phone and email are unique per restaurant when set
--   (MySQL lets several NULLs coexist, so guests without them are fine).
--   notes is free text the staff writes ("mesa junto a la ventana",
--   "alergia a frutos secos"). An allergy is health data under GDPR art. 9:
--   the UI says so next to the field, and erasing a guest wipes it.
--   anonymised_at: a guest who asks to be forgotten keeps their row (paid
--   tickets point at it and are fiscal records that must stay intact) but
--   loses every personal field.
--
-- pos_tickets.customer_id: the link lives on the CHECK, not the table visit,
-- so when a table is split one check per comensal, each check can belong to a
-- different person.
CREATE TABLE IF NOT EXISTS pos_customers (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  restaurant_id INT NOT NULL,
  display_name VARCHAR(180) NOT NULL,
  phone VARCHAR(32) NULL,
  email VARCHAR(255) NULL,
  tax_id VARCHAR(40) NULL,
  notes VARCHAR(1000) NULL,
  created_by INT NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  anonymised_at DATETIME NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uq_pos_customers_tenant_id (restaurant_id, id),
  UNIQUE KEY uq_pos_customers_phone (restaurant_id, phone),
  UNIQUE KEY uq_pos_customers_email (restaurant_id, email),
  KEY idx_pos_customers_name (restaurant_id, display_name),
  CONSTRAINT fk_pos_customers_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

SET @pos_ticket_customer_col = IF(
  (SELECT COUNT(*) FROM information_schema.COLUMNS
     WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'pos_tickets'
       AND COLUMN_NAME = 'customer_id') > 0,
  'SELECT 1',
  'ALTER TABLE pos_tickets ADD COLUMN customer_id BIGINT UNSIGNED NULL AFTER guest_label, ADD KEY idx_pos_tickets_customer (restaurant_id, customer_id, status), ADD CONSTRAINT fk_pos_tickets_customer FOREIGN KEY (restaurant_id, customer_id) REFERENCES pos_customers (restaurant_id, id)'
);
PREPARE pos_ticket_customer_stmt FROM @pos_ticket_customer_col;
EXECUTE pos_ticket_customer_stmt;
DEALLOCATE PREPARE pos_ticket_customer_stmt;
