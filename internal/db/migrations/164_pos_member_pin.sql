
-- Wrong-PIN throttling lives on the terminal (the signed-in user), not on the
-- member: bcrypt salts every hash, so a wrong PIN cannot honestly be attributed
-- to the person it was meant for.
-- Keyed on the backoffice user who is signed in at this terminal, NOT on a
-- restaurant_members row: most staff records have no bo_user_id at all, so a
-- member-keyed throttle would be unreferenced for almost everybody and the FK
-- would reject the very inserts it exists to record.
CREATE TABLE IF NOT EXISTS pos_pin_attempts (
  restaurant_id INT NOT NULL,
  attempted_by BIGINT NOT NULL,
  failed_attempts INT NOT NULL DEFAULT 0,
  locked_until DATETIME NULL,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (restaurant_id, attempted_by)
);
