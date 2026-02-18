-- Backoffice fichaje + horarios (admin scheduling + realtime support).
-- Idempotent: only runs if required tables exist.

-- Check if bo_role_permissions exists (created in 008)
SET @role_perms_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bo_role_permissions');

-- Insert permissions if table exists
SET @insert_perms := IF(@role_perms_exists = 1, "INSERT INTO bo_role_permissions (role_slug, section_key, is_allowed) VALUES ('admin', 'fichaje', 1), ('admin', 'horarios', 1), ('metre', 'fichaje', 1), ('jefe_cocina', 'fichaje', 1) ON DUPLICATE KEY UPDATE is_allowed = VALUES(is_allowed)", 'SELECT 1');
PREPARE stmt FROM @insert_perms; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- Check if restaurant_members exists (created in 008)
SET @members_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_members');

-- Create member_work_schedules table if restaurant_members exists
SET @create_table := IF(@members_exists = 1, "CREATE TABLE IF NOT EXISTS member_work_schedules (id BIGINT NOT NULL AUTO_INCREMENT, restaurant_member_id INT NOT NULL, restaurant_id INT NOT NULL, work_date DATE NOT NULL, start_time TIME NOT NULL, end_time TIME NOT NULL, notes VARCHAR(255) NULL, created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP, updated_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP, PRIMARY KEY (id), UNIQUE KEY uniq_member_work_schedules_rest_member_date (restaurant_id, restaurant_member_id, work_date), KEY idx_member_work_schedules_rest_date (restaurant_id, work_date)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci", 'SELECT 1');
PREPARE stmt FROM @create_table; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @fk_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLE_CONSTRAINTS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'member_work_schedules' AND CONSTRAINT_NAME = 'fk_member_work_schedules_member');
SET @add_fk := IF(@members_exists = 1 AND @fk_exists = 0, 'ALTER TABLE member_work_schedules ADD CONSTRAINT fk_member_work_schedules_member FOREIGN KEY (restaurant_member_id) REFERENCES restaurant_members(id) ON DELETE CASCADE', 'SELECT 1');
PREPARE stmt FROM @add_fk; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @fk_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLE_CONSTRAINTS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'member_work_schedules' AND CONSTRAINT_NAME = 'fk_member_work_schedules_restaurant');
SET @add_fk2 := IF(@members_exists = 1 AND @fk_exists = 0, 'ALTER TABLE member_work_schedules ADD CONSTRAINT fk_member_work_schedules_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE', 'SELECT 1');
PREPARE stmt FROM @add_fk2; EXECUTE stmt; DEALLOCATE PREPARE stmt;
