-- 071: Technical-sheet steps (Receta subtab) and their AI image jobs.
--
-- image_object_path is stored alongside image_url so cleanup can call bunnyDelete
-- directly; deriving a storage path from a CDN URL is fragile.
--
-- The job row is what makes generation survive a hard reload: status is persisted
-- before the provider call, so REST hydration can render a skeleton and the
-- WebSocket only has to deliver the transition.

CREATE TABLE IF NOT EXISTS stock_recipe_steps (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id INT NOT NULL,
    recipe_id BIGINT UNSIGNED NOT NULL,
    step_no INT NOT NULL,
    title VARCHAR(180) NOT NULL,
    description TEXT NULL,
    image_url VARCHAR(1000) NULL,
    image_object_path VARCHAR(1000) NULL,
    generation_status ENUM('NONE','PENDING','RUNNING','READY','FAILED') NOT NULL DEFAULT 'NONE',
    generation_mode ENUM('UPLOAD','AI_ENHANCE','AI_GENERATE') NULL,
    generation_error VARCHAR(500) NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uq_stock_recipe_steps_tenant_id (restaurant_id, id),
    UNIQUE KEY uq_stock_recipe_step_no (restaurant_id, recipe_id, step_no),
    KEY idx_stock_recipe_steps_recipe (restaurant_id, recipe_id, step_no),
    CONSTRAINT fk_stock_recipe_steps_restaurant
      FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE,
    CONSTRAINT fk_stock_recipe_steps_recipe
      FOREIGN KEY (restaurant_id, recipe_id) REFERENCES stock_recipes(restaurant_id, id) ON DELETE CASCADE,
    CONSTRAINT chk_stock_recipe_step_no CHECK (step_no > 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS stock_recipe_step_image_jobs (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id INT NOT NULL,
    step_id BIGINT UNSIGNED NOT NULL,
    mode ENUM('AI_ENHANCE','AI_GENERATE') NOT NULL,
    status ENUM('PENDING','RUNNING','SUCCEEDED','FAILED','CANCELLED') NOT NULL DEFAULT 'PENDING',
    prompt VARCHAR(2000) NULL,
    idempotency_key VARCHAR(120) NOT NULL,
    provider_request_id VARCHAR(120) NULL,
    result_object_path VARCHAR(1000) NULL,
    error_message VARCHAR(500) NULL,
    actor_user_id INT NOT NULL,
    started_at DATETIME NULL,
    finished_at DATETIME NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uq_step_image_job_idem (restaurant_id, idempotency_key),
    KEY idx_step_image_jobs_step (restaurant_id, step_id, status),
    KEY idx_step_image_jobs_stuck (status, started_at),
    CONSTRAINT fk_step_image_jobs_restaurant
      FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE,
    CONSTRAINT fk_step_image_jobs_step
      FOREIGN KEY (restaurant_id, step_id) REFERENCES stock_recipe_steps(restaurant_id, id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
