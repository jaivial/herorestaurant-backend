-- Structured Forky audit trail. Keep secrets out of arguments_json at call sites.
CREATE TABLE IF NOT EXISTS assistant_tool_audit (
 id BIGINT NOT NULL AUTO_INCREMENT,
 user_id INT NULL, restaurant_id INT NOT NULL, assistant_session_id BIGINT NULL,
 tool_name VARCHAR(128) NOT NULL, tool_version VARCHAR(32) NOT NULL DEFAULT '1',
 arguments_json JSON NULL, entity_type VARCHAR(64) NULL, entity_id VARCHAR(64) NULL,
 result_summary JSON NULL, error_message VARCHAR(1000) NULL, correlation_id VARCHAR(128) NULL,
 duration_ms BIGINT NOT NULL DEFAULT 0, created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
 PRIMARY KEY(id), KEY idx_assistant_audit_restaurant_created(restaurant_id,created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
