-- Registered OAuth callbacks per dynamic MCP client (RFC 7591).
-- Coordination id: mcp_server_v1
--
-- Claude and Gemini both register a redirect_uri of their own and refuse to
-- start the authorization flow when the registration response omits it. The
-- value is stored as a single space-separated column, which matches the
-- mcp_oauth_clients table style already in use (label-like, null when absent)
-- and needs no join table for a list that is only ever read as a set.
--
-- 'SELF' means the client declared no callback: the consent screen then
-- redirects the code through Instatic itself. A NULL or empty value means the
-- client predates this column, and any redirect_uri is accepted for it.
ALTER TABLE mcp_oauth_clients
    ADD COLUMN redirect_uris VARCHAR(1024) NULL DEFAULT NULL AFTER name;
