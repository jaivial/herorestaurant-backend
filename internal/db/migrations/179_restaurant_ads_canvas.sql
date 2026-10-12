-- Coordination id: ads_canvas_v1
-- Free-form Figma-like ad canvas. canvas_json carries the document
-- {version,width,height,background,elements[]}; NULL keeps the legacy
-- structured ad (content_json/ctas_json) so existing rows need no backfill.
ALTER TABLE restaurant_ads ADD COLUMN canvas_json JSON NULL AFTER layout_json;
