-- Seed datos realistas para restaurant_id=1 (analytics / estadisticas / e2e).
-- Rango: 2026-07-01 .. 2026-07-30 (ventana del e2e statistics-500.spec.ts).
-- Idempotente: IDs fijos >= 1_000_000, INSERT ... ON DUPLICATE KEY UPDATE.
-- Uso:  mysql ... newvillacarmen < seed_restaurant1_analytics.sql
-- Tras el seed:  POST /api/admin/analytics/refresh {"from":"2026-07-01","to":"2026-07-30"}
--   para reconstruir analytics_daily_rollups desde invoices/pos/stock (el refresh borra
--   rollups del rango y los relee; NO volcar rollups a mano, el refresh los pisa).

-- 1) Invoices de restaurante 1 (240, 2026-07-01..30, estado enviada).
INSERT INTO invoices
  (id, restaurant_id, customer_name, customer_surname, customer_email, customer_dni_cif, customer_phone,
   amount, payment_method, invoice_date, payment_date, status, is_reservation, created_at, updated_at)
WITH RECURSIVE seq AS (SELECT 0 AS n UNION ALL SELECT n + 1 FROM seq WHERE n < 239)
SELECT
  1000000 + n AS id,
  1 AS restaurant_id,
  CONCAT('Cliente Factura ', n) AS customer_name,
  '' AS customer_surname,
  CONCAT('seed.inv.', n, '@example.com') AS customer_email,
  CONCAT('B', LPAD(n + 1, 8, '0')) AS customer_dni_cif,
  CONCAT('600', LPAD(n, 7, '0')) AS customer_phone,
  ROUND(90 + ((n * 97) % 220) + ((n % 5) * 12), 2) AS amount,
  ELT(1 + (n % 3), 'tarjeta', 'bizum', 'transferencia') AS payment_method,
  DATE_ADD('2026-07-01', INTERVAL n DIV 8 DAY) AS invoice_date,
  DATE_ADD('2026-07-01', INTERVAL n DIV 8 DAY) AS payment_date,
  'enviada' AS status,
  0 AS is_reservation,
  NOW() AS created_at,
  NOW() AS updated_at
FROM seq
ON DUPLICATE KEY UPDATE
  amount = VALUES(amount), invoice_date = VALUES(invoice_date), payment_date = VALUES(payment_date), status = VALUES(status);

-- 2) Líneas de invoice (3 por factura; reparto 55/30/15, IVA 10).
INSERT IGNORE INTO invoice_line_items
  (id, invoice_id, description, quantity, unit_price, iva_rate, iva_amount, line_total, sort_order, created_at)
WITH RECURSIVE seq AS (SELECT 0 AS n UNION ALL SELECT n + 1 FROM seq WHERE n < 239),
     ks AS (SELECT 1 AS k UNION ALL SELECT k + 1 FROM ks WHERE k < 3)
SELECT
  1100000 + n * 3 + k AS id,
  1000000 + n AS invoice_id,
  ELT(k, 'Menú degustación', 'Vinos y bebidas', 'Postres y cafés') AS description,
  1 AS quantity,
  ROUND((90 + ((n * 97) % 220) + ((n % 5) * 12)) * ELT(k, 0.55, 0.30, 0.15), 2) AS unit_price,
  10 AS iva_rate,
  ROUND((90 + ((n * 97) % 220) + ((n % 5) * 12)) * ELT(k, 0.55, 0.30, 0.15) * 0.10, 2) AS iva_amount,
  ROUND((90 + ((n * 97) % 220) + ((n % 5) * 12)) * ELT(k, 0.55, 0.30, 0.15), 2) AS line_total,
  k AS sort_order,
  NOW() AS created_at
FROM seq CROSS JOIN ks;

-- 3) Visitas POS de restaurante 1 (450, 2026-07-01..30, cerradas).
INSERT INTO pos_visits
  (id, restaurant_id, channel, table_id, booking_id, service_date, service_type, covers, status,
   opened_by, closed_by, opened_at, closed_at, version, open_idempotency_key, created_at, updated_at)
WITH RECURSIVE seq AS (SELECT 0 AS n UNION ALL SELECT n + 1 FROM seq WHERE n < 449)
SELECT
  2000000 + n AS id,
  1 AS restaurant_id,
  'DINE_IN' AS channel,
  NULL AS table_id,
  NULL AS booking_id,
  DATE_ADD('2026-07-01', INTERVAL n DIV 15 DAY) AS service_date,
  ELT(1 + ((n DIV 15) % 2), 'LUNCH', 'DINNER') AS service_type,
  2 + (n % 5) AS covers,
  'CLOSED' AS status,
  1 AS opened_by,
  1 AS closed_by,
  TIMESTAMP(DATE_ADD('2026-07-01', INTERVAL n DIV 15 DAY), MAKETIME(13 + ((n DIV 15) % 2) * 7, (n * 7) % 59, 0)) AS opened_at,
  TIMESTAMPADD(MINUTE, 95, TIMESTAMP(DATE_ADD('2026-07-01', INTERVAL n DIV 15 DAY), MAKETIME(13 + ((n DIV 15) % 2) * 7, (n * 7) % 59, 0))) AS closed_at,
  1 AS version,
  CONCAT('seed-visit-', n) AS open_idempotency_key,
  NOW() AS created_at,
  NOW() AS updated_at
FROM seq
ON DUPLICATE KEY UPDATE
  service_date = VALUES(service_date), service_type = VALUES(service_type), covers = VALUES(covers), status = VALUES(status), opened_at = VALUES(opened_at), closed_at = VALUES(closed_at);

-- 4) Tickets POS (450, PAID, 45-100 €; 1 ticket por visita).
INSERT INTO pos_tickets
  (id, restaurant_id, visit_id, shift_id, ticket_number, status, subtotal_gross_cents, discount_cents,
   ticket_discount_cents, tax_cents, total_gross_cents, paid_cents, refunded_cents, stock_status,
   creation_idempotency_key, checkout_idempotency_key, opened_by, closed_by, opened_at, paid_at, version, created_at, updated_at)
WITH RECURSIVE seq AS (SELECT 0 AS n UNION ALL SELECT n + 1 FROM seq WHERE n < 449)
SELECT
  3000000 + n AS id,
  1 AS restaurant_id,
  2000000 + n AS visit_id,
  NULL AS shift_id,
  CONCAT('SEED-', LPAD(n + 1, 6, '0')) AS ticket_number,
  'PAID' AS status,
  (4500 + ((n * 139) % 5500)) - ((4500 + ((n * 139) % 5500)) * 10 DIV 110) AS subtotal_gross_cents,
  0 AS discount_cents,
  0 AS ticket_discount_cents,
  (4500 + ((n * 139) % 5500)) * 10 DIV 110 AS tax_cents,
  (4500 + ((n * 139) % 5500)) AS total_gross_cents,
  (4500 + ((n * 139) % 5500)) AS paid_cents,
  0 AS refunded_cents,
  'NOT_APPLICABLE' AS stock_status,
  CONCAT('seed-ticket-', n) AS creation_idempotency_key,
  CONCAT('seed-checkout-', n) AS checkout_idempotency_key,
  1 AS opened_by,
  1 AS closed_by,
  TIMESTAMP(DATE_ADD('2026-07-01', INTERVAL n DIV 15 DAY), MAKETIME(13 + ((n DIV 15) % 2) * 7, (n * 7) % 59, 0)) AS opened_at,
  TIMESTAMPADD(MINUTE, 75, TIMESTAMP(DATE_ADD('2026-07-01', INTERVAL n DIV 15 DAY), MAKETIME(13 + ((n DIV 15) % 2) * 7, (n * 7) % 59, 0))) AS paid_at,
  1 AS version,
  NOW() AS created_at,
  NOW() AS updated_at
FROM seq
ON DUPLICATE KEY UPDATE
  status = VALUES(status), total_gross_cents = VALUES(total_gross_cents), subtotal_gross_cents = VALUES(subtotal_gross_cents), tax_cents = VALUES(tax_cents), paid_cents = VALUES(paid_cents), paid_at = VALUES(paid_at);

-- 5) Líneas de ticket (4 por ticket; reparto 30/35/20/15; productos realistas).
INSERT IGNORE INTO pos_ticket_lines
  (id, restaurant_id, ticket_id, pos_product_id, product_name_snapshot, product_sku_snapshot, quantity,
   unit_price_gross_cents, vat_rate_snapshot, discount_cents, line_total_gross_cents, notes, course, status,
   void_reason, idempotency_key, created_by, voided_by, created_at, updated_at)
WITH RECURSIVE seq AS (SELECT 0 AS n UNION ALL SELECT n + 1 FROM seq WHERE n < 449),
     ks AS (SELECT 1 AS k UNION ALL SELECT k + 1 FROM ks WHERE k < 4)
SELECT
  4000000 + n * 4 + k AS id,
  1 AS restaurant_id,
  3000000 + n AS ticket_id,
  NULL AS pos_product_id,
  ELT(1 + ((n + k) % 6), 'Entrante de la casa', 'Arroz del senyoret', 'Secreto ibérico', 'Postre casero', 'Café o infusión', 'Bebida') AS product_name_snapshot,
  NULL AS product_sku_snapshot,
  1 AS quantity,
  (4500 + ((n * 139) % 5500)) * ELT(k, 30, 35, 20, 15) DIV 100 AS unit_price_gross_cents,
  10 AS vat_rate_snapshot,
  0 AS discount_cents,
  (4500 + ((n * 139) % 5500)) * ELT(k, 30, 35, 20, 15) DIV 100 AS line_total_gross_cents,
  NULL AS notes,
  ELT(1 + (k % 2), 'PRINCIPAL', 'POSTRE') AS course,
  'ACTIVE' AS status,
  NULL AS void_reason,
  CONCAT('seed-line-', n, '-', k) AS idempotency_key,
  1 AS created_by,
  NULL AS voided_by,
  NOW() AS created_at,
  NOW() AS updated_at
FROM seq CROSS JOIN ks;

-- 6) Compras de stock (2/dia, coste conocido; ~30% del ingreso del dia).
INSERT INTO stock_movements
  (id, restaurant_id, stock_item_id, warehouse_id, qty_base, type, waste_reason, entered_qty, entered_unit_id,
   unit_cost, total_cost, ref_type, ref_id, transfer_id, idempotency_key, note, actor_user_id, occurred_at, created_at)
WITH RECURSIVE seq AS (SELECT 0 AS n UNION ALL SELECT n + 1 FROM seq WHERE n < 59)
SELECT
  5000000 + n AS id,
  1 AS restaurant_id,
  20 + ((n DIV 2) * 7 + (n % 2) * 13) % 60 AS stock_item_id,
  1 AS warehouse_id,
  20 + ((n * 11) % 70) AS qty_base,
  'PURCHASE' AS type,
  NULL AS waste_reason,
  20 + ((n * 11) % 70) AS entered_qty,
  20 + ((n DIV 2) * 7 + (n % 2) * 13) % 60 AS entered_unit_id,
  ROUND((240 + ((n DIV 2) * 113 + (n % 2) * 271) % 500) / (20 + ((n * 11) % 70)), 4) AS unit_cost,
  240 + ((n DIV 2) * 113 + (n % 2) * 271) % 500 AS total_cost,
  NULL AS ref_type,
  NULL AS ref_id,
  NULL AS transfer_id,
  CONCAT('seed-purchase-', n) AS idempotency_key,
  'Seed compra' AS note,
  1 AS actor_user_id,
  TIMESTAMP(DATE_ADD('2026-07-01', INTERVAL n DIV 2 DAY), MAKETIME(9 + (n % 2) * 3, (n * 13) % 59, 0)) AS occurred_at,
  NOW() AS created_at
FROM seq
ON DUPLICATE KEY UPDATE
  qty_base = VALUES(qty_base), type = VALUES(type), unit_cost = VALUES(unit_cost), total_cost = VALUES(total_cost), occurred_at = VALUES(occurred_at);

