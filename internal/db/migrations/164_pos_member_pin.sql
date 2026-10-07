
-- Wrong-PIN throttling lives on the terminal (the signed-in user), not on the
-- member: bcrypt salts every hash, so a wrong PIN cannot honestly be attributed
-- to the person it was meant for.
CREATE TABLE pos_pin_attempts (
  restaurant_id INT NOT NULL,
  attempted_by INT NOT NULL,
  failed_attempts INT NOT NULL DEFAULT 0,
  locked_until DATETIME NULL,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (restaurant_id, attempted_by),
  CONSTRAINT fk_pos_pin_attempts_member FOREIGN KEY (restaurant_id, attempted_by) REFERENCES restaurant_members (restaurant_id, id)
);
