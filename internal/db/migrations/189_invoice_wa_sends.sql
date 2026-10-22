-- Coordination id: invoice_send_whatsapp_v1
--
-- Durable idempotency for operator-initiated invoice WhatsApp sends: one row
-- per send attempt, keyed by (restaurant_id, idempotency_key). Each stage
-- (text, pdf) is marked inflight BEFORE the provider call: the gateway has
-- no provider-side idempotency, so a crash between the provider call and the
-- stage persist leaves the stage inflight and a same-key retry reports it as
-- uncertain (stage_uncertain) instead of risking a duplicate customer
-- message. Stage statuses: pending, inflight (claimed, provider call started
-- or crashed mid-request), sent, error (demonstrably pre-send failure,
-- automatically retryable), unknown (provider call failed, delivery
-- uncertain, explicit operator retry only). Exactly-once is NOT claimed.
-- Rollback: disable the feature by removing its routes/handlers; this table
-- is audit data and is preserved — never DROP it as a "rollback".
CREATE TABLE IF NOT EXISTS `invoice_wa_sends` (
  `id` BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
  `restaurant_id` INT NOT NULL,
  `invoice_id` INT NOT NULL,
  `idempotency_key` VARCHAR(80) NOT NULL,
  `phone` VARCHAR(32) NOT NULL DEFAULT '',
  `text_status` VARCHAR(16) NOT NULL DEFAULT 'pending',
  `pdf_status` VARCHAR(16) NOT NULL DEFAULT 'pending',
  `pdf_url` TEXT NULL,
  `stage_error` VARCHAR(500) NOT NULL DEFAULT '',
  `created_at` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  UNIQUE KEY `uq_invoice_wa_sends_key` (`restaurant_id`, `idempotency_key`),
  KEY `idx_invoice_wa_sends_invoice` (`invoice_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
