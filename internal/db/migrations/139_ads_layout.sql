-- Coordination id: ads_layout_v1
-- A multiple anuncio is a step wizard: layout_json carries the mode
-- ("unico" | "multiple") and the steps. NULL keeps the classic single
-- announcement so existing rows need no backfill.
ALTER TABLE restaurant_ads ADD COLUMN layout_json JSON NULL AFTER ctas_json;
