-- 203_comida_items_description_enabled.sql
-- Coordination id: comida_description_toggle_v1
--
-- Per-item toggle for the carta (platos, bebidas, cafés), mirroring
-- group_menu_section_dishes_v2.description_enabled (migration 112): the
-- description text is kept, and the switch decides whether the public site
-- shows it. Default 1 keeps every existing description visible.

ALTER TABLE comida_items
    ADD COLUMN description_enabled TINYINT(1) NOT NULL DEFAULT 1 AFTER descripcion;
