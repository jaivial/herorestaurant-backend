-- Stripe Connect fees managed by root from Plataforma > Stripe Connect.
-- Coordination id: stripe_connect_fees_v1
--   platform_settings: generic key/value for platform-wide settings.
--     stripe_connect.platform_fee_percent  global platform commission (%)
--     stripe_connect.stripe_base_percent   Stripe's own % passed through
--     stripe_connect.stripe_base_fixed_cents Stripe's own fixed fee passed through
--   restaurant_stripe_fee_overrides: per-restaurant platform commission that
--     overrides the global one (0 allowed). No row = use the global.
--   The restaurant is charged application_fee = stripe base + platform fee.

CREATE TABLE IF NOT EXISTS `platform_settings` (
  `setting_key` VARCHAR(96) NOT NULL,
  `setting_value` VARCHAR(255) NOT NULL,
  `updated_at` TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`setting_key`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `restaurant_stripe_fee_overrides` (
  `restaurant_id` INT NOT NULL,
  `platform_fee_percent` DECIMAL(5,2) NOT NULL,
  `updated_by` INT NULL,
  `updated_at` TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`restaurant_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
