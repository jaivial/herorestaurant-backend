-- Stripe Connect: the platform Stripe account is the gateway for every tenant.
-- Coordination id: stripe_connect_multitenant_v1
--   Each restaurant gets an Express connected account created by the platform;
--   the restaurant only completes Stripe-hosted onboarding (KYC + IBAN).
--   account_encrypted holds {account_id, ...} sealed with BACKOFFICE_VAULT_KEY,
--   bound to the restaurant (AAD) so it cannot be moved to another tenant.
--   account_hash (HMAC) lets webhooks find the tenant without decrypting rows.

CREATE TABLE IF NOT EXISTS `restaurant_stripe_connect` (
  `restaurant_id` INT NOT NULL,
  `account_encrypted` TEXT NOT NULL,
  `account_hash` CHAR(64) NOT NULL,
  `status` VARCHAR(24) NOT NULL DEFAULT 'pending',
  `charges_enabled` TINYINT(1) NOT NULL DEFAULT 0,
  `payouts_enabled` TINYINT(1) NOT NULL DEFAULT 0,
  `details_submitted` TINYINT(1) NOT NULL DEFAULT 0,
  `demo` TINYINT(1) NOT NULL DEFAULT 0,
  `created_at` TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`restaurant_id`),
  UNIQUE KEY `uq_restaurant_stripe_connect_hash` (`account_hash`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

SET @col_exists := (
    SELECT COUNT(*) FROM information_schema.COLUMNS
    WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'booking_checkouts' AND COLUMN_NAME = 'destination_hash'
);
SET @ddl := IF(@col_exists = 0,
  'ALTER TABLE booking_checkouts ADD COLUMN destination_hash CHAR(64) NULL, ADD COLUMN application_fee_cents INT NOT NULL DEFAULT 0, ADD COLUMN expires_at TIMESTAMP NULL',
  'SELECT 1');
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

-- Per-restaurant Stripe keys (stripe_prereserva_adelanto_v1) are replaced by
-- the platform account: drop the stored tenant secrets entirely.
DROP TABLE IF EXISTS `restaurant_stripe_config`;
