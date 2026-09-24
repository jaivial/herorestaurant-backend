-- Stripe payment of the special-date prereserva adelanto.
-- Coordination id: stripe_prereserva_adelanto_v1
--   admin/config?content=stripe  -> restaurant_stripe_config (keys vault-encrypted)
--   public wizard summary "Continuar al pago" -> booking_checkouts (pending)
--   Stripe Checkout / demo checkout -> completion inserts the booking only
--   after the payment is confirmed; receipt PDF on BunnyCDN.

CREATE TABLE IF NOT EXISTS `restaurant_stripe_config` (
  `restaurant_id` INT NOT NULL,
  `secret_key_encrypted` TEXT NULL,
  `webhook_secret_encrypted` TEXT NULL,
  `publishable_key` VARCHAR(255) NULL,
  `demo_mode` TINYINT(1) NOT NULL DEFAULT 1,
  `currency` VARCHAR(8) NOT NULL DEFAULT 'eur',
  `updated_at` TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`restaurant_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `booking_checkouts` (
  `id` BIGINT NOT NULL AUTO_INCREMENT,
  `restaurant_id` INT NOT NULL,
  `public_id` VARCHAR(64) NOT NULL,
  `provider` VARCHAR(16) NOT NULL,
  `provider_session_id` VARCHAR(255) NULL,
  `status` VARCHAR(16) NOT NULL DEFAULT 'pending',
  `amount_cents` INT NOT NULL,
  `currency` VARCHAR(8) NOT NULL DEFAULT 'eur',
  `form_json` MEDIUMTEXT NOT NULL,
  `booking_id` INT NULL,
  `payment_intent_id` VARCHAR(255) NULL,
  `receipt_url` VARCHAR(1024) NULL,
  `error_message` VARCHAR(512) NULL,
  `created_at` TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  `paid_at` TIMESTAMP NULL,
  `completed_at` TIMESTAMP NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_booking_checkouts_public` (`public_id`),
  KEY `idx_booking_checkouts_session` (`provider_session_id`),
  KEY `idx_booking_checkouts_restaurant` (`restaurant_id`, `status`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
