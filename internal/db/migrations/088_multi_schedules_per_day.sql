-- Allow multiple work schedules per member per day (multi-shift days).
-- The previous unique key (restaurant_id, restaurant_member_id, work_date)
-- forced exactly one schedule per day; it is replaced by a non-unique index
-- so a member can have morning, afternoon and evening shifts on the same day.
-- Overlap validation happens at the API layer. Idempotent.

SET @uniq_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'member_work_schedules' AND INDEX_NAME = 'uniq_member_work_schedules_rest_member_date');
SET @drop_uniq := IF(@uniq_exists > 0, 'ALTER TABLE member_work_schedules DROP INDEX uniq_member_work_schedules_rest_member_date', 'SELECT 1');
PREPARE stmt FROM @drop_uniq; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @idx_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'member_work_schedules' AND INDEX_NAME = 'idx_member_work_schedules_member_date');
SET @add_idx := IF(@idx_exists = 0, 'ALTER TABLE member_work_schedules ADD INDEX idx_member_work_schedules_member_date (restaurant_id, restaurant_member_id, work_date)', 'SELECT 1');
PREPARE stmt FROM @add_idx; EXECUTE stmt; DEALLOCATE PREPARE stmt;
