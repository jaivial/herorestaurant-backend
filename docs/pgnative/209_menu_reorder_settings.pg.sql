-- Coordination id: menu_reorder_settings_v1 (PG twin of migration 209, apply by hand on prod PG)
CREATE TABLE IF NOT EXISTS menu_reorder_settings (
  restaurant_id integer NOT NULL,
  menu_id bigint NOT NULL,
  sections_reorder_enabled smallint NOT NULL DEFAULT 1,
  dishes_reorder_enabled smallint NOT NULL DEFAULT 1,
  updated_at timestamp NOT NULL DEFAULT (date_trunc('second', statement_timestamp()) AT TIME ZONE 'UTC'),
  PRIMARY KEY (restaurant_id, menu_id)
);

