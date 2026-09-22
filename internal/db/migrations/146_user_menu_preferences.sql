-- Per-user, per-restaurant, per-menu UI preferences.
-- Coordination id: menu_editor_preview_open_v1
--
-- Extends the user_preferences idea to a menu scope so the editor/preview split
-- of /app/comida/menus/crear?menuId= is remembered for every menu id, whatever
-- its type (closed_conventional, a_la_carte, groups, special). The value is
-- written only over the group-menus-v2 socket (editor_preview_set) and read back
-- through the menu REST (GET /api/admin/group-menus-v2/{id}) on hydration.
--
-- Idempotent.

CREATE TABLE IF NOT EXISTS `user_menu_preferences` (
  `id` INT NOT NULL AUTO_INCREMENT,
  `user_id` INT NOT NULL,
  `restaurant_id` INT NOT NULL,
  `menu_id` INT NOT NULL,
  `pref_key` VARCHAR(64) NOT NULL,
  `pref_value` VARCHAR(255) NULL,
  `updated_at` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_user_menu_pref` (`user_id`, `restaurant_id`, `menu_id`, `pref_key`),
  KEY `idx_user_menu_pref_menu` (`restaurant_id`, `menu_id`),
  CONSTRAINT `fk_user_menu_pref_user` FOREIGN KEY (`user_id`) REFERENCES `bo_users` (`id`) ON DELETE CASCADE,
  CONSTRAINT `fk_user_menu_pref_restaurant` FOREIGN KEY (`restaurant_id`) REFERENCES `restaurants` (`id`) ON DELETE CASCADE,
  CONSTRAINT `fk_user_menu_pref_menu` FOREIGN KEY (`menu_id`) REFERENCES `menus` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
