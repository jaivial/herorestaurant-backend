-- Per-dish toggle for showing/hiding the description on public displays
-- (/menusdegrupos, /menu/:id). The backoffice menu editor already sends
-- description_enabled per dish; until now the backend dropped it.

ALTER TABLE group_menu_section_dishes_v2
    ADD COLUMN description_enabled TINYINT(1) NOT NULL DEFAULT 1 AFTER description_snapshot;
