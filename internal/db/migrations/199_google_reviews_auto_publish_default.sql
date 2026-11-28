-- Coordination id: google_reviews_quality_v1
--
-- Auto-publish starts disabled: every AI reply waits for the owner's approval
-- until they choose a threshold in Configuración → Reseñas Google. Existing
-- rows keep the value the owner already saved.
ALTER TABLE restaurant_google_business ALTER COLUMN auto_publish_min_stars SET DEFAULT 0;
