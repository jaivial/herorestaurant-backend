CREATE TABLE IF NOT EXISTS restaurant_ads (
    id BIGINT NOT NULL AUTO_INCREMENT,
    restaurant_id INT NOT NULL,
    name VARCHAR(160) NOT NULL DEFAULT 'Nuevo anuncio',
    active TINYINT(1) NOT NULL DEFAULT 0,
    content_json JSON NOT NULL,
    ctas_json JSON NOT NULL,
    created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    KEY idx_restaurant_ads_restaurant (restaurant_id, updated_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
