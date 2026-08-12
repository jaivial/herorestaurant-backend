-- Per-user, per-restaurant UI preferences.
--
-- Generic key/value store scoped to (user_id, restaurant_id) so future UI
-- preferences (today: reservas table-vs-grid display mode) reuse the same table
-- without a schema change. Loaded server-side via GET /api/admin/me and written
-- via PUT /api/admin/me/preferences.
--
-- Idempotent.

CREATE TABLE IF NOT EXISTS `user_preferences` (
  `id` INT NOT NULL AUTO_INCREMENT,
  `user_id` INT NOT NULL,
  `restaurant_id` INT NOT NULL,
  `pref_key` VARCHAR(64) NOT NULL,
  `pref_value` VARCHAR(255) NULL,
  `updated_at` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_user_pref` (`user_id`, `restaurant_id`, `pref_key`),
  KEY `idx_user_pref_restaurant` (`restaurant_id`),
  CONSTRAINT `fk_user_pref_user` FOREIGN KEY (`user_id`) REFERENCES `bo_users` (`id`) ON DELETE CASCADE,
  CONSTRAINT `fk_user_pref_restaurant` FOREIGN KEY (`restaurant_id`) REFERENCES `restaurants` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
