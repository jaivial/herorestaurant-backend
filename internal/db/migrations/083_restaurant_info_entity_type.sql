-- Per-restaurant tax entity type used by the fiscal simulator.
-- Values match the backoffice EntityType: autonomo, sl, sl_new, sl_micro, sa.
-- 'sl' is the default for existing records (sociedad limitada, tipo general).
ALTER TABLE restaurant_info
  ADD COLUMN tipo_empresa VARCHAR(24) NOT NULL DEFAULT 'sl' AFTER clasificacion;
