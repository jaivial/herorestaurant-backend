-- Date-scoped salons: a salon created for a specific date only exists on that
-- date (e.g. an extra room set up for an event). Global salons keep
-- specific_date NULL. A date-scoped salon with the same name as a global one
-- shadows it on that date. scope_key normalizes NULL (global) into a sentinel
-- so the unique index also covers global duplicates.

ALTER TABLE restaurant_salons
  ADD COLUMN specific_date DATE NULL DEFAULT NULL AFTER display_order;

ALTER TABLE restaurant_salons
  ADD COLUMN scope_key VARCHAR(10)
    GENERATED ALWAYS AS (IFNULL(DATE_FORMAT(specific_date, '%Y-%m-%d'), 'global')) STORED;

ALTER TABLE restaurant_salons
  DROP INDEX uniq_restaurant_salons_restaurant_floor_name;

ALTER TABLE restaurant_salons
  ADD UNIQUE KEY uniq_restaurant_salons_scope (restaurant_id, floor_id, name, scope_key);
