-- app_norm: native replacement for MySQL utf8mb4_unicode_ci equality/uniqueness on text keys (PAD SPACE, case, accents).
-- Immutable so it can back UNIQUE expression indexes. Requires: CREATE EXTENSION unaccent; database encoding UTF8.
-- Verified on the scratch (U1, 2026-11-13) against MySQL 8.0.45 utf8mb4_unicode_ci: see u1-app-norm-audit.md
CREATE EXTENSION IF NOT EXISTS unaccent;
CREATE SCHEMA IF NOT EXISTS app;
CREATE OR REPLACE FUNCTION app.norm(x text) RETURNS text
LANGUAGE sql IMMUTABLE PARALLEL SAFE STRICT AS
$$ SELECT lower(public.unaccent('public.unaccent'::regdictionary,
        regexp_replace(normalize(rtrim(x), NFKC), E'[\\u200B-\\u200D\\u2060\\uFEFF]', '', 'g'))) $$;
-- ASCII-only identifiers (emails, slugs, idempotency keys, tokens): cheaper, case + trailing-space only.
CREATE OR REPLACE FUNCTION app.norm_ascii(x text) RETURNS text
LANGUAGE sql IMMUTABLE PARALLEL SAFE STRICT AS $$ SELECT lower(rtrim(x)) $$;
