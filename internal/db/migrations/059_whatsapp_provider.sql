-- 059: WhatsApp provider column for the server pool.
--
-- Adds a provider selector so the multi-tenant bot can use a self-hosted
-- Evolution API host instead of UAZAPI. Existing rows default to 'uazapi'
-- (no behavior change).
--
-- Column-reuse semantics for provider='evolution':
--   uazapi_servers.admin_token           -> Evolution global API key (apikey header)
--   restaurant_uazapi_instances.instance_token       -> Evolution instance hash/token
--   restaurant_uazapi_instances.provider_instance_id -> Evolution instanceName (routing key)
ALTER TABLE uazapi_servers
  ADD COLUMN provider VARCHAR(16) NOT NULL DEFAULT 'uazapi' AFTER name;
