-- Restaurant integrations: add notification recipients to avoid hardcoded staff numbers.

ALTER TABLE restaurant_integrations
  ADD COLUMN restaurant_whatsapp_numbers_json JSON DEFAULT NULL AFTER uazapi_token;

-- Seed VillaCarmen staff numbers (idempotent, keeps existing value if already set).
INSERT INTO restaurant_integrations (restaurant_id, restaurant_whatsapp_numbers_json)
VALUES (1, JSON_ARRAY('34692747052', '34638857294', '34686969914'))
ON DUPLICATE KEY UPDATE
  restaurant_whatsapp_numbers_json = IFNULL(restaurant_whatsapp_numbers_json, VALUES(restaurant_whatsapp_numbers_json));

