-- 073: Audit + idempotency record for the bulk product/stock link wizard.
--
-- WHY THIS TABLE EXISTS
-- ---------------------
-- The wizard links many comida products to technical sheets in one submission.
-- Two problems have to be solved together:
--
--   1. A retry (double click, flaky connection, browser resend) must not apply
--      the batch twice. The natural key for "the same submission" is a client
--      supplied idempotency key.
--   2. A bulk change to the menu is exactly the kind of edit someone will later
--      ask about ("who linked all the desserts?"). Without a record, the only
--      evidence is the final state.
--
-- idempotency_key is NOT NULL so UNIQUE (restaurant_id, idempotency_key) is a
-- TOTAL key. MySQL treats NULLs as distinct in unique indexes, so a NULLable
-- column would silently allow unlimited duplicate "no key" rows and defeat the
-- guarantee this table exists to provide.

CREATE TABLE IF NOT EXISTS comida_bulk_link_batches (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id INT NOT NULL,
    idempotency_key VARCHAR(120) NOT NULL,
    -- What was applied, kept verbatim so the audit does not depend on
    -- reconstructing intent from the current state of the menu.
    links_json JSON NOT NULL,
    linked_count INT NOT NULL DEFAULT 0,
    actor_user_id INT NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uq_comida_bulk_link_idem (restaurant_id, idempotency_key),
    KEY idx_comida_bulk_link_created (restaurant_id, created_at),
    CONSTRAINT fk_comida_bulk_link_restaurant FOREIGN KEY (restaurant_id)
        REFERENCES restaurants (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
