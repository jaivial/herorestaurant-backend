ALTER TABLE restaurant_ads
    ADD COLUMN starts_at DATE NULL AFTER active,
    ADD COLUMN ends_at DATE NULL AFTER starts_at;
