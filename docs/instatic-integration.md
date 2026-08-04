# Instatic integration contract (headless, from Go)

Vendored: `backend/vendor/instatic` @ `c6481c08` (v0.0.14+14). Bun `>=1.3 <1.4`.
One instance per restaurant. SQLite file DB. `POST /admin/api/cms/setup` one-shot bootstrap.

## Boot
```
PORT=<free> DATABASE_URL="sqlite:<path>.db" UPLOADS_DIR=<dir> bun run dev:server
```
Health: `GET /healthz` → 200 when listening.

## Setup (one-shot, per instance)
- `GET /admin/api/cms/setup/status` → `{ hasSite, hasAdmin, hasOwner, needsSetup }`
- `POST /admin/api/cms/setup` `{ siteName, email, password(>=12), displayName? }` → `201 {"ok":true}`
- After setup: disable step-up for headless publish (owner `step_up_auth_mode='required'` default):
  `UPDATE users SET step_up_auth_mode='disabled' WHERE email=<owner>`

## Auth
- `POST /admin/api/cms/login` `{ email, password }` → `200` + `Set-Cookie: instatic_admin_session=...`
- Persist cookie; send as `Cookie` header on all admin calls.

## Site document (shell + pages replace)
- `GET /admin/api/cms/site-document` → full SiteDocument JSON (shell + pages + components)
- `PUT /admin/api/cms/site-document` `{ mode: 'replace', ...SiteDocument }` → replaces shell+content in one tx
  (see `server/handlers/cms/siteDocument.ts`)

## Publish
- `POST /admin/api/cms/publish` `{}` → `{ publishedPages, ... }` (needs step-up disabled, cap `pages.publish`)
- `GET /admin/api/cms/publish/status` → freshness
- Output: `UPLOADS_DIR/published/current/<route>.html` (static), CSS at `/_instatic/css/*.css`

## Public serving
- `GET /` → baked home; `/admin/*` → editor SPA. Any host works (no host-based routing).

## Notes / gotchas
- Repos: `server/repositories/*`; publish: `server/publish/publishSite.ts` (`publishDraftSite`).
- Site shell single row `id='default'` in `site` table (`server/repositories/site.ts`).
- Content = `data_tables` + `data_rows` (`pages` system table has body = `pageTree`).
- New tables/columns = additive migrations in BOTH `migrations-pg.ts` + `migrations-sqlite.ts`.
- Architecture gate tests in `src/__tests__/architecture/*.test.ts` must stay green (`bun test`).
- Verified 2026-08-01: setup, login, step-up disable, publish, public HTML all OK on port 39001.
