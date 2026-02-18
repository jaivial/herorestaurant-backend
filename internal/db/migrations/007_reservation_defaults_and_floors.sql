-- Reservation defaults, mesas de tres and restaurant floors (backoffice config v2).

CREATE TABLE IF NOT EXISTS restaurant_reservation_defaults (
  restaurant_id INT NOT NULL,
  opening_mode VARCHAR(16) NOT NULL DEFAULT 'both',
  morning_hours_json LONGTEXT NULL,
  night_hours_json LONGTEXT NULL,
  daily_limit INT NOT NULL DEFAULT 45,
  mesas_de_dos_limit VARCHAR(16) NOT NULL DEFAULT '999',
  mesas_de_tres_limit VARCHAR(16) NOT NULL DEFAULT '999',
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (restaurant_id),
  KEY idx_restaurant_reservation_defaults_restaurant (restaurant_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS mesas_de_tres (
  id INT NOT NULL AUTO_INCREMENT,
  restaurant_id INT NOT NULL DEFAULT 1,
  reservationDate DATE NOT NULL,
  dailyLimit VARCHAR(16) NOT NULL,
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uniq_mesas_de_tres_restaurant_date (restaurant_id, reservationDate),
  KEY idx_mesas_de_tres_restaurant_date (restaurant_id, reservationDate)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS restaurant_floors (
  id INT NOT NULL AUTO_INCREMENT,
  restaurant_id INT NOT NULL,
  floor_number INT NOT NULL,
  floor_name VARCHAR(64) NOT NULL,
  is_ground TINYINT(1) NOT NULL DEFAULT 0,
  is_active TINYINT(1) NOT NULL DEFAULT 1,
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uniq_restaurant_floors_restaurant_number (restaurant_id, floor_number),
  KEY idx_restaurant_floors_restaurant (restaurant_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS restaurant_floor_overrides (
  restaurant_id INT NOT NULL,
  `date` DATE NOT NULL,
  floor_id INT NOT NULL,
  is_active TINYINT(1) NOT NULL DEFAULT 1,
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (restaurant_id, `date`, floor_id),
  KEY idx_restaurant_floor_overrides_restaurant_date (restaurant_id, `date`),
  KEY idx_restaurant_floor_overrides_floor (floor_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

INSERT INTO restaurant_reservation_defaults (
  restaurant_id,
  opening_mode,
  morning_hours_json,
  night_hours_json,
  daily_limit,
  mesas_de_dos_limit,
  mesas_de_tres_limit
)
SELECT
  r.id,
  'both',
  JSON_ARRAY(
    '08:00','08:30','09:00','09:30',
    '10:00','10:30','11:00','11:30',
    '12:00','12:30','13:00','13:30',
    '14:00','14:30','15:00','15:30',
    '16:00','16:30'
  ),
  JSON_ARRAY(
    '17:30','18:00','18:30','19:00',
    '19:30','20:00','20:30','21:00',
    '21:30','22:00','22:30','23:00',
    '23:30','00:00','00:30'
  ),
  45,
  '999',
  '999'
FROM restaurants r
ON DUPLICATE KEY UPDATE
  opening_mode = COALESCE(NULLIF(restaurant_reservation_defaults.opening_mode, ''), VALUES(opening_mode)),
  morning_hours_json = COALESCE(restaurant_reservation_defaults.morning_hours_json, VALUES(morning_hours_json)),
  night_hours_json = COALESCE(restaurant_reservation_defaults.night_hours_json, VALUES(night_hours_json)),
  daily_limit = COALESCE(restaurant_reservation_defaults.daily_limit, VALUES(daily_limit)),
  mesas_de_dos_limit = COALESCE(NULLIF(restaurant_reservation_defaults.mesas_de_dos_limit, ''), VALUES(mesas_de_dos_limit)),
  mesas_de_tres_limit = COALESCE(NULLIF(restaurant_reservation_defaults.mesas_de_tres_limit, ''), VALUES(mesas_de_tres_limit));

INSERT INTO restaurant_floors (restaurant_id, floor_number, floor_name, is_ground, is_active)
SELECT r.id, 0, 'Planta baja', 1, 1
FROM restaurants r
ON DUPLICATE KEY UPDATE
  floor_name = VALUES(floor_name),
  is_ground = VALUES(is_ground),
  is_active = VALUES(is_active);
