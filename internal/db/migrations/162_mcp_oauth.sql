-- MCP server (Model Context Protocol) with OAuth 2.1 authorization-code flow.
-- Coordination id: mcp_server_v1
--
-- mcp_oauth_clients: the ChatGPT connector is registered as a public client.
-- We store only the client id; there is no client secret because a public
-- client cannot keep one, and PKCE (S256) is what actually protects the
-- authorization code in transit.
--
-- mcp_oauth_codes: single-use authorization codes. The code is stored as a
-- SHA-256 digest and consumed on the token exchange, so a leaked code cannot
-- be replayed and the plaintext code never touches the database.
--
-- mcp_oauth_tokens: issued access tokens, again stored only as a digest. Each
-- token is bound to one backoffice user AND one restaurant, and inherits that
-- user's role, so an MCP session can never exceed the operator's own ACL.
CREATE TABLE IF NOT EXISTS mcp_oauth_clients (
    id BIGINT NOT NULL AUTO_INCREMENT,
    client_id VARCHAR(128) NOT NULL,
    name VARCHAR(128) NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uq_mcp_oauth_clients_client_id (client_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS mcp_oauth_codes (
    id BIGINT NOT NULL AUTO_INCREMENT,
    code_hash CHAR(64) NOT NULL,
    client_id VARCHAR(128) NOT NULL,
    user_id INT NOT NULL,
    restaurant_id INT NOT NULL,
    redirect_uri VARCHAR(512) NOT NULL,
    code_challenge VARCHAR(128) NOT NULL,
    code_challenge_method VARCHAR(10) NOT NULL DEFAULT 'S256',
    scope VARCHAR(255) NULL,
    expires_at TIMESTAMP NOT NULL,
    used_at TIMESTAMP NULL DEFAULT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uq_mcp_oauth_codes_hash (code_hash),
    KEY idx_mcp_oauth_codes_expiry (expires_at),
    CONSTRAINT fk_mcp_oauth_codes_user FOREIGN KEY (user_id) REFERENCES bo_users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS mcp_oauth_tokens (
    id BIGINT NOT NULL AUTO_INCREMENT,
    token_hash CHAR(64) NOT NULL,
    client_id VARCHAR(128) NOT NULL,
    user_id INT NOT NULL,
    restaurant_id INT NOT NULL,
    scope VARCHAR(255) NULL,
    expires_at TIMESTAMP NOT NULL,
    revoked_at TIMESTAMP NULL DEFAULT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uq_mcp_oauth_tokens_hash (token_hash),
    KEY idx_mcp_oauth_tokens_expiry (expires_at),
    KEY idx_mcp_oauth_tokens_client (client_id),
    CONSTRAINT fk_mcp_oauth_tokens_user FOREIGN KEY (user_id) REFERENCES bo_users(id) ON DELETE CASCADE,
    CONSTRAINT fk_mcp_oauth_tokens_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
