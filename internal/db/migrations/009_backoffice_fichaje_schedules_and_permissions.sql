-- Backoffice fichaje + horarios (admin scheduling + realtime support).

CREATE TABLE IF NOT EXISTS member_work_schedules (
  id BIGINT NOT NULL AUTO_INCREMENT,
  restaurant_member_id INT NOT NULL,
  restaurant_id INT NOT NULL,
  work_date DATE NOT NULL,
  start_time TIME NOT NULL,
  end_time TIME NOT NULL,
  notes VARCHAR(255) NULL,
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uniq_member_work_schedules_rest_member_date (restaurant_id, restaurant_member_id, work_date),
  KEY idx_member_work_schedules_rest_date (restaurant_id, work_date),
  CONSTRAINT fk_member_work_schedules_member FOREIGN KEY (restaurant_member_id) REFERENCES restaurant_members(id) ON DELETE CASCADE,
  CONSTRAINT fk_member_work_schedules_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Ensure fichaje is available for all backoffice role families and horarios for admin.
INSERT INTO bo_role_permissions (role_slug, section_key, is_allowed) VALUES
  ('admin', 'fichaje', 1),
  ('admin', 'horarios', 1),
  ('metre', 'fichaje', 1),
  ('jefe_cocina', 'fichaje', 1)
ON DUPLICATE KEY UPDATE
  is_allowed = VALUES(is_allowed);
