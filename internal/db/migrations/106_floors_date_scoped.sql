-- Date-scoped floors: a floor created for a specific date only exists on that
-- date (e.g. an extra room level set up for an event). Global floors keep
-- specific_date NULL. Mirrors 104_salons_date_scoped.sql: scope_key normalizes
-- NULL (global) into a sentinel so the unique index also covers global
-- duplicates, and a date-scoped floor with the same floor_number shadows the
-- global one on that date.
--
-- Per-date activation overrides keep living in restaurant_floor_overrides
-- (floor_id based), which works for both global and date-scoped rows.

ALTER TABLE restaurant_floors
  ADD COLUMN specific_date DATE NULL DEFAULT NULL AFTER is_active;

ALTER TABLE restaurant_floors
  ADD COLUMN scope_key VARCHAR(10)
    GENERATED ALWAYS AS (IFNULL(DATE_FORMAT(specific_date, '%Y-%m-%d'), 'global')) STORED;

ALTER TABLE restaurant_floors
  DROP INDEX uniq_restaurant_floors_restaurant_number;

ALTER TABLE restaurant_floors
  ADD UNIQUE KEY uniq_restaurant_floors_scope (restaurant_id, floor_number, scope_key);
