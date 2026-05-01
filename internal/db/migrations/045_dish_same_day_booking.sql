CREATE TABLE IF NOT EXISTS dish_same_day_booking (
  id BIGINT NOT NULL AUTO_INCREMENT,
  dish_id BIGINT NOT NULL,
  menu_id INT NOT NULL,
  restaurant_id INT NOT NULL,
  state TINYINT NOT NULL DEFAULT 1,
  date_modified TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  user_id INT NOT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY idx_dish_same_day_booking_dish_menu (dish_id, menu_id),
  KEY idx_dish_same_day_booking_dish (dish_id),
  KEY idx_dish_same_day_booking_menu (menu_id),
  KEY idx_dish_same_day_booking_restaurant (restaurant_id),
  CONSTRAINT fk_dish_same_day_booking_dish FOREIGN KEY (dish_id) REFERENCES group_menu_section_dishes_v2(id) ON DELETE CASCADE,
  CONSTRAINT fk_dish_same_day_booking_menu FOREIGN KEY (menu_id) REFERENCES menus(id) ON DELETE CASCADE,
  CONSTRAINT fk_dish_same_day_booking_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE,
  CONSTRAINT fk_dish_same_day_booking_user FOREIGN KEY (user_id) REFERENCES bo_users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
