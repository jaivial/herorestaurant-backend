INSERT INTO restaurant_websites (
  restaurant_id,
  template_id,
  domain_status,
  is_published,
  created_at,
  updated_at
)
SELECT
  r.id,
  'villa-carmen',
  'pending',
  0,
  NOW(),
  NOW()
FROM restaurants r
WHERE r.id = 1
ON DUPLICATE KEY UPDATE
  template_id = VALUES(template_id),
  updated_at = NOW();
