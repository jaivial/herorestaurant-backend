-- 049: Booking cancellation attribution & modification tracking
--
-- Extends cancelled_bookings with staff user attribution and WHATSAPP origin.
-- Creates booking_modifications table for tracking field-level changes.
-- Idempotent via IF NOT EXISTS / IF NOT IN.

-- ── cancelled_bookings ─────────────────────────────────────────────────────────

ALTER TABLE cancelled_bookings
  ADD COLUMN cancelled_by_user_id BIGINT NULL COMMENT "bo_users.id of staff who cancelled" AFTER cancelled_by;

ALTER TABLE cancelled_bookings
  ADD COLUMN cancelled_by_name VARCHAR(120) NULL COMMENT "display name of cancelling user" AFTER cancelled_by_user_id;

-- Existing rows: cancelled_by stays staff or customer, new columns NULL.
-- Staff cancels from backoffice writes staff + user details.
-- Client cancels: customer + NULL user fields.
-- WhatsApp (n8n with cancelled_by=whatsapp): whatsapp + NULL.

SET @idx_exists := (SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'cancelled_bookings' AND INDEX_NAME = 'idx_cancelled_bookings_date_by');
SET @ddl := IF(@idx_exists = 0, 'ALTER TABLE cancelled_bookings ADD INDEX idx_cancelled_bookings_date_by (restaurant_id, reservation_date, cancelled_by)', 'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- ── booking_modifications ──────────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS booking_modifications (
    id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    restaurant_id INT NOT NULL DEFAULT 1,

    booking_id INT NOT NULL,

    -- The date selected on reservas page when modification was made.
    original_reservation_date DATE NOT NULL,

    -- Only these 7 fields trigger inclusion in Modificadas tab.
    field_modified VARCHAR(30) NOT NULL
        COMMENT "One of: date, time, party_size, rice, strollers, high_chairs, children",

    old_value TEXT NULL,
    new_value TEXT NULL,

    -- Who made the change.
    modified_by VARCHAR(20) NOT NULL DEFAULT "staff"
        COMMENT "staff, customer, or whatsapp",

    modified_by_user_id BIGINT NULL,
    modified_by_name VARCHAR(120) NULL,

    -- Snapshot for display reference.
    customer_name VARCHAR(120) NOT NULL,
    contact_phone VARCHAR(20) NULL,

    modification_date DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,

    -- If field_modified = "date", new_value = new reservation_date YYYY-MM-DD.
    INDEX idx_mod_restaurant_date (restaurant_id, original_reservation_date),
    INDEX idx_mod_booking (booking_id),
    INDEX idx_mod_date_field (original_reservation_date, field_modified)

) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
  COMMENT="Modificaciones de reservas para panel Canceladas/Modificadas";

