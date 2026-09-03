# Backend Endpoints (Go)

The Go server mounts the API router under `/api/*` (primary).

For migration compatibility, a set of legacy endpoints are also exposed at the root path without the `/api` prefix (see `backend/cmd/server/main.go`). This is limited to legacy-style endpoints (mostly `*.php` and legacy backend folders) to avoid colliding with SPA routes like `/vinos`.

## Auth (Admin)

Admin endpoints are protected by `ADMIN_TOKEN`:

- If `ADMIN_TOKEN` is set (non-empty), requests must include:
  - `X-Admin-Token: <token>` or
  - `Authorization: Bearer <token>`
- If `ADMIN_TOKEN` is empty, admin endpoints are not gated (dev convenience).

Note: the new Backoffice API under `/api/admin/*` does **not** use `ADMIN_TOKEN`; it uses a cookie-based session (see next section).

## Auth (Backoffice Session)

The new React SSR backoffice uses a cookie-based session (`bo_session`) and lives under `/api/admin/*`.

- `POST /api/admin/login` sets `Set-Cookie: bo_session=...` (Secure, HttpOnly, SameSite=Lax).
- Subsequent `/api/admin/*` requests must send the cookie.
- `POST /api/admin/logout` clears the cookie.
- Sliding expiration is enabled:
  - Default session TTL is `21h` (configurable with `BO_SESSION_TTL_*` envs).
  - High-security routes/pages can use a shorter TTL (default `30m`, configurable with `BO_SESSION_HIGH_SECURITY_TTL_MINUTES` and `BO_SESSION_HIGH_SECURITY_PATH_PREFIXES`).
  - Each authenticated response includes `moving_expiration_date` (RFC3339 UTC) with the renewed expiration timestamp.

## Conventions

- Most endpoints return JSON with either:
  - `{ "success": true, ... }` / `{ "success": false, "message": "..." }`, or
  - legacy `{ "status": "success|error|warning", ... }` for some admin UIs.
- Legacy form endpoints usually accept `multipart/form-data` (FormData) and also `application/x-www-form-urlencoded`.
- Some endpoints accept `application/json` bodies where the legacy JS sends JSON.

## Public booking details

### `GET /api/public/booking?id=<booking_id>`

Returns booking details used by confirmation/cancellation pages:

```json
{
  "success": true,
  "booking": {
    "id": 123,
    "reservationDate": "15/07/2026",
    "reservationTime": "14:00",
    "partySize": 4,
    "adults": 3,
    "children": 1,
    "customerName": "...",
    "arrozDisplay": "Arroz del senyoret x 2",
    "menuDisplay": "Menú de grupo",
    "principales": [{ "name": "Solomillo", "servings": 2 }],
    "commentary": "...",
    "babyStrollers": 0,
    "highChairs": 1,
    "floorDisplay": "Planta 2",
    "tableNumber": "12",
    "status": "confirmed",
    "isSameDay": false,
    "isConfirmed": true
  },
  "riceOptions": []
}
```

Optional/empty fields use `omitempty`; counts remain numeric.

## Auth (Internal / n8n)

Some internal automation endpoints (ported from the legacy PHP automation scripts) require `INTERNAL_API_TOKEN`:

- Header: `X-Api-Token: <token>`
- If `INTERNAL_API_TOKEN` is empty/unset, access is denied (mirrors legacy PHP security behavior).

---

## Backoffice (React SSR) API (`/api/admin/*`)

These endpoints are consumed by the new backoffice UI (`backoffice/`) via the `/api/admin` proxy in `backoffice/server/index.ts`.

RBAC:
- All `/api/admin/*` routes use cookie session + role permissions.
- Sections: `reservas`, `menus`, `ajustes`, `miembros`, `fichaje`, `horarios`.
- Role defaults:
  - `root`: `reservas`, `menus`, `ajustes`, `miembros`, `fichaje`, `horarios`
  - `admin`: `reservas`, `menus`, `ajustes`, `miembros`, `fichaje`, `horarios`
  - `metre`, `jefe_cocina`: `reservas`, `menus`, `fichaje`
  - Resto: `fichaje`
- `estadisticas` section is enabled by default only for `root` and `admin`.
- Jerarquía de importancia (0-100):
  - `root = 100`, `admin = 90`, resto por debajo.
- Sección de miembros/roles:
  - Los endpoints de miembros y roles requieren importancia `>= 90` (admin/root).

### `GET /api/admin/analytics/overview`

Requires backoffice session with `estadisticas` access, default `root`/`admin` only. Query parameters: required `from` and `to` (`YYYY-MM-DD`), optional `granularity` (`day`, `week`, `month`, `quarter`, `year`, default `day`), optional `compare=previous`.

Returns EUR-only summary with invoiced and POS revenue separated, previous-period comparison, zero-filled series, recipe/production waste breakdown, and cost coverage. Unknown costs use `null` plus `N/D` labels and coverage counters. Non-EUR invoice documents are excluded from revenue and counted in `dataQuality`.

### `POST /api/admin/analytics/refresh`

Requires same statistics access. JSON body: `{ "from": "YYYY-MM-DD", "to": "YYYY-MM-DD" }`. Rebuilds selected tenant/date range idempotently from invoices, POS, stock, and production source tables. Response includes `runId`, row count, and `outbox: false`. Refresh is explicit because V1 has no source-table outbox.

### `POST /api/admin/login`
Body (JSON):
- `identifier` (string, recomendado; acepta email o username)
- `email` (string, compat legacy)
- `password` (string)

Response:
- `{ success: true, session: { user, restaurants, activeRestaurantId } }`
- `session.user` incluye `role`, `roleImportance`, `sectionAccess`, `username?` y `mustChangePassword`.
- `{ success: false, message: string }`
- On success also returns `moving_expiration_date` and refreshes `bo_session` cookie expiry.

### `POST /api/admin/logout`
Response:
- `{ success: true }`

### `GET /api/admin/me`
Response:
- `{ success: true, session: { user, restaurants, activeRestaurantId, preferences } }`
- `session.user` incluye `role`, `roleImportance`, `sectionAccess`, `username?` y `mustChangePassword`.
- `session.preferences`: `map<string,string>` de preferencias UI del usuario en su restaurante activo (clave hoy: `reservasDisplayMode` = `tabla` | `grid`). Best-effort: `{}` si falla la lectura.
- Also returns `moving_expiration_date` and refreshes `bo_session` cookie expiry.

Session validation loads role importance and active member in the main session query. The moving
expiration heartbeat is persisted at most once per minute; responses between heartbeats return the
current `X-Moving-Expiration-Date` without rewriting the session row or cookie.

### `PUT /api/admin/me/preferences`
Requires backoffice session. Persiste una preferencia UI del usuario en su restaurante activo.
Body (JSON): `key` (p. ej. `reservasDisplayMode`), `value` (p. ej. `tabla` | `grid`). Solo se aceptan claves/valores en lista blanca; los valores se normalizan a minúsculas.
Response: `{ success: true, preferences: { ... } }` o `{ success: false, message }`.

### `GET /healthz`
Unauthenticated readiness check. Uses a one-second DB ping timeout.

Responses:
- `200`: `{ success: true }`
- `503`: `{ success: false, message: "Database unavailable" }`

### `GET /api/admin/dashboard/metrics`
Requires backoffice session and `reservas` access.

Query:
- `date` (required, `YYYY-MM-DD`) for booking metrics.

Response:
- `{ success: true, metrics, invoiceMetrics }`
- `metrics`: booking totals for `date`.
- `invoiceMetrics`: `{ pendingCount, pendingAmount, monthIncome, weekSentCount }` when current role
  has `facturas` access; otherwise `null`.
- Invoice metrics are DB aggregates; invoice rows are not transferred.

### `POST /api/admin/me/password`
Set password for current authenticated backoffice user.

Body (JSON):
- `password` (string)
- `confirmPassword` (string) (alias legacy: `passwordRepeat`)

Response:
- `{ success: true }`
- `{ success: false, message }`

### `POST /api/admin/active-restaurant`
Body (JSON):
- `restaurantId` (number)

Response:
- `{ success: true, activeRestaurantId: number, role: string, roleImportance: number, sectionAccess: string[] }`

### `GET /api/admin/members`
List active members for the active restaurant.

Response:
- `{ success: true, members: Member[] }`

`Member`:
- `id` (number)
- `firstName` (string)
- `lastName` (string)
- `email` (string|null)
- `dni` (string|null)
- `bankAccount` (string|null)
- `phone` (string|null)
- `photoUrl` (string|null)
- `weeklyContractHours` (number)

### `POST /api/admin/members`
Create member.

Body (JSON):
- `firstName` (string, required)
- `lastName` (string, required)
- `roleSlug` (string, required in new flow; fallback `admin`)
- Optional: `email`, `dni`, `bankAccount`, `phone`, `photoUrl`
- Optional: `username`, `temporaryPassword`
- Optional: `weeklyContractHours` (number, default `40`)

Behavior:
- Con `email` y/o `phone`: crea/vincula `bo_users`, asigna rol y genera invitación (token de un solo uso).
- Sin `email` ni `phone`: exige `username` + `temporaryPassword`, crea usuario manual con `must_change_password=1`.
- ACL: el `roleSlug` debe ser de importance estrictamente inferior a la del actor (un admin no puede crear admin/root; con omisión el fallback `admin` queda sujeto a la misma regla).

Response:
- `{ success: true, member: Member, user?, role?, invitation?, provisioning? }`

### `POST /api/admin/members/{id}/invitation/resend`
Regenera invitación para un miembro activo.

Behavior:
- Invalida tokens activos anteriores del mismo miembro.
- Requiere que el miembro tenga al menos email o teléfono.
- ACL: no permite reenviar invitaciones cuyo rol sea igual o superior al del actor.

Response:
- `{ success: true, member: { id, boUserId, username? }, invitation: { expiresAt, delivery[] } }`
- `{ success: false, message }`

### `POST /api/admin/members/{id}/password-reset/send`
Genera y envía enlace de restablecimiento de password para un miembro.

Behavior:
- Invalida tokens activos anteriores de reset del mismo miembro.
- Requiere que el miembro tenga al menos email o teléfono.

Response:
- `{ success: true, reset: { expiresAt, delivery[] } }`
- `{ success: false, message }`

### `GET /api/admin/members/{id}`
Get member detail.

Response:
- `{ success: true, member: Member }`

### `PATCH /api/admin/members/{id}`
Update member fields and/or contract weekly hours.

ACL:
- Solo se puede editar un miembro de rol estrictamente inferior al del actor (importance menor); el propio perfil siempre es editable.

Response:
- `{ success: true, member: Member }`

### `DELETE /api/admin/members/{id}`
Soft-delete de miembro (`is_active = 0`) y corte de acceso al restaurante activo.

Behavior (transaccional):
- `restaurant_members.is_active = 0`.
- Elimina la fila `bo_user_restaurants` del usuario → su sesión activa deja de resolver rol para este restaurante (logout efectivo).
- Invalida invitaciones pendientes (`invalidated_reason = 'member_deleted'`).
- Invalida resets de contraseña pendientes (`invalidated_reason = 'member_deleted'`).

ACL (jerarquía por `bo_roles.importance`, defaults root=100, admin=90, ...):
- Gate de ruta: sesión + sección `miembros` + importance >= 90 (root/admin).
- El actor debe superar ESTRICTAMENTE al rol del miembro: no se puede eliminar un igual ni un superior, solo roles inferiores.
- No se puede eliminar el propio miembro.

Response:
- `{ success: true, message: "Miembro eliminado" }`
- `{ success: false, message }` (200) si falla la jerarquía o el auto-borrado; 404 si no existe.

### `GET|POST /api/admin/members/{id}/compensations`
Admin-only, tenant-scoped effective-dated compensation history.

- `GET`: `{ success, items: [{ id, payType, grossAmount, monthlyHours, employerCostPct, effectiveHourlyCost, effectiveFrom, effectiveTo, notes }] }`
- `POST` body: `{ payType: "MONTHLY"|"HOURLY", grossAmount, monthlyHours?, employerCostPct, effectiveFrom, effectiveTo?, notes? }`
- Effective periods cannot overlap for one member.
- `effectiveHourlyCost = gross/monthlyHours * (1 + employerCostPct/100)` for monthly pay, or hourly gross with same employer burden.

### `PATCH|DELETE /api/admin/members/{id}/compensations/{compensationId}`
Admin-only update/soft-delete. Every create/update/delete writes immutable audit snapshot.

### `GET /api/admin/members/{id}/stats`
Member worked-hours stats for weekly/monthly/quarterly views.

Query params:
- `view`: `weekly|monthly|quarterly`
- `date`: `YYYY-MM-DD` (reference date)

Response:
- `{ success: true, view, date, startDate, endDate, points, summary }`
- `summary` includes `workedHours`, `expectedHours`, `progressPercent`, `weeklyWorkedHours`, `weeklyContractHours`, `weeklyProgressPercent`.

### `GET /api/admin/members/{id}/time-balance`
Quarter bag calculation on natural quarter boundaries.

Query params:
- `date`: `YYYY-MM-DD` (reference date)

Formula:
- `balanceHours = workedHours(quarterStart..cutoff) - expectedHoursUntilToday`
- `expectedHoursUntilToday = (weeklyContractHours / 7) * elapsedDaysInQuarter`

Response:
- `{ success: true, quarter, weeklyContractHours, workedHours, expectedHours, balanceHours }`

### `POST /api/admin/members/{id}/ensure-user`
Ensure an active member is linked to a backoffice user account (`bo_users`).

Behavior:
- Requires role importance `>= 90` (admin/root).
- If `restaurant_members.bo_user_id` already exists, reuses that user.
- If missing and member has email, resolves by email or creates a new `bo_users` record, then links `bo_user_id`.
- If member has no email, returns `{ success: false, message }`.

Response:
- `{ success: true, user: { id, email, name, created }, member: { id, boUserId } }`
- `{ success: false, message }`

### `GET /api/admin/roles`
Get role catalog + role permissions + current user assignments for active restaurant.

Response:
- `{ success: true, roles: RoleCatalogItem[], users: RoleUserItem[], currentUser }`
- `roles[]`: `{ slug, label, sortOrder, importance, iconKey, isSystem, permissions[] }`
- `users[]`: `{ id, email, name, role, roleImportance }`
- `currentUser`: `{ id, role, roleImportance }`

### `POST /api/admin/roles`
Create a custom role.

Body (JSON):
- `label` (string, required)
- `slug` (string, optional)
- `importance` (number `0..100`, required by UI)
- `iconKey` (string, required by UI)
- `permissions` (string[], required; at least one section)

Rules:
- Caller must have role importance `>= 90`.
- New role importance must be strictly lower than caller importance.
- System role slugs are reserved.

Response:
- `{ success: true, role: RoleCatalogItem }`
- `{ success: false, message }`

### `PATCH /api/admin/users/{id}/role`
Update user role for active restaurant.

Body (JSON):
- `role` (string)

Rules:
- Caller must have role importance `>= 90`.
- Caller importance must be strictly greater than:
  - current role importance of target user, and
  - new role importance being assigned.
- Caller cannot change own role.

Response:
- `{ success: true, user: { id, role, roleImportance } }`

### `POST /api/admin/invitations/validate`
Public endpoint (sin sesión) para validar token de invitación.

Body (JSON):
- `token` (string)

Response:
- `{ success: true, invitation: { memberId, firstName, lastName, email?, dni?, phone?, photoUrl?, roleSlug, roleLabel, expiresAt } }`
- `{ success: false, message }`

### `POST /api/admin/invitations/onboarding/start`
Public endpoint (sin sesión) para iniciar onboarding desde token.

Body (JSON):
- `token` (string)

Response:
- `{ success: true, onboardingGuid, member }`
- `{ success: false, message }`

### `GET /api/admin/invitations/onboarding/{guid}`
Public endpoint (sin sesión) para recuperar estado/datos de onboarding.

Response:
- `{ success: true, member, expiresAt }`
- `{ success: false, message }`

### `POST /api/admin/invitations/onboarding/{guid}/profile`
Public endpoint (sin sesión) para actualizar perfil en onboarding.

Body (JSON):
- `firstName` (string)
- `lastName` (string)
- Optional: `photoUrl` (string)

Response:
- `{ success: true, member }`
- `{ success: false, message }`

### `POST /api/admin/invitations/onboarding/{guid}/avatar`
Public endpoint (sin sesión) para subir avatar (multipart `avatar`) en onboarding.

Response:
- `{ success: true, avatarUrl, member }`
- `{ success: false, message }`

### `POST /api/admin/invitations/onboarding/{guid}/password`
Public endpoint (sin sesión) para establecer password final y consumir invitación.

Body (JSON):
- `password` (string)
- `confirmPassword` (string) (alias legacy: `passwordRepeat`)

Response:
- `{ success: true, next: "/login" }`
- `{ success: false, message }`

### `POST /api/admin/password-resets/validate`
Public endpoint (sin sesión) para validar token de reset.

Body (JSON):
- `token` (string)

Response:
- `{ success: true, reset: { memberId, firstName, lastName, email?, username?, expiresAt } }`
- `{ success: false, message }`

### `POST /api/admin/password-resets/confirm`
Public endpoint (sin sesión) para confirmar nueva password usando token de reset.

Body (JSON):
- `token` (string)
- `password` (string)
- `confirmPassword` (string) (alias legacy: `passwordRepeat`)

Response:
- `{ success: true, next: "/login" }`
- `{ success: false, message }`

### `GET /api/admin/fichaje/ping`
Lightweight endpoint for clients with access to the `fichaje` section.

Response:
- `{ success: true, message: "fichaje_ready" }`

### `GET /api/admin/fichaje/state`
Returns current fichaje state for the logged user in the active restaurant.

Response:
- `{ success: true, state }`
- `state.now`: server timestamp (RFC3339)
- `state.member`: `{ id, fullName, dni } | null`
- `state.activeEntry`: `{ id, memberId, memberName, workDate, startTime, startAtIso } | null`
- `state.scheduleToday`: `{ id, memberId, memberName, date, startTime, endTime, updatedAt } | null`

### `POST /api/admin/fichaje/start`
Starts a fichaje entry for the logged user/member.

Body (JSON):
- `dni` (string)
- `password` (string)

Response:
- `{ success: true, state }`
- `{ success: false, message }` when validation fails

### `POST /api/admin/fichaje/stop`
Stops the currently active fichaje entry for the logged user/member.

Response:
- `{ success: true, state }`
- `{ success: false, message }` when there is no active entry

### `POST /api/admin/fichaje/admin/start`
Admin-only start of fichaje for another member.

Body (JSON):
- `memberId` (number)

Response:
- `{ success: true, activeEntry }`
- `{ success: false, message }` on validation errors

### `POST /api/admin/fichaje/admin/stop`
Admin-only stop of fichaje for another member.

Body (JSON):
- `memberId` (number)

Response:
- `{ success: true, activeEntry }`
- `{ success: false, message }` if the member has no active entry

### `GET /api/admin/fichaje/entries`
Admin-only list of `member_time_entries` for one member and one date.

Query params:
- `date` (`YYYY-MM-DD`, optional; default today)
- `memberId` (number, required)

Response:
- `{ success: true, date, memberId, entries }`
- `entries[]`: `{ id, memberId, memberName, workDate, startTime, endTime|null, minutesWorked, source }`

### `PATCH /api/admin/fichaje/entries/{id}`
Admin-only patch of a specific `member_time_entries` record.

Body (JSON):
- `startTime` (`HH:MM`, optional)
- `endTime` (`HH:MM`, optional)

Rules:
- At least one field is required.
- For active entries (`end_time IS NULL`), only `endTime` can be patched.
- When both times are present, `endTime` must be strictly greater than `startTime`.

Response:
- `{ success: true, entry }`
- `{ success: false, message }` on validation errors

### `GET /api/admin/fichaje/labour-cost`
Admin-only actual labour cost report. Query `from=YYYY-MM-DD&to=YYYY-MM-DD`, max 366 days.
Uses each time-entry date against effective compensation history. Returns totals, per-member minutes/cost and members missing compensation.

### `GET /api/admin/fichaje/ws`
WebSocket endpoint for realtime fichaje events scoped by active restaurant.

Behavior:
- Server auto-subscribes the socket to the active restaurant room.
- Client can send `{ \"type\": \"join_restaurant\", \"restaurantId\": <id> }` to request a fresh joined payload.
- Broadcast event types: `clock_started`, `clock_stopped`, `schedule_updated`.
- A background auto-cut loop closes stale active fichajes and emits `clock_stopped`.

Auto-cut rules:
- If member has schedule for that `work_date`, open entry closes at schedule `end_time`.
- If no schedule exists for that date, open entry closes at `23:59` (Europe/Madrid).

### `GET /api/admin/horarios`
Admin-only list of assigned schedules for one day.

Query params:
- `date` (`YYYY-MM-DD`, optional; default today in Europe/Madrid timezone)

Response:
- `{ success: true, date, schedules }`
- `schedules[]`: `{ id, memberId, memberName, date, startTime, endTime, updatedAt }`

### `POST /api/admin/horarios`
Admin-only upsert for one member schedule in one day.

Body (JSON):
- `date` (`YYYY-MM-DD`)
- `memberId` (number)
- `startTime` (`HH:MM`)
- `endTime` (`HH:MM`)

Rules:
- `endTime` must be strictly greater than `startTime`.
- Upsert key: `(restaurant_id, restaurant_member_id, work_date)`.

Response:
- `{ success: true, schedule }`
- `{ success: false, message }` for validation errors

### `GET /api/admin/horarios/month`
Admin-only monthly summary used by the horarios calendar.

Query params:
- `year` (int, optional; default current year)
- `month` (int `1-12`, optional; default current month)

Response:
- `{ success: true, year, month, days }`
- `days[]`: `{ date: \"YYYY-MM-DD\", assignedCount: number }`

### `GET /api/admin/calendar`
Monthly calendar data (mirrors legacy `/api/get_calendar_data.php` but scoped to the active backoffice restaurant). Sets `ETag`.

Query params:
- `year` (int, optional; defaults to current year)
- `month` (int `1-12`, optional; defaults to current month)

Response:
- `{ success: true, data: CalendarDay[] }`

`CalendarDay`:
- `date` (`YYYY-MM-DD`)
- `booking_count` (number)
- `total_people` (number) (sum of `party_size`)
- `limit` (number) (daily limit)
- `is_open` (boolean)

### `GET /api/admin/bookings`
List bookings for a date (paginated).

Query params:
- `date` (required `YYYY-MM-DD`)
- `status` (optional): `pending|confirmed`
- `q` (optional): optimized search over `customer_name`, `contact_email` y `contact_phone`.
  - Uses prefix matching (`q%`) for index-friendly filtering.
  - When query terms are long enough (`>= 3` chars), also attempts MySQL FULLTEXT on `customer_name/contact_email/commentary` (with automatic prefix fallback).
- `sort` (optional, default `reservation_time`): `reservation_time|added_date`
- `dir` (optional, default `asc`): `asc|desc`
- `page` (optional, default `1`) (1-based)
- `count` (optional, default `15`) (max `25`)

Legacy compatibility:
- If `page`/`count` are absent, the endpoint also accepts `limit`/`offset`.

Response:
- `{ success: true, bookings: Booking[], floors: Floor[], total_count: number, total: number, page: number, count: number }`
- `floors` usa la misma estructura `Floor` de config y representa el estado activo del dia consultado.
- `Booking` incluye `children` (`number`) y `preferred_floor_number` (`number|null`) para la preferencia de salón/planta.

### `GET /api/admin/bookings/export`
Exports **all** bookings for a date (no filters; used for PDF export).

Query params:
- `date` (required `YYYY-MM-DD`)

Response:
- `{ success: true, bookings: Booking[] }`

### `GET /api/admin/bookings/search`
Cross-date general booking search by name/email and phone.

Auth: `bo_session` cookie + `reservas` section.

Query params:
- `name` (optional string) — partial match on `customer_name` and `contact_email` (`LIKE '%name%'`)
- `phone` (optional string) — prefix match on `contact_phone` (digits only, `LIKE 'digits%'`)
- `page` (optional int, default 1)
- `count` (optional int, default 15, max 100)

At least one of `name` or `phone` must be non-empty; otherwise returns empty results.

Response shape is identical to `GET /api/admin/bookings`:
- `{ success: true, bookings: Booking[], floors: [], total_count: number, total: number, page: number, count: number }`
- `floors` is always an empty array (cross-date search has no single-day floor context).
- Results ordered by `reservation_date DESC, reservation_time DESC, id DESC`.

### `GET /api/admin/bookings/{id}`
Response:
- `{ success: true, booking: Booking }`

### `POST /api/admin/bookings`
Create booking (admin; allows overbooking).

Body (JSON):
- `reservation_date` (`YYYY-MM-DD`)
- `reservation_time` (`HH:MM` or `HH:MM:SS`)
- `party_size` (number)
- `customer_name` (string)
- `contact_phone` (string; digits-only validated)
- Optional: `contact_email` (string), `table_number` (string), `commentary` (string), `babyStrollers` (number), `highChairs` (number)
- Optional: `preferred_floor_number` (number)
- `special_menu` (boolean)
- If `special_menu=true`:
  - `menu_de_grupo_id` (number, required)
  - `principales_json` (array, optional; rows `{ name, servings }`)
- If `special_menu=false`:
  - `arroz_types` (string[])
  - `arroz_servings` (number[])

Response:
- `{ success: true, booking: Booking, notifications_sent: boolean, whatsapp_sent: boolean, email_sent: boolean }`
- If WhatsApp/Email fail: includes `notification_warning` field with error details.
- `{ success: false, message: string }`

### `PATCH /api/admin/bookings/{id}`
Partial update.

Body (JSON): any subset of the `POST /api/admin/bookings` fields.

Response:
- `{ success: true, booking: Booking }`
- `{ success: false, message: string }`

### `POST /api/admin/bookings/{id}/cancel`
Cancels a booking (moves row to `cancelled_bookings` and deletes from `bookings`).

Response:
- `{ success: true }`

### `GET /api/admin/arroz-types`
Returns available rice types from `FINDE` (active `TIPO='ARROZ'`), as a bare JSON array.

Response:
- `string[]`

### Comida Module (`/api/admin/comida/*` and `/api/comida/*`)

Nuevo contrato unificado para carta/comida por tipo:
- `platos`, `postres`, `vinos`, `bebidas`, `cafes`.
- Auth:
  - Backoffice: cookie `bo_session` en `/api/admin/comida/*`.
  - Público multi-tenant: `/api/comida/*`.
  - Escritura en `/api/comida/*` requiere `ADMIN_TOKEN` (`X-Admin-Token` o `Authorization: Bearer`).

#### `GET /api/admin/comida/counts`
Requires backoffice session and `menus` access.

Response:
```json
{
  "success": true,
  "countsByType": {
    "vinos": 0,
    "cafes": 0,
    "postres": 0,
    "platos": 0,
    "bebidas": 0
  }
}
```

Returns all five card counters in one DB round-trip and replaces five list requests in backoffice SSR.

#### `GET /api/admin/comida/{tipo}` (alias público: `GET /api/comida/{tipo}`)
Listado paginado + filtros.

Query params:
- `page` (default `1`)
- `pageSize` (default `24`, max `100`) (aliases: `limit`, `count`)
- `q` (búsqueda por texto)
- `active` (`0|1`)
- `tipo` (subtipo, por ejemplo vino tinto o tipo de plato)
- `categoria|category` (especialmente para `platos`)
- `alergeno|allergen` (especialmente para `platos`/`postres`)
- `suplemento` (`0|1`, especialmente `platos`)

Response base:
- `{ success: true, items: Item[], total: number, page: number, limit: number, pageSize: number }`

Aliases por tipo:
- `vinos`: además incluye `vinos: Vino[]`
- `postres`: además incluye `postres: Postre[]`

#### `GET /api/admin/comida/{tipo}/{id}` (alias público: `GET /api/comida/{tipo}/{id}`)
Detalle por item.

Response:
- `{ success: true, item: Item }`
- `vinos`: también `vino`
- `postres`: también `postre`

#### `POST /api/admin/comida/{tipo}` (alias admin público: `POST /api/comida/{tipo}`)
Crear item.

Body (JSON):
- Campos comunes según tipo: `nombre`, `descripcion`, `tipo`, `precio`, `active`, `alergenos[]`, `imageBase64`.
- `platos`: soporta `categoria|category|category_id`, `titulo`, `suplemento`.
- `vinos`: `bodega`, `denominacion_origen`, `graduacion`, `anyo`.
- `postres`: usa `descripcion` (o fallback `nombre`) + `alergenos`.

Response:
- `{ success: true, num: number, item: Item }`
- `vinos`: también `vino`
- `postres`: también `postre`

#### `PATCH /api/admin/comida/{tipo}/{id}` (alias admin público: `PATCH /api/comida/{tipo}/{id}`)
Actualización parcial.

Response:
- `{ success: true, item: Item }` (según tipo incluye alias `vino`/`postre` cuando aplica)
- Error de validación: `{ success: false, message: string }`

#### `DELETE /api/admin/comida/{tipo}/{id}` (alias admin público: `DELETE /api/comida/{tipo}/{id}`)
Eliminación.

Response:
- `{ success: true }`

#### Categorías de platos
- `GET /api/admin/comida/platos/categorias`
- `POST /api/admin/comida/platos/categorias`
- Alias público:
  - `GET /api/comida/platos/categorias`
  - `POST /api/comida/platos/categorias` (requiere `ADMIN_TOKEN`)

Modelo:
- Base + custom por restaurante en `comida_plato_categories`.
- Seeds base automáticos: `Entrantes`, `Principal`, `Arroz`, `Postre`.

Response list:
- `{ success: true, categories: Category[] }`
- aliases legacy: `categorias`, `tipos`.

Response create:
- `{ success: true, category: Category }`
- alias legacy: `categoria`.

#### Catálogo unificado de categorías

Auth: sesión backoffice + sección `menus`. Siempre acotado a `ActiveRestaurantID`.

Cubre los tipos que nunca tuvieron catálogo (`vinos`, `cafes`, `postres`) y añade
categorías **globales**, compartidas por todos los tipos. Las tablas legacy
(`comida_plato_categories`, `comida_bebida_categories`) y la FK
`comida_items.category_id` no se tocan.

Modelo: `comida_categories(restaurant_id, food_type, name, slug, active)` con
`UNIQUE (restaurant_id, food_type, slug)`. `food_type = ''` es el centinela de
"global"; no se usa `NULL` porque en MySQL los `NULL` no colisionan en un índice
único y permitirían globales duplicadas.

`Category`:
```
{ id, key, name, slug, foodType, scope, isGlobal, origin, editable, active }
```
- `key`: identificador estable **para el cliente**, con formato `{origin}:{id}`.
  Los `id` legacy y los nuevos son secuencias `AUTO_INCREMENT` independientes, así
  que el `id` **por sí solo no identifica una categoría**: usa siempre `key` para
  claves de React, selección y comparación.
- `id`: PK en `comida_categories`. Vale `0` cuando `origin` es `legacy`, porque el
  `id` de la tabla legacy no es direccionable por estos endpoints.
- `origin`: `"unified"` (fila en `comida_categories`) o `"legacy"` (fila en
  `comida_plato_categories` / `comida_bebida_categories`).
- `editable`: `true` solo si `origin == "unified"`. Las legacy se listan pero no
  se pueden editar ni borrar aquí; se siguen gestionando desde la carta antigua.
- `scope`: `"global"` o el tipo (`platos|bebidas|vinos|cafes|postres`).

**Unicidad de nombre entre scopes solapados.** El índice único solo cubre
`(restaurant_id, food_type, slug)`, así que por sí solo aceptaría una global
"Tapas" junto a una de `platos` "Tapas". Las dos se muestran juntas en el mismo
selector y los productos referencian la categoría **por nombre**, así que después
no habría forma de saber de cuál de las dos vino un producto: renombrar una
reescribiría los productos de la otra. Por eso los endpoints de escritura rechazan
el solape: una global choca con cualquier tipo, y un tipo choca con las globales y
con su propia tabla legacy.

##### `GET /api/admin/comida/categorias?foodType={tipo}`
Con `foodType`: devuelve las del tipo + las globales + (solo para `platos` y
`bebidas`) las legacy de su tabla. Solo las activas: es la vista de selector.
Sin `foodType`: el catálogo completo, **inactivas incluidas**, para la pantalla de
config. Incluye también las legacy de `platos` y `bebidas`, para que quien gestiona
vea exactamente lo mismo que ofrecen los selectores.

`foodType` acepta el mismo vocabulario que el resto de `/comida` (singulares y
acentos: `plato`, `vino`, `café`). `""` y `global` limitan a las globales; `all`
**no** es un valor válido.

Las categorías con el mismo nombre se colapsan en una sola entrada. Gana la
`editable`, y entre dos editables gana la de scope específico sobre la global, para
que el cliente reciba la que puede gestionar y no una copia inerte.

- `200` → `{ success: true, categories: Category[] }` (ordenadas por nombre)
- `400` → `foodType` inválido

##### `POST /api/admin/comida/categorias`
Body: `{ name: string, foodType?: string|null, global?: boolean }`.
`global: true` tiene prioridad sobre `foodType`. `global: false` **exige** un
`foodType` real: sin él la petición se rechaza en vez de crear una global, que es
justo lo contrario de lo pedido. Los campos desconocidos se rechazan, para que un
`"globl": true` mal escrito no se ignore en silencio y acabe dando el scope opuesto.

- `200` → `{ success: true, category: Category }`
- `400` → nombre vacío, nombre de más de 120 caracteres, scope contradictorio o
  campo desconocido
- `409` → el nombre ya existe en alguno de los scopes solapados o en la tabla legacy,
  o choca con un nombre base reservado

Los nombres base de platos (entrantes, principal, arroz, postre) y de bebidas
(refrescos, aguas, zumos, cervezas, copas, licores, cocktails) están **reservados**
en su tipo y en `global`, aunque la tabla legacy aún no los tenga: se siembran de
forma diferida, al abrir el listado por primera vez, así que en un restaurante nuevo
parecen libres. Tomar uno daría una categoría unified a la que el siguiente listado
le sembraría una gemela legacy al lado.

La reserva es **uniforme**: cubre también a una categoría que ya lleva ese nombre, que
es como quedaron las creadas antes de que existiera. Esa fila no puede reactivarse ni
moverse de scope mientras conserve el nombre, porque en cuanto el listado siembra la
fila base el duplicado es real y visible. La salida es **renombrarla**, que nunca está
bloqueado: el `PATCH` valida contra el nombre nuevo, no contra el actual.

##### `PATCH /api/admin/comida/categorias/{id}`
Body: `{ name?, foodType?, global?, active? }`. Permite renombrar, mover de scope
y activar/desactivar. Solo acepta `id` de categorías con `origin: "unified"`.

Renombrar propaga el nuevo nombre en la **misma transacción** a todo lo que
referencia la categoría por nombre:
- `comida_items.categoria`, restringido a los `source_type` que el scope alcanza.
- `VINOS.tipo`, si el scope alcanza a vinos.
- La fila gemela en la tabla legacy, si existe. Guardar un plato resuelve su
  categoría contra `comida_plato_categories` y crea la fila si falta, así que una
  categoría creada aquí genera una gemela legacy en cuanto un producto la usa, y
  `comida_items.category_id` apunta a esa gemela. Sin renombrarla, volvería a
  aparecer como una entrada legacy con el nombre viejo.

Se considera gemela la fila legacy que coincide en **slug y nombre**, está activa y
no es `source: 'base'`. El slug por sí solo no basta: las tablas legacy son
anteriores a este catálogo y la carta antigua sigue escribiendo en ellas, así que
una fila ajena puede compartirlo. Lo mismo aplica al `DELETE`, que borra la gemela
para que no reaparezca como entrada legacy irreactivable.

Como contrapartida, `POST /api/admin/comida/platos/categorias` acepta un `slug` del
cliente desacoplado del `name`, así que la carta antigua puede renombrar la gemela y
romper el enlace de forma permanente: a partir de ahí el renombrado unificado ya no
cascadea y la fila legacy vuelve a aparecer en el listado con el nombre viejo. Es el
precio de no tocar filas que no son nuestras.

La fila se bloquea con `SELECT ... FOR UPDATE` antes de escribir, para que dos
renombrados simultáneos no dejen productos repartidos entre el nombre viejo y el nuevo.

**Mover de scope y desactivar se rechazan mientras la categoría esté en uso.** El
scope nuevo no arrastra a los productos, que se emparejan por nombre dentro del
scope viejo; y desactivar la esconde de todos los selectores mientras los productos
siguen llevando su nombre. Renombrar sí está permitido, porque cascadea.

- `200` → `{ success: true, category: Category }`
- `400` → nombre inválido, scope contradictorio o campo desconocido
- `404` → no encontrada, de otro restaurante, o `legacy` (las legacy se serializan
  con `id: 0`, que no es direccionable aquí)
- `409` → el nombre ya existe en un scope solapado, choca con un nombre base
  reservado, o se intenta mover/desactivar una categoría en uso

Reactivar revalida el nombre: mientras la categoría estaba apagada, la carta antigua
pudo escribir ese nombre en la tabla legacy, y volver a encenderla dejaría las dos
entradas en el mismo selector.

La reserva de nombres base se comprueba contra el scope de **destino**, así que cubre
por igual el renombrado, la reactivación y el cambio de scope: una categoría creada en
un tipo que la reserva no alcanza no puede colarse moviéndola a uno que sí.

##### `DELETE /api/admin/comida/categorias/{id}`
Solo categorías `unified`.

- `200` → `{ success: true }`
- `404` → no encontrada, de otro restaurante, o `legacy`
- `409` → `La categoria esta en uso`

Borrar arrastra, en la misma transacción, el `stock_margin_scopes` con
`scope_kind: 'COMIDA_CATEGORY'` y `scope_key: '{tipo}:{id de la gemela}'`, y sus bandas
por cascada. Ese `scope_key` es texto plano sin FK, así que nada en la base de datos lo
limpiaría, y como los ids se reutilizan la siguiente categoría en caer en ese id
heredaría en silencio un objetivo de margen ajeno.

El uso se comprueba **solo en los scopes que la categoría alcanza**: una categoría
de `cafes` no se considera en uso porque un plato distinto comparta su nombre.
`comida_items.categoria` es un `VARCHAR` compartido por platos/bebidas/cafés, los
vinos se categorizan por `VINOS.tipo`, y `POSTRES` no tiene columna de categoría
(una categoría de `postres` nunca está en uso).

#### `PATCH /api/admin/comida/items/{id}/production-type`

Auth: sesión backoffice + permiso de stock. Marca un producto como `RAW` (se compra
y se vende tal cual) o `MANUFACTURED` (se produce desde una ficha técnica).

Body: `{ productionType: "RAW"|"MANUFACTURED", stockRecipeId?: number|null, source?: "comida"|"vinos"|"postres" }`

`source` selecciona la tabla del catálogo, porque los tres catálogos tienen claves
primarias independientes: `comida_items.id`, `VINOS.num`, `POSTRES.NUM`. Omitirlo
equivale a `"comida"`, así que un producto de `postres` **debe** enviarlo o el
`UPDATE` buscará su `NUM` dentro de `comida_items.id`.

- `200` → `{ success: true }`
- `400` → tipo de producción o ficha técnica inválidos
- `404` → el producto no existe en el catálogo indicado

Nota: el `404` se decide comprobando la existencia de la fila, no por
`RowsAffected() == 0`. El DSN no activa `clientFoundRows`, así que MySQL informa de
filas *modificadas* y no *coincidentes*; reenviar los mismos valores es un update
sin cambios y devolvía un `404` espurio sobre la fila correcta.

#### Aliases legacy backoffice (`/api/admin/*`)
Para compatibilidad con pantalla anterior de carta:
- `GET/POST/PATCH/DELETE /api/admin/platos` (+ `POST /api/admin/platos/{id}/toggle`)
- `GET/POST/PATCH/DELETE /api/admin/bebidas` (+ `POST /api/admin/bebidas/{id}/toggle`)
- `GET/POST/PATCH/DELETE /api/admin/cafes` (+ `POST /api/admin/cafes/{id}/toggle`)

Estos aliases delegan internamente al módulo `/api/admin/comida/*`.

### Group Menus V2 (`/api/admin/group-menus-v2*`)
Backoffice wizard API for the new menu editor. Uses cookie session auth.

### `GET /api/admin/group-menus-v2?includeDrafts=0|1`
Returns menus list used by `/app/menus`.

Response:
- `{ success: true, count, menus: [{ id, menu_title, price, active, is_draft, menu_type, created_at, modified_at }] }`

### `POST /api/admin/group-menus-v2/drafts`
Creates a new draft menu and default sections (`Entrantes`, `Principales`, `Postres`).

Body (JSON):
- `menu_type` (optional string; default `closed_conventional`)

Response:
- `{ success: true, menu_id }`

### `GET /api/admin/group-menus-v2/{id}`
Returns full editor payload:
- basics (`menu_title`, `price`, `active`, `is_draft`, `menu_type`, `menu_subtitle`)
- preview toggles/media:
  - `show_dish_images`
  - `show_menu_preview_image`
  - `menu_preview_image_url`
  - `menu_preview_ai_requested`
  - `menu_preview_ai_generating`
  - `ai_requested_img` (alias for preview state)
  - `ai_generating_img` (alias for preview state)
  - `ai_generated_img` (alias URL/null for preview state)
  - `menu_preview` object with the same fields above (for WS/fetch parity)
- settings (`included_coffee`, `beverage`, `comments`, `min_party_size`, `main_dishes_limit`, `main_dishes_limit_number`)
- `sections[]` and nested `dishes[]` (`sections[].annotations` is `string[]`)
- `ai_images` tracker for dish image generation (`total_requested`, `total_generating`, `items[]`)

Response:
- `{ success: true, menu: { ... } }`

### `PATCH /api/admin/group-menus-v2/{id}/basics`
Upserts menu metadata and settings (patch semantics).

Body (JSON, any subset):
- `menu_title`, `price`, `active`, `is_draft`, `menu_type`
- `menu_subtitle` (`string[]`)
- `show_dish_images` (boolean)
- `show_menu_preview_image` (boolean)
- `beverage` (object)
- `comments` (`string[]`)
- `min_party_size`, `main_dishes_limit`, `main_dishes_limit_number`, `included_coffee`

Response:
- `{ success: true }`

### `PATCH /api/admin/group-menus-v2/{id}/menu-type`
Changes only the menu type from list/editor quick action.

Body (JSON):
- `menu_type` (required string)

Response:
- `{ success: true, menu_id, menu_type }`

### `PUT /api/admin/group-menus-v2/{id}/sections`
Replaces the ordered sections list for a menu.

Body (JSON):
- `sections`: array of `{ id?, title, kind, annotations? }`

Rules:
- At least 1 section is required.
- Removed section IDs are deleted.

Response:
- `{ success: true, sections }`

### `PATCH /api/admin/group-menus-v2/{id}/sections/{sectionId}/annotations`
Updates only section annotations (ordered list).

Body (JSON):
- `annotations`: `string[]`

Response:
- `{ success: true, section_id, annotations, menu_id }`

### `GET /api/admin/group-menus-v2/{id}/sections/{sectionId}/dishes`
Lazy-loads dishes for a single section. Enables accordion-style UI where sections load on-demand.

Auth:
- Backoffice session cookie (`bo_session`)

Response:
- `{ success: true, dishes: [{ id, section_id, catalog_dish_id, title, description, allergens, supplement_enabled, supplement_price, price, active, position, foto_url?, image_url?, ai_requested_img, ai_generating_img, ai_generated_img? }] }`

### `PUT /api/admin/group-menus-v2/{id}/sections/{sectionId}/dishes`
Replaces dishes for one section (ordered).

Body (JSON):
- `dishes`: array of `{ id?, catalog_dish_id?, title, description, allergens, supplement_enabled, supplement_price, active? }`

Response:
- `{ success: true, dishes: [{ ..., foto_url?, image_url? }] }`
- Dish fields include AI image state:
  - `ai_requested_img` (boolean)
  - `ai_generating_img` (boolean)
  - `ai_generated_img` (string|null)

### `PATCH /api/admin/group-menus-v2/{id}/sections/{sectionId}/dishes/{dishId}`
Updates a single dish in-place (without replacing the whole section list).

Body (JSON):
- Any subset of: `{ catalog_dish_id, title, description, allergens, supplement_enabled, supplement_price, price, active }`

Response:
- `{ success: true, dish }`
- `dish` may include `foto_url` and `image_url` when the dish has an uploaded image.
- `dish` includes `ai_requested_img`, `ai_generating_img`, `ai_generated_img`.

### `POST /api/admin/group-menus-v2/{id}/sections/{sectionId}/dishes/{dishId}/image`
Uploads/replaces an image for one V2 dish.

Auth:
- Backoffice session cookie (`bo_session`)

Body (`multipart/form-data`):
- `image`: image file (`jpeg`, `png`, `webp`, `gif`; max 8MB)

Storage path:
- `{restaurantId}/pictures/{menuId}/dish-{dishId}-{timestamp}.{ext}`

Response:
- `{ success: true, dish }`
- `dish` includes `foto_url` + `image_url` aliases to the Bunny pull URL.

### `POST /api/admin/group-menus-v2/{id}/sections/{sectionId}/dishes/{dishId}/image/ai`
Starts asynchronous AI enhancement for one V2 dish image.

Auth:
- Backoffice session cookie (`bo_session`)

Body (`multipart/form-data`):
- `image`: source image (`jpeg`, `png`, `webp`; max bytes controlled by backend env)

Behavior:
- Validates menu/section/dish ownership.
- Sets dish flags immediately: `ai_requested_img=1`, `ai_generating_img=1`.
- Emits websocket event `ai_image_started`.
- Runs background worker (bounded concurrency) to call OpenAI images edit API.
- Uploads generated result to Bunny path:
  - `{restaurant_id}/pictures/{menu_id}/ai-generated/{dish_id}.webp`
- Persists:
  - `ai_generated_img` (full Bunny pull URL),
  - `ai_generating_img=0`,
  - `foto_path` (object path).
- Emits `ai_image_completed` (or `ai_image_failed` on errors).

Response:
- `{ success: true, message: \"AI image generation started\", dish_id }`
- `{ success: false, message }`

### `POST /api/admin/group-menus-v2/{id}/preview-image`
Uploads/replaces one menu-level preview image.

Auth:
- Backoffice session cookie (`bo_session`)

Body (`multipart/form-data`):
- `image`: image file (`jpeg`, `png`, `webp`, `gif`; max 8MB)

Behavior:
- Validates menu ownership.
- Converts input to `image/webp` and enforces output max size `150KB`.
- Uploads to Bunny Storage path:
  - `{restaurant_id}/pictures/menupreviewpictures/{menu_id}/{image_id}.webp`
- Persists:
  - `menu_preview_image_path` (object path),
  - `show_menu_preview_image=1`,
  - `menu_preview_ai_requested=0`,
  - `menu_preview_ai_generating=0`.

Response:
- `{ success: true, imageUrl }`
- `{ success: false, message }`

### `POST /api/admin/group-menus-v2/{id}/preview-image/ai`
Starts asynchronous AI enhancement for one menu-level preview image.

Auth:
- Backoffice session cookie (`bo_session`)

Body (`multipart/form-data`):
- `image`: source image (`jpeg`, `png`, `webp`; max bytes controlled by backend env)

Behavior:
- Validates menu ownership.
- Sets menu flags immediately:
  - `menu_preview_ai_requested=1`
  - `menu_preview_ai_generating=1`
- Emits websocket event `preview_image_started`.
- Runs background worker (bounded concurrency) using the same AI provider path as dish images.
- Normalizes generated output to `image/webp` with max size `150KB`.
- Uploads to Bunny Storage path:
  - `{restaurant_id}/pictures/menupreviewpictures/{menu_id}/{image_id}.webp`
- Persists:
  - `menu_preview_image_path` (object path),
  - `show_menu_preview_image=1`,
  - `menu_preview_ai_generating=0`.
- Emits `preview_image_completed` (or `preview_image_failed` on errors).

Response:
- `{ success: true, message, menu_id }`
- `{ success: false, message }`

### `GET /api/admin/group-menus-v2/{id}/slider`
Get menu-level slider state and images.

Auth:
- Backoffice session cookie (`bo_session`)

Response:
- `{ success: true, slider: { show_slider: bool, images: [{ id, image_url, position, created_at }] } }`
- `{ success: false, message }`

### `PATCH /api/admin/group-menus-v2/{id}/slider`
Toggle menu slider visibility.

Auth:
- Backoffice session cookie (`bo_session`)

Body (`application/json`):
- `show_slider`: boolean

Response:
- `{ success: true, show_slider: bool }`
- `{ success: false, message }`

### `POST /api/admin/group-menus-v2/{id}/slider/images`
Upload a new slider image for a menu. Supports 16:9 crop via ImageMagick normalization.

Auth:
- Backoffice session cookie (`bo_session`)

Body (`multipart/form-data`):
- `image`: source image (`jpeg`, `png`, `webp`, `gif`; max 8MB)

Behavior:
- Validates menu ownership.
- Normalizes to WebP via `specialmenuimage.NormalizeToWebP` (respects 16:9 output).
- Uploads to Bunny Storage path: `{restaurant_id}/pictures/menusliderpictures/{menu_id}/{image_id}.webp`
- Inserts row into `menu_slider_images` table with auto-incremented position.
- Sets `show_menu_slider=1` on the menu.

Response:
- `{ success: true, image: { id, image_url, position, created_at } }`
- `{ success: false, message }`

### `DELETE /api/admin/group-menus-v2/{id}/slider/images/{imageId}`
Delete a slider image.

Auth:
- Backoffice session cookie (`bo_session`)

Response:
- `{ success: true }`
- `{ success: false, message }`

### `PUT /api/admin/group-menus-v2/{id}/slider/images`
Reorder slider images by passing new position order.

Auth:
- Backoffice session cookie (`bo_session`)

Body (`application/json`):
- `image_ids`: array of image IDs in desired order

Response:
- `{ success: true }`
- `{ success: false, message }`

### `POST /api/admin/group-menus-v2/{id}/slider/images/ai`
Starts asynchronous AI enhancement for a slider image. Uses adjusted prompt for 16:9 ambiance framing (less dish focus).

Auth:
- Backoffice session cookie (`bo_session`)
- Requires `OpenAIAPIKey` configured (WaveSpeed AI)

Body (`multipart/form-data`):
- `image`: source image (`jpeg`, `png`, `webp`; max bytes controlled by backend env)

Behavior:
- Validates menu ownership.
- Uses custom AI prompt: `boGroupMenuV2SliderAIPrompt` - emphasizes wide-angle restaurant ambiance/table setting, dish as secondary subject, 16:9 output.
- Output size: `1792x1024` (16:9).
- Uploads to Bunny Storage path: `{restaurant_id}/pictures/menusliderpictures/{menu_id}/{image_id}.webp`
- Inserts row into `menu_slider_images` table.
- Sets `show_menu_slider=1` on the menu.
- Emits `slider_image_completed` websocket event.

Response:
- `{ success: true, message, menu_id }`
- `{ success: false, message }`

### `GET /api/admin/group-menus-v2/ws?menuId={id}`
WebSocket endpoint for realtime V2 dish AI image updates (scoped by active restaurant + menu id).

Auth:
- Backoffice session cookie (`bo_session`)

Behavior:
- Requires query `menuId` (positive integer and owned by active restaurant).
- Server sends `hello` with current `tracker` snapshot.
- `hello/snapshot` may include `menu_preview`:
  - `{ show_menu_preview_image, menu_preview_image_url, menu_preview_ai_requested, menu_preview_ai_generating, ai_requested_img, ai_generating_img, ai_generated_img }`
- Client can send `sync`, `refresh`, `join`, `join_menu`, or `join_group_menu` messages to request fresh snapshot.
- Broadcast event types:
  - `ai_image_started`
  - `ai_image_completed`
  - `ai_image_failed`
  - `preview_image_started`
  - `preview_image_completed`
  - `preview_image_failed`

Tracker payload shape:
- `{ total_requested, total_generating, items: [{ dish_id, ai_requested, ai_generating, ai_generated_img }] }`

### `POST /api/admin/group-menus-v2/{id}/special-image`
Uploads/replaces the image for one special menu.

Auth:
- Backoffice session cookie (`bo_session`)

Body (`multipart/form-data`):
- `image`: source file
  - Supported: `jpeg`, `png`, `webp`, `gif`, `pdf`, `doc`, `docx`, `txt`
  - Max input size: `10MB`

Behavior:
- Validates the menu belongs to the active restaurant and is `menu_type = special`.
- Converts input to `image/webp` server-side.
- Enforces output max size `150KB` (best-effort compression and resize).
- Uploads to Bunny Storage path:
  - `{restaurant_id}/pictures/menus_especiales/{menu_id}.webp`
- Persists `special_menu_image_url` in `menusDeGrupos`.

Response:
- `{ success: true, imageUrl, filename }`
- `{ success: false, message }`

### `POST /api/admin/group-menus-v2/{id}/publish`
Validates menu has at least one section and one active dish, marks `is_draft=0`, and syncs legacy snapshot fields.

Response:
- `{ success: true }` or `{ success: false, message }`

### `POST /api/admin/group-menus-v2/{id}/toggle-active`
Toggles `active` quickly from the list view.

Response:
- `{ success: true, active: boolean }`

### `DELETE /api/admin/group-menus-v2/{id}`
Deletes a menu and cascades V2 sections/dishes.

Response:
- `{ success: true }`

### Dishes Catalog (`/api/admin/dishes-catalog*`)

### `GET /api/admin/dishes-catalog/search?q=<text>&limit=<n>`
Searches reusable dishes by title for the active restaurant.

Response:
- `{ success: true, items: [{ id, title, description, allergens, default_supplement_enabled, default_supplement_price, updated_at }] }`

### `POST /api/admin/dishes-catalog/upsert`
Creates or updates a reusable dish entry.

Body (JSON):
- `id` (optional for update)
- `title` (required)
- `description`
- `allergens` (`string[]`)
- `default_supplement_enabled` (boolean)
- `default_supplement_price` (number|null)

Response:
- `{ success: true, dish: { ... } }`

### Reservation Config (`/api/admin/config/*`)

### `GET /api/admin/config/restaurant-info`
Returns contact and fiscal data for active restaurant.

Response:
- `{ success: true, restaurantInfo: { direccion, telefono, email, website, cif, direccionFacturacion, clasificacion } }`
- `website` is stored per `restaurant_info.restaurant_id`, normalized to `https://...`.

### `POST /api/admin/config/check-website`
Checks website URL without saving it. Backoffice calls this endpoint 500 ms after website input changes.

Body:
- `{ website: string }`

Response:
- `{ success: true, website: "https://..." }` only when final URL responds HTTP 200.
- `{ success: false, message }` otherwise.

### `POST /api/admin/config/restaurant-info`
Partial update. `website` accepts a domain or URL, is normalized to HTTPS, checked server-side, and only saved when final URL responds HTTP 200. Empty value clears it.

Body:
- `website` (`string`, optional; no credentials, query, or fragment)
- Other contact/fiscal fields are unchanged.

### `GET /api/admin/config/defaults`
Returns restaurant-level default config used as fallback in daily config.

Response:
- `{ success: true, openingMode, morningHours, nightHours, weekdayOpen, hours, dailyLimit, mesasDeDosLimit, mesasDeTresLimit, hourSplitEnabled, defaultHourPercentages, allowFloorReservation, allowSalonReservation }`
- `weekdayOpen`: objeto por dia con claves `monday..sunday` y valor booleano `open/closed`.
- `allowFloorReservation` / `allowSalonReservation`: toggles globales de reserva de planta/salón (por defecto `false`).

### `POST /api/admin/config/defaults`
Partial update of defaults (patch semantics).

Body (JSON, any subset):
- `openingMode`: `morning|night|both`
- `morningHours`: `string[]` (`HH:MM`)
- `nightHours`: `string[]` (`HH:MM`)
- `weekdayOpen`: objeto parcial o completo con claves `monday..sunday` y valores booleanos
- `dailyLimit`: number
- `mesasDeDosLimit`: string (`0..999`, `sin_limite` supported)
- `mesasDeTresLimit`: string (`0..999`, `sin_limite` supported)
- `allowFloorReservation`: boolean (toggle global "Permitir reserva de planta")
- `allowSalonReservation`: boolean (toggle global "Permitir reserva de salón")

Response:
- Same shape as `GET /api/admin/config/defaults`.

### `GET /api/admin/config/location-booking?date=YYYY-MM-DD`
Returns the location booking toggles for a date: global defaults, per-date override (null = inherit) and the effective values.

Response:
- `{ success: true, date, global: { allowFloorReservation, allowSalonReservation }, override: { allowFloorReservation: bool|null, allowSalonReservation: bool|null }, effective: { allowFloorReservation, allowSalonReservation } }`
- `override` flags are `null` when the date inherits the global default.

### `POST /api/admin/config/location-booking`
Writes per-date overrides from plain toggle switches.

Body (JSON, any subset):
- `date`: `YYYY-MM-DD` (required)
- `allowFloorReservation`: boolean
- `allowSalonReservation`: boolean

Semantics: toggling a flag to the same value as the global default clears that
override (the date inherits the default again); a different value pins it.

Response: same shape as `GET /api/admin/config/location-booking`.

### `POST /api/admin/config/floors/date`
Sets how many floors exist for one date, overriding the global default.

Body (JSON):
- `date`: `YYYY-MM-DD` (required)
- `count`: 1..8 (required)

Semantics:
- Increasing beyond the global floors creates **date-scoped** floors (`restaurant_floors.specific_date = date`), visible only on that date (a date-scoped floor with the same `floor_number` shadows the global one).
- Decreasing deactivates floors for that date via `restaurant_floor_overrides`; date-scoped rows created for that date are deleted outright. Global floors are never deleted.

Response: `{ success: true, date, floors: [...] }` — same floor shape as `GET /api/admin/config/floors?date=`, with `dateScoped` set on date-scoped rows.

### `GET /api/admin/config/day?date=YYYY-MM-DD`
Returns open/closed day state.
- Fallback si no existe override en `restaurant_days`: se usa `weekdayOpen` de `restaurant_reservation_defaults`.

Response:
- `{ success: true, date, isOpen }`

### `POST /api/admin/config/day`
Upserts open/closed day state. A range sends every calendar day explicitly; each becomes one `restaurant_days` row keyed by `(restaurant_id, date)`.

Body (JSON), one of:
- Single day: `date` (`YYYY-MM-DD`), `isOpen` (boolean)
- Range: `dates` (`YYYY-MM-DD[]`), `rangeDates: true`, `isOpen` (boolean)

Response:
- Single day: `{ success: true, date, isOpen }`
- Range: `{ success: true, dates, isOpen }`

### `GET /api/admin/config/opening-hours?date=YYYY-MM-DD`
Returns daily opening config. If no per-date row exists, falls back to restaurant defaults.

Response:
- `{ success: true, date, openingMode, morningHours, nightHours, hours, source }`
- `source`: `default|override`

### `POST /api/admin/config/opening-hours`
Upserts opening hours for a specific date.

Body (JSON):
- `date` (`YYYY-MM-DD`)
- Recommended:
  - `openingMode`: `morning|night|both`
  - `morningHours`: `string[]`
  - `nightHours`: `string[]`
- Legacy-compatible:
  - `hours`: `string[]`

Response:
- `{ success: true, date, openingMode, morningHours, nightHours, hours, source: "override" }`

### `GET /api/admin/config/daily-limit?date=YYYY-MM-DD`
Returns daily pax limit and occupancy summary. If no row exists in `reservation_manager`, falls back to defaults.

Response:
- `{ success: true, date, limit, totalPeople, freeBookingSeats, source }`

### `POST /api/admin/config/daily-limit`
Upserts daily pax limit for one date.

Body (JSON):
- `date` (`YYYY-MM-DD`)
- `limit` (number)

Response:
- `{ success: true, date, limit }`

### `GET /api/admin/config/mesas-de-dos?date=YYYY-MM-DD`
Returns per-date mesas de dos limit with fallback to defaults.

Response:
- `{ success: true, date, limit, source }`

### `POST /api/admin/config/mesas-de-dos`
Upserts per-date mesas de dos limit.

Body (JSON):
- `date` (`YYYY-MM-DD`)
- `limit` (string; `sin_limite` supported)

Response:
- `{ success: true, date, limit, source: "override" }`

### `GET /api/admin/config/mesas-de-tres?date=YYYY-MM-DD`
Returns per-date mesas de tres limit with fallback to defaults.

Response:
- `{ success: true, date, limit, source }`

### `POST /api/admin/config/mesas-de-tres`
Upserts per-date mesas de tres limit.

Body (JSON):
- `date` (`YYYY-MM-DD`)
- `limit` (string; `sin_limite` supported)

Response:
- `{ success: true, date, limit, source: "override" }`

### `GET /api/admin/config/floors/defaults`
Returns default floor setup for the active restaurant.

Response:
- `{ success: true, floors: Floor[] }`

### `POST /api/admin/config/floors/defaults`
Mutates default floor setup.

Body (JSON):
- Resize set: `{ count }` (min `1`, max `8`)
- Toggle one floor: `{ floorNumber, active }`
- Set floor aforo: `{ floorNumber, maxAforo }`
  - `maxAforo`: number of guests the floor may hold; `0` = no limit (unbounded).
  - **Invariant:** setting a cap below the sum of the floor's salons' capacities is rejected (`{ success: false, aforoCapped: true, remainingAforo, totalSalonAforo }`); `maxAforo: 0` (unbounded) is always accepted.

Response:
- `{ success: true, floors: Floor[] }`
- On aforo-cap rejection: `{ success: false, message, aforoCapped: true, remainingAforo, totalSalonAforo }`

### `GET /api/admin/config/floors?date=YYYY-MM-DD`
Returns floor activation for one date (default + per-date overrides merged).

Response:
- `{ success: true, date, floors: Floor[] }`

### `POST /api/admin/config/floors`
Upserts one per-date floor override.

Body (JSON):
- `date` (`YYYY-MM-DD`)
- `floorNumber` (number)
- `active` (boolean)
- `maxAforo` (number, optional) — per-date aforo cap; `0` = unbounded. Same invariant as above.

Response:
- `{ success: true, date, floors: Floor[] }`

`Floor`:
- `{ id, floorNumber, name, isGround, active, maxAforo?, totalSalonAforo? }`
- `maxAforo`: this floor's aforo cap (`0` = unbounded).
- `totalSalonAforo`: backend-computed sum of the capacities of this floor's salons that have a capacity limit.

## Public Menu / Navigation

### `GET /api/menu-visibility` (alias: `GET /menu-visibility`)
Returns the current visibility flags used by navigation.

Response:
- `{ success: true, menuVisibility: { menudeldia: boolean, menufindesemana: boolean, ... } }`

### `GET /api/menus/home`
Returns lightweight active menu data for homepage cards.

Response:
- `{ success: true, count, menus: HomeMenu[] }`

`HomeMenu`:
- `id`, `slug`, `menu_title`, `menu_type`, `active`
- `menu_subtitle` (`string[]`)
- `menu_title_english` (optional string)
- `menu_subtitle_english` (optional `string[]`; aligned with `menu_subtitle`)
- `show_dish_images`, `show_menu_preview_image` (boolean)
- `menu_preview_image_url`, `special_menu_image_url` (string)

English fields are returned when stored translations exist. Consumers should fall back to Spanish per missing field or array item.

### `GET /api/menus/public`
Returns the active public menu catalog sourced from `menusDeGrupos` + V2 sections/dishes.

Behavior:
- Returns only menus with `active=1` and `is_draft=0`.
- Returns only supported public types:
  - `closed_conventional`
  - `a_la_carte`
  - `special`
  - `closed_group`
  - `a_la_carte_group`
- If a menu has no V2 sections/dishes, fallback sections are derived from legacy snapshot fields (`entrantes`, `principales`, `postre`).

Response:
- `{ success: true, count, menus: PublicMenu[] }`

`PublicMenu`:
- `id` (number)
- `slug` (string; stable route slug built from title + id)
- `menu_title` (string)
- `menu_type` (string)
- `price` (string)
- `active` (boolean)
- `menu_subtitle` (`string[]`)
- `entrantes` (`string[]`)
- `principales` (`{ titulo_principales: string, items: string[] }`)
- `postre` (`string[]`)
- `settings`:
  - `included_coffee` (boolean)
  - `beverage` (object)
  - `comments` (`string[]`)
  - `min_party_size` (number)
  - `main_dishes_limit` (boolean)
  - `main_dishes_limit_number` (number)
- `show_dish_images` (boolean; legacy toggle for preview image cards)
- `show_menu_preview_image` (boolean; toggles menu-level preview hero image)
- `menu_preview_image_url` (string; Bunny pull URL when menu preview image exists)
- `sections` (`PublicMenuSection[]`)
- `special_menu_image_url` (string; full Bunny pull URL when set, empty when not set)
- `legacy_source_table` (string; optional, e.g. `DIA|FINDE`)
- `created_at`, `modified_at` (string)

`PublicMenuSection`:
- `id` (number)
- `title` (string)
- `kind` (string; normalized kind)
- `position` (number)
- `annotations` (`string[]`)
- `dishes` (`PublicMenuDish[]`)

`PublicMenuDish`:
- `id` (number)
- `title` (string)
- `description` (string)
- `allergens` (`string[]`)
- `supplement_enabled` (boolean)
- `supplement_price` (number|null)
- `price` (number|null)
- `position` (number)
- `foto_url` (string|null; full Bunny pull URL when dish image exists)

### `GET /api/menus/dia`
Response:
- `{ success: true, entrantes: Dish[], principales: Dish[], arroces: Dish[], precio: string }`

### `GET /api/menus/finde`
Same shape as `/api/menus/dia`.

`Dish`:
- `{ descripcion: string, alergenos: string[] }`

### `GET /api/postres`
Response:
- `{ success: true, postres: Dish[] }`

---

## Wines (Public + Admin)

### `GET /api/vinos`
Query params:
- `tipo` (required unless `num` is provided): `TINTO|BLANCO|CAVA`
- `active` (optional, default `1`)
- `include_image` (optional, default `true`; includes `foto_url` when `1`)
- `num` (optional): returns a single wine by id (overrides `tipo`)

Response:
- `{ success: true, vinos: Vino[] }`
- Sets `ETag`; supports `If-None-Match` (returns `304`).

`Vino`:
- `num` (int), `nombre` (string), `precio` (number), `descripcion` (string), `bodega` (string)
- `denominacion_origen` (string), `tipo` (string), `graduacion` (number), `anyo` (string)
- `active` (0|1), `has_foto` (bool)
- If `include_image=1`: `foto_url` (string, BunnyCDN URL)

### `GET /api/api_vinos.php` (legacy GET alias)
Same behavior as `GET /api/vinos`.

### `POST /api/vinos` (admin)
Same behavior as `POST /api/api_vinos.php`.

### `POST /api/api_vinos.php` (admin)
Form fields:
- `action`: `update_status|delete|update|add`

Actions:
- `update_status`: `wineId`, `status` (0|1) -> `{ success: true }`
- `delete`: `wineId` -> `{ success: true }`
- `update`: `wineId`, `nombre`, `precio`, plus optional fields `descripcion`, `bodega`, `denominacion_origen`, `graduacion`, `anyo`,
  - optional image: `imageBase64` (preferred) or file upload `image`
  - -> `{ success: true }` or `{ success: true, warning: string }`
- `add`: `tipo`, `nombre`, `precio`, `bodega` (required), plus optional fields above,
  - optional image: `imageBase64` or file `image`
  - -> `{ success: true, wineId: number }` or `{ success: true, wineId: number, warning: string }`

---

## Menu Visibility (Legacy Admin)

### `POST /api/menuVisibilityBackend/toggleMenuVisibility.php` (admin)
Body:
- JSON or form: `menu_key` and `is_active` (bool-ish: `true|false|1|0|yes|no`)

Response:
- `{ success: true, message: string, menu: {...} }`

---

## Menu Admin (DIA / FINDE)

### `POST /api/updateDishDia.php` (admin)
Legacy form endpoint for `DIA` table:
- Add dish: `anyadeEntrante|anyadePrincipal|anyadeArroz` + `inputText` + `selectedAlergenos[]`/`selectedAlergenos2[]`/`selectedAlergenos3[]`
- Update dish: `update` + `formID` + `inputText` + `selectedAlergenos[]`
- Delete dish: `eliminaplato` + `formID`
- Toggle active (legacy): `toggleActive` + `dishId` + `newStatus`

Response:
- `{ status: "success|error", success: boolean, message: string, newId?: number }`

### `POST /api/toggleDishStatusDia.php` (admin)
Form:
- `dishId` (int), `isActive` (bool-ish)

Response:
- `{ status: "success", success: true, dishId: number, newStatus: 0|1 }`

### `GET /api/searchDishesDia.php`
Query:
- `searchTerm` (string)

Response:
- `{ status: "success|error", success: boolean, matchingIds: number[] }`

### `POST /api/updateDish.php` (admin)
Same behavior as `updateDishDia.php` but for `FINDE` table.

### `POST /api/toggleDishStatus.php` (admin)
Form:
- `dishId` (int), `isActive` (bool-ish)
- `table` (optional): defaults to `FINDE`; supports `POSTRES`

Response:
- `{ status: "success", success: true, dishId: number, newStatus: 0|1 }`

### `GET /api/searchDishesFinde.php`
Same behavior as `searchDishesDia.php` but searches `FINDE`.

---

## Postres Admin

### `GET|POST /api/updatePostre.php` (admin)
Actions (JSON or form):
- `getPostres` -> returns `{ status: "success", active: [...], inactive: [...] }`
- `addPostre`: `descripcion`, `alergenos` -> `{ status: "success", newId: number }`
- `updatePostre`: `num`, `descripcion`, `alergenos`
- `deletePostre`: `num`
- `toggleActive`: `num`, `active`

### `GET /api/searchPostres.php` (admin)
Query:
- `searchTerm`

Response:
- `{ status: "success|error", matchingIds: number[] }`

---

## Group Menus (menusDeGrupos)

### `GET /api/menuDeGruposBackend/getAllMenus.php`
Response:
- `{ success: true, menus: MenuDeGrupo[] }`

### `GET /api/menuDeGruposBackend/getMenu.php?id=<id>`
Response:
- `{ success: true, menu: MenuDeGrupo }`

### `GET /api/menuDeGruposBackend/getActiveMenusForDisplay.php` (also `/getActiveMenusForDisplay`)
Slim list of active group menus (`closed_group`, `a_la_carte_group`). Each menu
carries only `id`, `menu_title` and (when a translation exists)
`menu_title_english`. Use `getMenuForDisplay` for full per-menu content.

Response:
- `{ success: true, count: number, menus: [{ id, menu_title, menu_title_english? }] }`

### `GET /api/menuDeGruposBackend/getMenuForDisplay?id=<id>` (alias: `/getMenuForDisplay.php`)
Full display payload for ONE active group menu (must be `closed_group` or
`a_la_carte_group`, active, belonging to the resolved restaurant). Includes the
legacy blobs (menu_subtitle, entrantes, principales, postre, beverage,
comments, ...) enriched with v2 dish details (`nombre`, `alergenos`,
`suplemento`, `suplemento_activo`, `descripcion_enabled`) and English
translations. Every dish carries the explicit boolean `descripcion_enabled`;
dishes persisted with `description_enabled = 0` return
`descripcion_enabled: false` WITHOUT a `descripcion` field.

Response:
- `{ success: true, menu: MenuDeGrupoDisplay }` or `{ success: false, message }` when the id is invalid/unknown.

### `POST /api/menuDeGruposBackend/addMenu.php` (admin)
Accepts JSON or `multipart/form-data` (from legacy axios).

### `POST|PUT /api/menuDeGruposBackend/updateMenu.php` (admin)
Accepts JSON or `multipart/form-data`.

### `POST /api/menuDeGruposBackend/toggleActive.php` (admin)
Body:
- `id`, `active`

### `POST|DELETE /api/menuDeGruposBackend/deleteMenu.php` (admin)
Body:
- `id`

---

## Reservations Availability Helpers

### `GET /api/reservations/rice-types`
Returns active rice options for the reservation UI.

Response:
- `{ success: true, riceTypes: string[], riceTypesEnglish?: string[] }`
- `riceTypesEnglish` aligns by index with `riceTypes`; missing translations are empty strings.

### `GET /api/reservations/group-menus?party_size=<number>`
Returns active group menus valid for party size. Menu payload includes optional `menu_title_english`, `entrantes_english`, and `principales_english` (`{ titulo_principales, items }`). English arrays align with Spanish source arrays.

### `GET /api/reservations/mandatory-menus?date=YYYY-MM-DD`
Returns mandatory/recommended menus for date. Menu payload includes optional `menuTitleEnglish`, `entrantesEnglish`, and `principalesEnglish` (`{ titulo_principales, items }`).

### `POST /api/fetch_daily_limit.php`
Form:
- `date` (`YYYY-MM-DD`)

Response:
- `{ success: true, date, dailyLimit, totalPeople, freeBookingSeats }`

### `GET /api/reservations/month-availability`
Query params:
- `month` (int `1-12`)
- `year` (int)

Response:
- `{ success: true, month: number, year: number, availability: { [YYYY-MM-DD]: { dailyLimit: number, totalPeople: number, freeBookingSeats: number } } }`
- An explicit `restaurant_days.is_open = false` override reports `dailyLimit: 0` and `freeBookingSeats: 0` for that date.

### `GET /api/reservations/closed-days`
Query params:
- `from` (`YYYY-MM-DD`)
- `to` (`YYYY-MM-DD`)

Response:
- `{ success: true, closed_days: string[], opened_days: string[] }`

---

## Opening Hours (Legacy Admin UI)

### `GET /api/getopeninghours.php`
Returns the opening hours configuration from `openinghours`.

### `POST /api/editopeninghours.php` (admin)
Upserts `openinghours` and removes `hour_configuration` legacy rows (mirrors PHP behavior).

---

## Hour Percentages

### `GET /api/gethourpercentages.php`
Returns hour-percentage configuration used by reservation capacity logic.

### `POST /api/updatehourpercentages.php` (admin)
Updates hour-percentage configuration.

---

## Calendar Data

### `GET /api/get_calendar_data.php`
Returns monthly/day availability data for legacy admin UIs (cached + `ETag`).

---

## Group Menus Helpers

### `GET /api/getValidMenusForPartySize.php`
Query:
- `party_size` (int, required)

Response:
- `{ success: true, hasValidMenus, count, menus: [...] }`

Notes:
- Returns only active rows from `menusDeGrupos` where `menu_type = closed_group`.
- Applies `min_party_size <= party_size`.

---

## Automation / Modification Endpoints (n8n)

### `GET|POST /api/get_booking_availability_context.php`
Returns booking availability context used by n8n flows (month availability, limits, closed days, etc.).

### `GET /api/get_available_rice_types.php`
Returns available rice types for automation.

### `POST /api/check_date_availability.php`
Checks if a booking date change is possible (capacity/closed day).

### `POST /api/check_party_size_availability.php`
Checks if a party size change is possible (capacity).

### `POST /api/validate_booking_modifiable.php`
Validates whether a booking can be modified.

### `POST /api/update_reservation.php` (alias: `POST /update_reservation.php`)
Updates an existing booking from automation flows.

### `POST /api/save_modification_history.php`
Stores booking modification history (creates `modification_history` table if missing).

### `POST /api/notify_restaurant_modification.php`
Best-effort notification to restaurant staff (WhatsApp via UAZAPI if configured).

---

## n8n Reminders

### `GET /api/n8nReminder.php` (alias: `GET /n8nReminder.php`)
Internal endpoint that sends WhatsApp reminder buttons (confirm + optional rice) for bookings in the next 48 hours.

Auth:
- Requires `X-Api-Token` matching `INTERNAL_API_TOKEN`.

Response:
- `{ success, total, confirmation_sent, rice_sent, failed, details: [...] }`

---

## Public WhatsApp Pages (HTML)

These are legacy PHP pages ported to Go (served as HTML). They are used from WhatsApp links and must exist at the root path.

### `GET|POST /confirm_reservation.php`
Confirms a booking (`bookings.status='confirmed'`).

### `GET|POST /cancel_reservation.php`
Cancels a booking (moves to `cancelled_bookings`, deletes from `bookings`).

### `GET|POST /book_rice.php`
Allows clients to select a rice type and servings for an existing booking (writes JSON arrays to `bookings.arroz_type` and `bookings.arroz_servings`).

---

## Navidad Booking

### `POST /api/navidad_booking.php`
Legacy Navidad contact form handler (rate-limited; WhatsApp best-effort via UAZAPI if configured).

---

## Marketing (Legacy Tool)

### `POST /api/emailAdvertising/sendEmailAndWhastappAd.php` (alias: `POST /emailAdvertising/sendEmailAndWhastappAd.php`) (admin)
Query params:
- `action=send`
- `type=all|email|whatsapp`

Notes:
- Email sending is stubbed (no SMTP configured in Go).
- WhatsApp is sent via UAZAPI if `UAZAPI_URL` + `UAZAPI_TOKEN` are configured.

## Public reservation helpers (modern aliases)

The Preact frontend uses these modern `/api/reservations/*` aliases (same handlers as the
legacy `*.php` endpoints; the `.php` paths remain for legacy PHP compatibility).

### `GET /api/reservations/two-top-availability?date=<YYYY-MM-DD>`
Alias of `POST /api/fetch_mesas_de_dos.php` (GET, `date` query param).
Response: `{ success: true, disponibilidadDeDos: boolean, limiteMesasDeDos: number, mesasDeDosReservadas: number }`

### `GET /api/reservations/hour-data?date=<YYYY-MM-DD>`
Alias of `GET /api/gethourdata.php` (`date` query param). Response shape identical to legacy handler
(hourly booking data + daily limit + salon state).

### `GET /api/reservations/day-context?date=<YYYY-MM-DD>`
Alias of `GET /api/get_reservation_day_context.php` (`date` query param). Response shape identical to
legacy handler (defaults, opening mode, morning/night hours, closed-day info).

Adds a `party_size` query param (positive int, optional): when present, floors and
`locationBooking.floors` salons whose remaining aforo is below the party size are gated out
(removed from `activeFloors` / the salon's `active` flag) so the booking form cannot offer a
floor/salon that cannot physically seat the party.

Additionally includes `locationBooking`:
- `allowFloorReservation` / `allowSalonReservation`: effective toggles for the date (per-date override ?? global default).
- `floors`: active floors for the date, each with nested active `salons`, plus aforo fields.

Aforo (capacity) fields on floors and salons:
- Floor (`floors`, `activeFloors`): `maxAforo` (0 = unbounded), `occupancy`, `remaining` (remaining = maxAforo − occupancy; 0 when maxAforo is 0/unbounded).
- Salon (`locationBooking.floors[].salons`): `capacityLimit`, `occupancy`, `remaining`.

```json
"locationBooking": {
  "allowFloorReservation": true,
  "allowSalonReservation": true,
  "floors": [{
    "id": 1, "floorNumber": 0, "name": "Planta baja", "isGround": true,
    "maxAforo": 0, "occupancy": 0, "remaining": 0,
    "salons": [{ "id": 2, "name": "Salón principal", "capacityLimit": 44, "occupancy": 0, "remaining": 44 }]
  }]
}
```

Occupancy is tracked in the `reservation_location_occupancy` ledger (scope `salon`/`floor`,
`target_id`, `count`) and is adjusted on booking insert, cancel, and modify paths so the aforo
remaining reflects live confirmed bookings for the date.

Front booking forms may also send `preferred_salon_id` (optional int) alongside `preferred_floor_number`; it is validated against the active salons for the date (and floor) and stored in `bookings.preferred_salon_id`. Booking list/search/detail responses include `preferred_salon_id` (null when unset).

### `POST /api/fetch_mesas_de_dos.php`
Form:
- `date` (`YYYY-MM-DD`)

Response:
- `{ success: true, disponibilidadDeDos: boolean, limiteMesasDeDos: number, mesasDeDosReservadas: number }`

### `POST /api/update_daily_limit.php` (admin)
Form:
- `date` (`YYYY-MM-DD`), `daily_limit` (int)

Response:
- `{ success: true, message: string, date: string, dailyLimit: number }`

### `POST /api/limitemesasdedos.php` (admin)
Form:
- `date` (`YYYY-MM-DD`, optional), `daily_limit` (`0-40|999|sin_limite`)

Response:
- `{ success: true, message: string }`

### `POST /api/get_mesasdedos_limit.php` (admin)
Form:
- `date` (`YYYY-MM-DD`, optional)

Response:
- `{ success: true, daily_limit: string, message: string }`

### `POST /api/check_day_status.php` (admin)
Form:
- `date` (`YYYY-MM-DD`)

Response:
- `{ success: true, date: string, weekday: string, is_open: boolean, is_default_closed_day: boolean }`

### `POST /api/open_day.php` (admin)
Form:
- `date` (`YYYY-MM-DD`)

Response:
- `{ success: true, message: string, date: string, is_open: true }`

### `POST /api/close_day.php` (admin)
Form:
- `date` (`YYYY-MM-DD`)

Response:
- `{ success: true, message: string, date: string, is_open: false }`

### `POST /api/fetch_occupancy.php` (admin)
Form:
- `date` (`YYYY-MM-DD`)

Response:
- `{ success: true, totalPeople: number, dailyLimit: number, date: string, status: "OK" }`

---

## Hours Configuration (Legacy `/api/*` in PHP)

### `GET /api/gethourdata.php?date=YYYY-MM-DD`
Returns hour slots for a date combining:
- `openinghours.hoursarray` defaults
- any per-date overrides from `hour_configuration`
- occupancy-derived capacity and status fields

### `POST /api/savehourdata.php` (admin)
JSON body:
- `{ date: "YYYY-MM-DD", data: [...] }`

Upserts into `hour_configuration`.

---

## Booking Creation

### `POST /api/bookings/front`
Ruta pública canónica usada por Preact en `/reservas`.

Alias legacy compatible:
- `POST /api/insert_booking_front.php`

Form:
- `website_url` (honeypot; debe ir vacío)
- `form_load_time` (unix timestamp en segundos; protección anti-bot)
- `reservation_date`
- `party_size`
- `reservation_time`
- `customer_name`
- `contact_email`
- `country_code`
- `contact_phone`
- `adults` o `children`
- `preferred_floor_number` (opcional)
  - Si hay una sola planta activa para ese día se autoasigna.
  - Si hay varias plantas activas, el frontend debe enviar selección.
- `toggleArroz`
- `arroz_type` / `arroz_servings` o `arroz_types_json` / `arroz_servings_json`
- `baby_strollers`
- `high_chairs`
- `menu_de_grupo_selected`
- `menu_de_grupo_id`
- `principales_enabled`
- `principales_json` (JSON array)

Persistencia:
- Inserta en `bookings` los datos básicos de la reserva, teléfono + prefijo, niños derivados de `adults`, accesorios, arroz, menú de grupo, `principales_json` y `preferred_floor_number`.

Response (success):
- `{ success: true, booking_id: number, message: "¡Reserva realizada con éxito!", notifications_sent: true, email_sent: boolean, whatsapp_sent: true }`

Response (WhatsApp failed):
- `{ success: false, message: "Error enviando confirmación por WhatsApp: ...", error_code: "WHATSAPP_FAILED", booking_id: number }` (HTTP 503)

Response (Email failed):
- `{ success: false, message: "Error enviando confirmación por email: ...", error_code: "EMAIL_FAILED", booking_id: number, whatsapp_sent: true }` (HTTP 503)

Notificaciones:
- Envía WhatsApp al cliente vía UAZAPI (botón con condiciones + cancelar). Requiere `uazapi_url` + `uazapi_token` en `restaurant_integrations` o env vars `UAZAPI_URL`/`UAZAPI_TOKEN`.
- Envía email de confirmación al cliente y al restaurante vía SMTP usando `email_provider_config` por restaurante (migration `043_email_provider_config.sql`). Para `provider='smtp'` requiere `smtp_host`, `smtp_port`, `smtp_username`, `smtp_password`, `smtp_encryption` (`none|tls|ssl`) e `is_active=1`. Para `provider='gmail'` usa `smtp.gmail.com:587` con `gmail_from_email` + `gmail_app_password`. Las env vars `SMTP_HOST/PORT/USERNAME/PASSWORD` ya no se consultan.
- Ambas notificaciones son **síncronas y obligatorias**: si fallan, la reserva queda en BD pero se devuelve error al cliente.

### `GET /api/get_reservation_day_context.php?date=YYYY-MM-DD`
Contexto operativo del día para el formulario público de reservas.

Response:
- `{ success: true, date, openingMode, morningHours, nightHours, floors, activeFloors }`
- `openingMode`: `both | morning | night`
- `floors`: estado de plantas para la fecha
- `activeFloors`: subconjunto de `floors` con `active=true`

### `POST /api/insert_booking.php` (admin)
Form:
- `date`, `time`, `nombre`, `phone`, `special_menu`, etc.

Response (success):
- `{ success: true, booking_id: number, notifications_sent: true, email_sent: boolean, whatsapp_sent: true }`

Response (WhatsApp/Email failed): Same error pattern as `POST /api/bookings/front`.

El email de confirmación usa la misma resolución que `POST /api/bookings/front`: SMTP desde `email_provider_config` por restaurante (`provider='smtp'` o `'gmail'`, `is_active=1`), sin env vars `SMTP_HOST/PORT/USERNAME/PASSWORD`.

- The confirmation email's contact block (phone/email/address) is sourced from `restaurant_info` (`telefono`/`email`/`direccion`) and the header name + logo from `restaurant_branding` (`brand_name`/`logo_url`). All five fields are editable from the backoffice at `/app/config?content=contacto`; the logo is an image upload that gets normalized to WebP ≤50 KB and stored on BunnyCDN (see `POST /api/admin/branding/logo`).

### `POST /api/admin/branding/logo`
Upload (or replace) the restaurant's email-header logo.

- Auth: `bo_session` cookie + `ajustes` section (`requireBOSession` + `ajustesGate`).
- Body: `multipart/form-data` with one field `image` (jpg/png/webp, max 8 MB raw upload).
- Processing: ImageMagick normalizes to WebP, sweeping dimensions down to 460 px / quality 28 to enforce a hard 50 KB cap.
- Storage: uploaded to BunnyCDN at `branding/{restaurantId}/logo.webp`; the resulting pull URL is upserted into `restaurant_branding.logo_url` (other columns preserved).
- Response (success): `{ success: true, logoUrl: "https://<pull-base>/branding/{id}/logo.webp" }`.
- Errors:
  - `400` if the image type is not jpg/png/webp, if the file is empty / > 8 MB, or if ImageMagick can't reduce the result below 50 KB.
  - `401` if no `bo_session`.
  - `500` if the Bunny upload or DB upsert fails.
  - `503` if Bunny storage is not configured (`BunnyStorageKey` / `BunnyStorageZone` / `BunnyPullBaseURL` missing).
---


## Booking Admin (confreservas.php)

### `POST /api/fetch_bookings.php` (admin)
Form:
- `date` (required `YYYY-MM-DD`)
- `page` (optional), `page_size` (optional)
- `all` (optional bool-ish)
- `time_sort` / `date_added_sort` (`asc|desc|none`)

Response:
- `{ success: true, bookings: [...], totalPeople: number, total_count: number, page, page_size, total_pages, is_all }`

### `POST /api/get_booking.php` (admin)
Form:
- `id`

Response:
- `{ success: true, booking: {...} }`

### `POST /api/edit_booking.php` (admin)
URL-encoded form (legacy):
- Expects the same keys used by the legacy UI (see `confreservas.php` JS mapping).

Response:
- `{ success: true }` or `{ success: false, message }`

### `POST /api/delete_booking.php` (admin)
Form:
- `id`

### `POST /api/update_table_number.php` (admin)
JSON body:
- `{ id, table_number }`

### `POST /api/get_reservations.php` (admin)
Form:
- `start_date`, `end_date`

### `POST /api/fetch_cancelled_bookings.php` (admin)
Form:
- `date` (`YYYY-MM-DD`, optional)

### `POST /api/reactivate_booking.php` (admin)
Form:
- `id`

---

## Salón Condesa

### `GET /api/salon_condesa_api.php?date=YYYY-MM-DD`
Response:
- `{ success: true, date, state: 0|1 }`

### `POST /api/salon_condesa_api.php` (admin)
JSON or form:
- `date`, `state`

Response:
- `{ success: true }`

## Backoffice Premium API (`/api/admin/premium/*`)

Auth:
- Requires authenticated backoffice session cookie (`bo_session`).
- Uses the same RBAC model as `/api/admin/*` (typically `ajustes` access for management actions).

Endpoints:
- `GET /api/admin/premium/website`
- `PUT /api/admin/premium/website`
- `GET /api/admin/website/menu-templates`
- `PUT /api/admin/website/menu-templates`
- `GET /api/admin/premium/areas`
- `POST /api/admin/premium/areas`
- `PATCH /api/admin/premium/areas/{id}`
- `DELETE /api/admin/premium/areas/{id}`
- `GET /api/admin/premium/tables`
- `POST /api/admin/premium/tables`
- `PATCH /api/admin/premium/tables/{id}`
- `DELETE /api/admin/premium/tables/{id}`
- `GET /api/admin/premium/recurring-invoices`
- `POST /api/admin/premium/recurring-invoices`
- `PATCH /api/admin/premium/recurring-invoices/{id}`
- `GET /api/admin/premium/domains`
- `POST /api/admin/premium/domains`

Response basics:
- Success: `{ "success": true, ... }`
- Error: `{ "success": false, "message": "..." }`

## Backoffice Tables Map (`/api/admin/tables*`)

Auth:
- Requires backoffice session cookie (`bo_session`) and `reservas` section access.

### `GET /api/admin/tables`

Query params (optional):
- `date` (`YYYY-MM-DD`): aplica overrides de layout por dia.
- `floor_number` (int >= 0): combinado con `date`, aplica overrides por salon/planta.

Response:
- `{ success: true, data: Area[], areas: Area[], tables: Table[] }`
- `Area`: incluye `tables` (mesas de esa area).
- `Table` incluye como minimo:
  - `id`, `area_id`, `name`, `capacity`, `status`, `x_pos`, `y_pos`
  - `numero_mesa` (string, identificador unico por restaurante, p.ej. `"4"`, `"4B"`, `"4-B"`; se muestra en el nodo del mapa)
  - `shape` (`round|square`)
  - `fill_color`, `outline_color`, `style_preset`, `texture_image_url`
  - `metadata` (si existe en DB)

### `POST /api/admin/tables`

Body JSON:
- `entity`: `"table"` (default) o `"area"`.
- Para `table`: admite `area_id`, `name`, `numero_mesa` (string alfanumerico unico por restaurante, máx 32 caracteres; si se omite, el backend deriva el siguiente numero libre), `capacity|seats`, `status`, `shape`, `fill_color`, `outline_color`, `style_preset`, `texture_image_url`, `x_pos`, `y_pos`, `is_active`, `metadata`.
- Para `area`: admite `name`, `display_order|sort_order`, `is_active`, `metadata`.
- Conflictos: si `numero_mesa` (o `name`) ya existe en el restaurante, responde `409` con `{ success: false, code: "TABLES_CREATE_CONFLICT", message }`.

Response:
- `table`: `{ success: true, entity: "table", item: Table, table: Table }`
- `area`: `{ success: true, entity: "area", item: Area }`

### `PUT /api/admin/tables`

Body JSON:
- `id` (required) + mismos campos opcionales de `POST` para actualizar.
- `numero_mesa` es editable y debe ser unico por restaurante; conflicto -> `409` con `code: "TABLES_CREATE_CONFLICT"`.
- Para posicion por layout diario: incluir `date` + `floor_number` junto a `x_pos`/`y_pos`.
- `entity: "layout"` permite guardar metadata de mapa por dia/planta (por ejemplo `elements`, `booking_states`) usando `date` + `floor_number` + `metadata`.
- En `metadata.elements[]` se soporta `display_mode` por elemento (`"asset" | "text" | "both"`). Si falta o es inválido se normaliza a `"both"`.

Response:
- `{ success: true, entity: "table"|"area", item: ... }`

### `POST /api/admin/tables/{id}/texture-image`

Multipart form:
- `image` (jpg/png/webp/gif).  
- Se normaliza a `image/webp` y se fuerza salida <= `150KB`.
- Se sube a BunnyCDN y se guarda `texture_image_url` en la mesa.

Response:
- `{ success: true, id, imageUrl }`

### `DELETE /api/admin/tables/{id}`

Elimina la mesa del restaurante activo.

Response:
- `{ success: true, entity: "table", id }`

Errores:
- `404/409` con `code: "TABLES_DELETE_CONFLICT"`: mesa inexistente, o con servicios abiertos en el TPV (`pos_visits` FK `ON DELETE RESTRICT`).
- Broadcast WS: `table_deleted` con `{ id }`.

### `GET /api/admin/tables/ws`

WebSocket events:
- `hello`, `snapshot`, `table_created`, `table_updated`, `area_created`, `area_updated`.
- Para eventos de mesa, payload incluye `table` normalizada (incluyendo campos de estilo/texture cuando existan).
- Eventos POS de día de caja: `pos_cash_day_opened` y `pos_cash_day_closed` llevan
  `data.cashDay` con el día completo, para que un cliente recién conectado no
  tenga que volver a pedirlo; `pos_cash_day_totals` lleva `data` con `date`,
  `totalGrossCents`, `covers` y `ticketCount`, y se emite tras cada cobro,
  devolución, cierre de visita y ajuste de comensales. Los totales salen del
  mismo agregado que `GET /pos/cash-days`, así que la cifra del socket y la del
  endpoint no pueden divergir. El broadcast ocurre después de confirmar la
  operación y nunca la revierte: una venta cobrada no se deshace porque el hub
  falle.

`GET /api/admin/website/menu-templates` response:
- `default_theme_id`: plantilla fallback para la web premium.
- `overrides`: map por tipo de menu (`closed_conventional`, `a_la_carte`, `closed_group`, `a_la_carte_group`, `special`).
- `themes`: catalogo de plantillas disponibles.
- `assigned`: `true` cuando el restaurante tiene al menos una plantilla asignada en configuracion (default o override), `false` en caso contrario.

## Member phone

### `PUT /api/admin/members/{id}/phone`

Requires backoffice session + `miembros` section + high-admin role. Updates `phone` and `whatsapp_number` for active member in active restaurant.

Request body:

```json
{
  "phone": "+34612345678"
}
```

Response: `{ "success": true, "member": Member }`. Invalid phone returns `400`; unknown member returns `404`.

## WhatsApp Premium Multi-Tenant Onboarding (`/api/admin/members/whatsapp/*`)

Requires backoffice session + `miembros` section + high-admin role (same as existing WhatsApp premium endpoints).

### `POST /api/admin/members/whatsapp/subscribe`

Activates WhatsApp Pack recurring feature and now attempts automatic provisioning + connect bootstrap.

Request body:

```json
{}
```

Response (`200`):

```json
{
  "success": true,
  "message": "Conexion iniciada. Escanea el QR en WhatsApp para completar el enlace",
  "connected": false,
  "subscription": {
    "feature_key": "whatsapp_pack",
    "frequency": "monthly",
    "amount": 29,
    "currency": "EUR",
    "is_active": true
  },
  "connection": {
    "status": "pending",
    "connected": false,
    "instance_name": "nv-1-1739999999999999999",
    "qr": "..."
  }
}
```

### `POST /api/admin/members/whatsapp/connect`

Creates/assigns a tenant instance (if missing) and starts connection handshake.

Request body:

```json
{
  "phone": "34600111222"
}
```

Notes:

- Without `phone`, UAZAPI returns QR-style handshake.
- With `phone`, UAZAPI may return pair-code style handshake.

Response (`200`):

```json
{
  "success": true,
  "message": "Conexion iniciada. Escanea el QR en WhatsApp para completar el enlace",
  "connected": false,
  "connection": {
    "status": "pending",
    "connected": false,
    "instance_name": "nv-1-1739999999999999999",
    "provider_instance_id": "...",
    "server_base_url": "https://your-uazapi-server",
    "qr": "...",
    "pair_code": null,
    "phone": null,
    "updated_at": "2026-02-20T..."
  }
}
```

Failure without active subscription:

```json
{
  "success": false,
  "code": "NEEDS_SUBSCRIPTION",
  "message": "Necesitas una suscripcion activa de WhatsApp Pack"
}
```

### `GET /api/admin/members/whatsapp/connection`

Returns latest connection state and tries to refresh from UAZAPI `/instance/status`.

Response (`200`):

```json
{
  "success": true,
  "connected": true,
  "message": "WhatsApp conectado y listo para enviar mensajes",
  "connection": {
    "status": "connected",
    "connected": true,
    "instance_name": "nv-1-1739999999999999999",
    "phone": "34600111222"
  }
}
```

### `POST /api/admin/members/whatsapp/disconnect`

Disconnects active WhatsApp instance. Optional hard delete.

Request body:

```json
{
  "delete_instance": false
}
```

If `delete_instance=true`, backend also removes the remote instance and local mapping.

Response (`200`):

```json
{
  "success": true,
  "message": "WhatsApp desconectado",
  "connected": false
}
```

### Sending behavior impact

`POST /api/admin/members/whatsapp/send` now returns:

- `NEEDS_SUBSCRIPTION` when feature is not active.
- `NEEDS_CONNECTION` when subscription exists but provider connection/token is missing.

## UAZAPI Server Pool Admin (`/api/admin/integrations/uazapi/servers`)

Requires backoffice session + `ajustes` section + high-admin role (`importance >= 90`).

Response style for this block:

- Success: `{ "success": true, "data": ... }` (and optional `message`)
- Error: `{ "success": false, "code": "...", "message": "..." }`

`adminToken` is accepted on create/update, stored in DB, and never returned raw. Responses expose `adminTokenMasked` only.

### `GET /api/admin/integrations/uazapi/servers`

Lists current server pool ordered by active/priority. Includes capacity and current usage.

Response (`200`):

```json
{
  "success": true,
  "data": {
    "servers": [
      {
        "id": 1,
        "name": "Primary Madrid",
        "baseUrl": "https://uazapi-1.example.com",
        "adminTokenMasked": "abc********xyz",
        "capacity": 300,
        "used": 184,
        "priority": 100,
        "isActive": true,
        "metadata": {
          "region": "eu-west"
        }
      }
    ]
  }
}
```

### `POST /api/admin/integrations/uazapi/servers`

Creates a server entry for multi-tenant provisioning.

Request body:

```json
{
  "name": "Primary Madrid",
  "baseUrl": "https://uazapi-1.example.com/",
  "adminToken": "super-secret-admin-token",
  "capacity": 300,
  "priority": 100,
  "isActive": true,
  "metadata": {
    "region": "eu-west"
  }
}
```

Validation:

- `baseUrl` must be `http` or `https`, and is normalized with trailing `/` removed.
- `capacity` must be `> 0` and `<= 10000`.

Response (`200`):

```json
{
  "success": true,
  "message": "Servidor UAZAPI creado",
  "data": {
    "server": {
      "id": 2,
      "name": "Primary Madrid",
      "baseUrl": "https://uazapi-1.example.com",
      "adminTokenMasked": "sup****************ken",
      "capacity": 300,
      "used": 0,
      "priority": 100,
      "isActive": true,
      "metadata": {
        "region": "eu-west"
      }
    }
  }
}
```

### `PATCH /api/admin/integrations/uazapi/servers/{id}`

Updates allowed fields:

- `name`
- `baseUrl`
- `adminToken`
- `capacity`
- `priority`
- `isActive`
- `metadata` (object or `null`)

Request body example:

```json
{
  "capacity": 500,
  "isActive": false
}
```

Response (`200`):

```json
{
  "success": true,
  "message": "Servidor UAZAPI actualizado",
  "data": {
    "server": {
      "id": 2,
      "name": "Primary Madrid",
      "baseUrl": "https://uazapi-1.example.com",
      "adminTokenMasked": "sup****************ken",
      "capacity": 500,
      "used": 184,
      "priority": 100,
      "isActive": false,
      "metadata": {
        "region": "eu-west"
      }
    }
  }
}
```

Common errors:

- `BAD_REQUEST`
- `NOT_FOUND`
- `DUPLICATE_BASE_URL`
- `UAZAPI_POOL_UNAVAILABLE`

---

## WhatsApp Bot (multi-tenant)

### POST /bot/webhook

Public webhook for UAZAPI inbound messages. Tenant is resolved by the
`token` field in the payload (matched against
`restaurant_uazapi_instances.instance_token`, fallback: `owner` phone against
`connected_phone`). Requires an active `whatsapp_pack` recurring feature;
otherwise responds `{"processed": false, "code": "NEEDS_SUBSCRIPTION"}`.

The message is processed asynchronously by the MiniMax-M3 agent loop
(same credentials as the translation system: `MINIMAX_API_KEY`,
`MINIMAX_BASE_URL`). The agent replies through the tenant's UAZAPI instance
(`/send/text`, `/send/menu`, `/send/media`, `/send/location`, `/send/contact`).

Responses:

- `200 {"processed": true}` — message accepted
- `200 {"processed": true, "duplicate": true}` — deduplicated by messageid
- `200 {"processed": false, "code": "NEEDS_SUBSCRIPTION"}` — no WhatsApp Pack
- `200 {"processed": false, "code": "DAILY_CAP"}` — daily turn cap reached
- `401 {"processed": false, "message": "unknown instance"}` — bad token

### GET /api/admin/bot/config

Session + `ajustes` section + role importance >= 90. Returns the bot
personalization config for the active restaurant.

```json
{
  "success": true,
  "config": {
    "language_default": "es",
    "tone": "cercano y profesional",
    "greeting_style": "",
    "disable_attachments": false,
    "custom_instructions": "",
    "contact_phone": ""
  }
}
```

### PUT /api/admin/bot/config

Same auth. Body is the `config` object above (`language_default` must be
`es` or `en`; anything else falls back to `es`). Stored in
`whatsapp_bot_config.config_json`.

Env knobs (defaults): `BOT_MINIMAX_MODEL` (MiniMax-M3),
`BOT_MINIMAX_TIMEOUT_SECONDS` (45), `BOT_MINIMAX_MAX_TOKENS` (1024),
`BOT_MAX_ITERATIONS` (8), `BOT_HISTORY_LIMIT` (20), `BOT_DAILY_TURNS_CAP` (2000).

### GET /api/admin/bot/settings/{restaurantId}

Root-only (role importance 100). Returns the bot config for the given
restaurant plus `promptPreview` (the fully rendered system prompt with live
restaurant data) and `defaultModel` (global fallback when `config.model` is
empty).

### PUT /api/admin/bot/settings/{restaurantId}

Root-only. Body is the `config` object (adds `model` to override the global
`BOT_MINIMAX_MODEL` per restaurant). Responds with the saved config and a
refreshed `promptPreview`.

### POST /api/admin/bot/settings/{restaurantId}/preview

Root-only. Renders the system prompt for a **draft** config without saving
it. Body: the `config` object. Response: same shape as GET (config,
promptPreview, defaultRules, restaurant). Used by the IA tab for the live
prompt preview while editing.


#### Bot tools note

The bot no longer inlines rice types or schedules in the system prompt. It
resolves them dynamically each turn via tools bound to Go internals:

- `get_rice_menu` — active rice types for the restaurant.
- `get_default_schedule` — default weekly schedule: `opening_mode`,
  `morning_hours`, `night_hours`, `daily_limit`, `weekday_open` (per-weekday
  open flags) and `open_days` (Spanish names of open weekdays).
- `get_day_schedule` — effective schedule for a concrete date: `weekday`,
  `weekday_open`, `has_override` (true when `openinghours` overrides the general
  config for that day), `opening_mode`, `morning_hours`, `night_hours` and
  `open`.


- `list_menus` — active bookable menus with their category (`closed_conventional`,
  `closed_group`, `a_la_carte`, `a_la_carte_group`, `special`), Spanish
  `category_label`, price and subtitle.
- `get_menu_details` — full info for one menu (`menu_id`): sections with their
  dishes (title, description, price, supplement) plus settings: beverage type +
  label, `unlimited_drinks`, `drink_price_per_person`, `min_party_size`,
  `has_max_main_dishes_per_table` / `max_main_dishes_per_table`,
  `coffee_included`, `comments`.
- `get_coffee_menu` / `get_drinks_menu` — items from `CAFES` / `BEBIDAS`
  (name, price, description, group, supplement).
- `get_wines_menu` — wines from `VINOS` grouped by type (name, price, type,
  winery, denomination, year, abv).

## Multi-Tenant WhatsApp Bot (Evolution API onboarding)

Each restaurant whose subscription includes the WhatsApp Pack
(`feature_key = whatsapp_pack`) can provision a dedicated Evolution API instance
and connect its phone by scanning a QR. New instances only use active
`uazapi_servers.provider = evolution` hosts. If no Evolution host has capacity,
provisioning falls back to the configured UAZAPI pool instead of failing the
restaurant onboarding. Existing UAZAPI instances remain supported.

Production deployment uses self-hosted Evolution API `2.3.7` with
`WHATSAPP-BAILEYS`, bound to `127.0.0.1:8098`. Compose config lives at
`/opt/newvillacarmen-evolution`; PostgreSQL and Redis stay container-internal.
UAZAPI pool allocation is disabled. Bot option menus use Evolution
`POST /message/sendButtons/{instance}` with reply buttons. Evolution `2.3.7`
Baileys `sendList` is avoided because its live route returns HTTP 400.

If a restaurant already has `restaurant_integrations.uazapi_url/uazapi_token`,
onboarding checks that instance first and adopts it into
`restaurant_uazapi_instances`. This supports legacy connected instances without
requiring an admin token or creating a duplicate remote instance.

### Inbound webhook (provider → backend)
`POST /bot/webhook` — public UAZAPI webhook. No auth header; the tenant is
resolved from the payload `token` (instance token) or `owner` (connected phone)
against `restaurant_uazapi_instances` (must be `is_active = 1`). Handles two
kinds of payloads:
- **Message events** → gated by `hasActiveRecurringFeature(rid, whatsapp_pack)`,
  deduped, daily-capped, then processed by the AI agent in the background.
  Non-entitled restaurants return `{ processed: false, code: "NEEDS_SUBSCRIPTION" }`.
- **Connection lifecycle events** (`qrcode` / `connection` / pairing) → update the
  provisioning row (status, connected phone, clear QR) so the onboarding UI stays
  live without polling. Response: `{ processed, connection: true }`.

The instance webhook is auto-registered at provisioning time via
`POST {server}/instance/updatewebhook` pointing at
`BOT_PUBLIC_WEBHOOK_URL + /bot/webhook` with events `["messages","connection"]`.
Set `BOT_PUBLIC_WEBHOOK_URL` to the public HTTPS origin of this backend.

Evolution uses `POST /bot/webhook/evolution/{secret}` with
`EVOLUTION_WEBHOOK_SECRET`. Tenant routing uses Evolution `instance` matched to
`restaurant_uazapi_instances.provider_instance_id`. `CONNECTION_UPDATE` and
`QRCODE_UPDATED` persist current state and broadcast it to the restaurant's
backoffice WebSocket room.

### Backoffice onboarding (session-cookie auth, `ajustes` + roles-admin gate)
- `POST /api/admin/members/whatsapp/connect` — provision (if needed) and start
  pairing. Optional body `{ phone }` requests a pairing code instead of a QR.
- `GET  /api/admin/members/whatsapp/connection` — current connection state.
- `GET  /api/admin/members/whatsapp/ws` — authenticated WebSocket. Sends an
  immediate snapshot, then `whatsapp.connection` events for QR/status changes.
- `POST /api/admin/members/whatsapp/disconnect` — disconnect; `{ delete_instance:true }`
  also deletes the remote instance and local row.

Disconnect marks the local instance inactive and responds immediately. Provider
logout runs in a bounded background request, so a slow provider cannot leave the
settings UI loading. Inactive instances cannot route inbound bot messages;
reconnect reactivates the same instance.

Some providers generate QR asynchronously after `/connect`. Backend watches
pairing status for up to 60 seconds, persists the first QR/pair code, and pushes
it through the restaurant WebSocket. Watcher stops immediately if instance is
inactive or suspended.

Subscription activation/cancellation endpoints remain root-only. Restaurant
admins cannot grant or cancel their own entitlement from settings.

Status response:

```json
{
  "success": true,
  "entitled": true,
  "connected": false,
  "connection": {
    "status": "pending",
    "connected": false,
    "phone": null,
    "qr": "data:image/png;base64,...",
    "pair_code": null,
    "updated_at": "2026-07-23T13:00:00Z"
  }
}
```

Connection response shape (`connection`):
`{ status, connected, phone, qr, pair_code, updated_at }`. Provider URL, instance
name, provider ID, API key and instance token are never returned to browser.

### Superadmin server pool (`ajustes` + roles-admin gate)
- `GET/POST /api/admin/integrations/uazapi/servers`, `PATCH /.../servers/{id}` —
  manage provider pool (`provider`, `base_url`, `admin_token`, capacity,
  priority). New onboarding prefers the least-loaded active Evolution server,
  then UAZAPI. No capacity in either provider returns `WHATSAPP_POOL_FULL`.

Evolution server create body includes `"provider":"evolution"`. `adminToken`
is Evolution global API key used in `apikey` header.

### Data model
- `uazapi_servers` — provider host pool (migrations 019 + 059).
- `restaurant_uazapi_instances` — one row per restaurant (unique), holds instance
  token, status, connected phone, QR/pair code, webhook metadata (migration 019).
- `whatsapp_bot_config` / `whatsapp_bot_sessions` / `whatsapp_bot_messages` —
  per-tenant personalization + conversation history (migration 056).

### Bot behavior parity
The Go bot tool set is a superset of the legacy .NET bot: booking CRUD
(`create_booking`, `cancel_booking`, `modify_booking`, `get_bookings`),
availability (`check_day_capacity`, `check_availability_for_party`), schedule
(`get_default_schedule`, `get_day_schedule`), menus (`list_menus`,
`get_menu_details`, `get_rice_menu`, `get_coffee_menu`, `get_drinks_menu`,
`get_wines_menu`), messaging/media (`send_message`, `send_menu_buttons`,
`send_contact`, `send_location`, `send_image`, `send_document`) and
`get_restaurant_info`. The legacy `fetch_whatsapp_history` tool is unnecessary
because history is persisted per tenant in `whatsapp_bot_messages`. All tools are
scoped by `restaurant_id`; the system prompt is personalized from
`whatsapp_bot_config` (language, tone, greeting, rules, custom instructions).
# Stock control (backoffice)

All routes require `bo_session`, active restaurant context, `stock` section access,
and endpoint-specific stock permission. Responses use `{ success: true, ... }` or
`{ success: false, message }`. Every query is scoped by active `restaurant_id`.

| Method | Route | Permission | Contract |
|---|---|---|---|
| GET | `/api/admin/stock/warehouses` | `stock.view` | `{ warehouses: StockWarehouse[] }`; creates default warehouse lazily when none exists |
| POST | `/api/admin/stock/warehouses` | `stock.warehouses.manage` | Body `{ name, code?, type, isDefault?, isActive?, sortOrder?, notes? }`; returns `{ id }` |
| PATCH | `/api/admin/stock/warehouses/{id}` | `stock.warehouses.manage` | Same body as create; full warehouse update |
| DELETE | `/api/admin/stock/warehouses/{id}` | `stock.warehouses.manage` | Soft delete; rejects default or non-empty warehouse with `409` |
| GET/POST | `/api/admin/stock/categories` | `stock.view` / `stock.items.manage` | List or create tenant categories |
| PATCH/DELETE | `/api/admin/stock/categories/{id}` | `stock.items.manage` | Update or delete unused category |
| GET | `/api/admin/stock/items` | `stock.view` | Query `q` (matches name, SKU or barcode), `warehouseId`, `page`, `pageSize<=100`; returns paginated card payload incl. `barcode` |
| GET | `/api/admin/stock/item-options` | `stock.view` | Active item/default-unit options for recipes and OCR mapping; `q` matches name, SKU or barcode |
| POST | `/api/admin/stock/items` | `stock.items.manage` | Creates item (optional `barcode`, max 64 chars) plus default display/purchase unit |
| POST | `/api/admin/stock/items/import` | `stock.items.manage` | Multipart CSV/XLSX preview; `confirm=1` atomically creates valid rows |
| PATCH | `/api/admin/stock/items/{id}` | `stock.items.manage` | Updates item metadata incl. `barcode`, tracking flag and deduction source |
| DELETE | `/api/admin/stock/items/{id}` | `stock.items.manage` | Soft delete; rejects item with non-zero stock |
| PATCH | `/api/admin/stock/items/{id}/targets` | `stock.items.manage` | Saves warehouse par/reorder targets in selected item unit |
| GET/POST | `/api/admin/stock/items/{id}/units` | `stock.view` / `stock.items.manage` | List or create item-specific conversion units |
| DELETE | `/api/admin/stock/items/{id}/units/{unitId}` | `stock.items.manage` | Delete unused, non-default unit |
| GET | `/api/admin/stock/items/{id}/movements` | `stock.view` | Query `page`, `pageSize<=200`, optional filters `type` (PURCHASE, PRODUCTION_IN, TRANSFER_IN, RETURN, ADJUSTMENT, PRODUCTION_OUT, SALE, WASTE, TRANSFER_OUT, INVENTORY_COUNT), `from`/`to` (`YYYY-MM-DD`, inclusive); audited movement history |
| POST | `/api/admin/stock/items/{id}/movements` | `stock.adjust` or `stock.waste.record` | Atomic ledger + level update; adjustment accepts `direction=ADD|SUBTRACT`; inbound rows (PURCHASE, ADJUSTMENT+ADD) accept optional `expiresAt` (`YYYY-MM-DD` or RFC3339) |
| GET | `/api/admin/stock/expiring` | `stock.view` | Optional `days` (default 30, 1–365); estimate of soon-to-expire stock per item+warehouse: `{ days, items: [{ itemId, itemName, warehouseId, warehouseName, expiresAt, estimatedQtyBase }] }` (unexpired inbound with `expires_at` minus outbound since earliest inbound, clamped at 0) |
| GET | `/api/admin/stock/summary` | `stock.view` | `{ itemsTracked, belowPar, belowReorder, outOfStock, negative, coveragePct }`; `details=1` adds `belowParItems`/`belowReorderItems`/`outOfStockItems`/`negativeItems` (`{id,name,qty,par,reorderPoint}`) and `unresolvedAnomalies` (open `pos_stock_anomalies`) |
| GET | `/api/admin/stock/valuation` | `stock.view` | `{ totalValue, items: [{ itemId, itemName, categoryName, qtyBase, avgUnitCost, lastUnitCost, unitCost, value }] }`; values qty at latest PURCHASE `unit_cost`, falling back to `stock_levels.avg_unit_cost`; only items with qty ≠ 0 |
| GET | `/api/admin/stock/export` | `stock.view` | CSV download via `type=items\|movements\|waste`. `items`: id/sku/name/category/base_unit/qty/par/reorder/avg+last unit cost/value. `movements`: last 90 days (cap 5000), filters `movementType`, `from`/`to` (`YYYY-MM-DD`). `waste`: WASTE movements only, same filters |
| GET | `/api/admin/stock/suppliers` | `stock.view` | Supplier registry (backfilled from `supplier_name` on prices/scans): `{ suppliers: [{ id, name, notes, isActive, aliasCount, itemCount, pricePointCount, lastPriceAt }] }` |
| POST | `/api/admin/stock/suppliers` | `stock.items.manage` | Create supplier `{ name, notes }`; 409 on duplicate name (unique per restaurant) |
| PATCH | `/api/admin/stock/suppliers/{id}` | `stock.items.manage` | Update `{ name, notes, isActive }`; 409 on name clash, 404 if missing. Aliases/price history are keyed by `supplier_name`, so renaming orphans old history rows |
| DELETE | `/api/admin/stock/suppliers/{id}` | `stock.items.manage` | Removes the registry row only — aliases and price history (keyed by name) are kept |
| GET | `/api/admin/stock/suppliers/{id}/aliases` | `stock.view` | OCR alias rows for this supplier: `{ aliases: [{ id, supplierCode, description, stockItemId, itemName, stockUnitId, unitLabel, unitFactor, updatedAt }] }` |
| PUT | `/api/admin/stock/suppliers/{id}/aliases` | `stock.items.manage` | Full-replace save `{ aliases: [{ supplierCode, description, stockItemId, stockUnitId }] }` (≤500): upserts by (code, description), deletes rows missing from payload, one transaction. Unit must belong to item, else falls back to the item's default purchase unit |
| GET | `/api/admin/stock/suppliers/{id}/prices` | `stock.view` | Per-item price stats over `days` (default 180, 7–730) from `stock_item_prices`: `{ items: [{ itemId, itemName, baseUnit, samples, minCost, maxCost, avgCost, lastCost, lastAt, others: [{ supplierName, avgCost, samples }] }] }` for cross-supplier comparison |
| POST | `/api/admin/stock/transfers` | `stock.transfer` | Atomic two-ledger-entry warehouse transfer |
| POST | `/api/admin/stock/counts` | `stock.count.perform` | Opens count sheet and snapshots expected stock |
| GET | `/api/admin/stock/counts/{id}` | `stock.view` | Count sheet plus item lines |
| POST | `/api/admin/stock/counts/{id}/close` | `stock.count.close` | Applies observed quantities as idempotent inventory-count deltas; positive-delta lines accept optional `expiresAt` |
| GET | `/api/admin/stock/reconciliation` | `stock.view` | Compares materialized levels with ledger sums |
| POST | `/api/admin/stock/reconciliation/rebuild` | `stock.settings.manage` | Rebuilds materialized quantities from ledger |
| GET | `/api/admin/stock/settings` | `stock.view` | Tenant stock settings with defaults |
| PATCH | `/api/admin/stock/settings` | `stock.settings.manage` | Saves display/cadence, negative policy, business/seasonality profile and onboarding |
| POST | `/api/admin/stock/settings/classify-seasonality` | `stock.settings.manage` + AI plan | MiniMax structured business-profile classification |
| GET | `/api/admin/stock/permissions/mine` | session only | Caller's own effective stock permissions `{ role, permissions: [{key, allowed}] }`; lets restricted roles render the stock UI without `stock.settings.manage` |

Movement `type`: `PURCHASE`, `ADJUSTMENT`, `PRODUCTION_IN`, `PRODUCTION_OUT`,
`SALE`, `WASTE`, `TRANSFER_IN`, `TRANSFER_OUT`, `RETURN`. Input quantity is always
positive; direction derives from type. `WASTE` requires `wasteReason`.

## Stock recipes, analytics, costing and extraction

| Method | Route | Contract |
|---|---|---|
| GET/POST | `/api/admin/stock/recipes` | List or create technical recipes |
| GET/PATCH/DELETE | `/api/admin/stock/recipes/{id}` | Recipe detail/update/soft-delete with nested BOM cycle checks and `{ labour:[{memberId,minutesPerBatch,notes?}] }` |
| PATCH | `/api/admin/stock/recipes/{id}/pricing` | Gross price, VAT, overhead and strategic/signature protection |
| POST | `/api/admin/stock/recipes/{id}/production/preview` | Recursive raw-material requirement/shortage preview |
| POST | `/api/admin/stock/recipes/{id}/production` | Atomic manufactured stock-in + exploded raw-component stock-out |
| GET | `/api/admin/stock/production-orders` | Latest confirmed production orders with standard/actual labour summary |
| GET | `/api/admin/stock/production-labour/entries` | Closed fichaje entries with remaining allocatable minutes; no salary values |
| GET/POST | `/api/admin/stock/production-orders/{id}/labour` | List or allocate actual fichaje minutes to production; missing compensation stays incomplete |
| DELETE | `/api/admin/stock/production-orders/{id}/labour/{allocationId}` | Remove allocation and deterministically rebuild actual labour snapshot |
| PUT | `/api/admin/stock/affluence` | Manual covers input until POS module exists |
| GET | `/api/admin/stock/forecast` | Scenario/horizon forecast with eight-week confidence state. `?scenario=LIGHT\|MEDIUM\|HIGH` (default MEDIUM), `?horizonDays=1..30` (default 7). When a `stock_settings.seasonality_profile` exists, its multipliers are day-weighted over the window and composed on top of the scenario (`effectiveMultiplier = scenario × seasonal`); response carries `seasonalFactor` (window average, 1.0 = neutral) and `seasonalityApplied`. Malformed profiles fall back to neutral, never error. |
| GET/POST/PATCH/DELETE | `/api/admin/stock/vat-rates[/{id}]` | Tenant VAT CRUD |
| POST | `/api/admin/stock/items/{id}/prices` | Record raw-item base-unit purchase price |
| GET | `/api/admin/stock/costing` | Recursive ingredient + member labour cost, overhead, net price, food-cost %, margin and missing-rate diagnostics. `?salesDays=7..365` (default 90) adds menu-engineering sales mix per recipe: `sold` (ACTIVE lines on PAID/PARTIALLY_REFUNDED tickets, joined product→recipe via `pos_product_stock_rules`), `tickets`, `marginPct` and `class` (`star`/`plowhorse`/`puzzle`/`dog`; empty until sales exist). Top-level `salesMix` reports `{days, totalSold, recipesWithSales, avgSold, weightedAvgMargin, classified}`. |
| GET | `/api/admin/stock/labour-members` | Active members with cost availability only; salary and hourly amount are not exposed |
| GET/POST/PATCH/DELETE | `/api/admin/stock/margin-bands[/{id}]` | Tenant margin-band CRUD |
| POST | `/api/admin/stock/ai/recommendations` | Persisted MiniMax advisory report; protected dishes cannot receive removal advice |
| POST | `/api/admin/stock/documents/extract-text` | AI-gated pasted-text extraction; always review-required |
| POST | `/api/admin/stock/documents/upload` | MiniMax M3 native PDF/JPG/PNG/WebP extraction, 10 MB max |
| GET | `/api/admin/stock/documents[/{id}]` | Tenant scan queue/detail, mapped lines and `originalAvailable` |
| GET | `/api/admin/stock/documents/{id}/original` | Authenticated private original download; `Cache-Control: private, no-store` |
| DELETE | `/api/admin/stock/documents/{id}/original` | Delete private original and retain reviewed extraction/audit |
| PATCH | `/api/admin/stock/documents/{id}/review` | Edit metadata/lines and map tenant item units |
| POST | `/api/admin/stock/documents/{id}/confirm-invoice` | Atomic purchases, weighted cost and supplier-alias learning; optional `lineExpiries` map (`{lineId: YYYY-MM-DD}`) stores per-line expiry on the PURCHASE rows |
| POST | `/api/admin/stock/documents/{id}/confirm-recipe` | Create reviewed OCR recipe |
| POST | `/api/admin/stock/documents/{id}/reject` | Reject review draft |

Original supplier files are never persisted in public Bunny storage. When
`BUNNY_PRIVATE_STORAGE_ZONE` and `BUNNY_PRIVATE_STORAGE_ACCESS_KEY` are configured,
private originals are retained under opaque tenant paths with access audit and
`STOCK_DOCUMENT_RETENTION_DAYS`; otherwise extraction continues without retention.

## Technical sheets (fichas técnicas) (`/api/admin/comida/technical-sheets/*`)

Distinct from `/api/admin/stock/recipes` (analytics / costing / production
orders). These routes drive the ficha-técnica editor and the backoffice's
"Fichas técnicas" tab on `/app/stock?tab=sheets`. Tenant-scoped via the
active restaurant; reads need `stock.view` (or any higher stock role),
writes need `stock.recipe.manage` (or higher).

| Method | Route | Notes |
|---|---|---|
| GET | `/api/admin/comida/technical-sheets` | List: `q` (substring on `name`), `status` (`DRAFT` / `PUBLISHED` → maps to `ACTIVE`), `categoryId`, `page` (1-based, server cap 1,000,000), `pageSize` (default 100, cap 100). Returns `{ success, sheets:[...full hydration], page, pageSize, total, totalPages, preferences:{ stockSheetsShowImages } }`. The `preferences` map only carries keys this UI consumes — other stored user preferences are not exposed. |
| POST | `/api/admin/comida/technical-sheets` | Body `{name, portions, outputUnit?}` where `outputUnit` overrides COUNT/ud defaults: `{baseDimension, displayUnitCode, displayUnitLabel, displayUnitFactor}`. |
| GET | `/api/admin/comida/technical-sheets/{id}` | Detail. |
| DELETE | `/api/admin/comida/technical-sheets/{id}` | Aborts with `409 { code: "SHEET_IN_USE", products, usedBySheets }` when any product (comida/vinos/postres) or parent sheet still depends on it. The output item stays if it has ledger history. |
| GET | `/api/admin/comida/technical-sheets/{id}/usage` | `{ success, products:[{id,name,source}], usedBySheets:[name], inUse }` — what would break if this sheet changed. |
| POST | `/api/admin/comida/technical-sheets/{id}/duplicate` | Body `{name?}`; returns `{sheetId, outputItemId}`. |
| POST | `/api/admin/comida/technical-sheets/ensure` | Idempotent: returns or creates the sheet for a product. Body `{itemId, name, source:comida|vinos|postres}`. |
| GET / POST / PATCH / DELETE | `/api/admin/comida/technical-sheets/{id}/components[/{componentId}]` | Component CRUD; each line is a stock item or a sub-recipe (`subRecipeId`). |
| GET / POST / PATCH / DELETE | `/api/admin/comida/technical-sheets/{id}/steps[/{stepId}]` | Step CRUD + `PUT /steps/order` (body `{stepIds}`). |
| POST | `/api/admin/comida/technical-sheets/{id}/steps/{stepId}/image` | Multipart upload; client compresses to WebP and the server re-normalises. |
| POST | `/api/admin/comida/technical-sheets/{id}/steps/{stepId}/image-jobs` | Queues AI work; result arrives over the socket (see below), not from this call. Body `{mode:"AI_ENHANCE"|"AI_GENERATE", prompt?, idempotencyKey?}`. |
| GET | `/api/admin/comida/technical-sheets/{id}/cost` | Recursive ingredient + member-labour cost, overhead, net price, food-cost %, margin and missing-rate diagnostics. |
| GET / PATCH | `/api/admin/comida/technical-sheets/{id}/allergens` | Effective allergen list + manual `{added?, disabled?}` overrides. |
| PATCH | `/api/admin/comida/items/{itemId}/production-type` | Toggle between `RAW` (restores the backfilled SKU item) and `MANUFACTURED` (links to a sheet). Body `{productionType, stockRecipeId?, source:comida|vinos|postres}`. Reverting an untouched draft discards it; sheets with real content are kept. |

### WS: `/api/admin/comida/technical-sheets/ws`

Live notification channel for image-job progress and a paginated search.
Auth + origin check follow the other backoffice sockets. One socket per
tenant; the same endpoint is used by the editor and by the sheets grid,
which mutually gate `enabled` so only one is mounted at a time.

**Frames**

| Direction | Frame | Notes |
|---|---|---|
| client → server | `{"type":"search","query":"...","status":"DRAFT\|PUBLISHED","categoryId":0,"page":1,"pageSize":25}` | `pageSize` cap 100, `page` cap 1,000,000 (server-side); missing `pageSize` falls back to the historical LIMIT 25. |
| server → client | `{"type":"searchResults","query":<trimmed>,"sheets":[...summary],"page":<clamped>,"pageSize":<clamped>,"total":N,"totalPages":ceil(total/pageSize)}` | The echoed `query` is whitespace-trimmed so the client can render it back into the search box without re-sending padding. |
| server → client | `{"type":"imageJob", ...}` | Same shape as the editor's existing socket (step + card image jobs); same hub also broadcasts to clients open in the sheets grid. |
| server → client | `{"type":"searchError","message":"..."}` | One per failed search. |

REST remains the source of truth for hydration; messages only tell the
caller to re-read the current page over HTTP.

## POS / TPV (`/api/admin/pos/*`)

All routes require `bo_session`, active `pos_pack`, tenant scope and exact POS permission. Money fields are integer cents. Paid tickets are immutable; corrections use refunds and append-only stock returns.

### Settings, periods and bootstrap

| Method | Route | Permission | Purpose |
|---|---|---|---|
| GET | `/api/admin/pos/bootstrap` | `pos.view` | Settings, active products, visits, table occupancy and the cash day; with stock mode `SHADOW`/`LIVE` also `productStock` (`{productId: ok\|low\|out}`) |
| GET/PATCH | `/api/admin/pos/settings` | `pos.view` / `pos.settings.manage` | Enable POS; stock/covers modes; timezone and cutoff |
| GET/POST/PATCH/DELETE | `/api/admin/pos/service-periods[/{id}]` | `pos.view` / `pos.settings.manage` | `LUNCH`, `DINNER`, `OTHER` periods including cross-midnight ranges |

`/pos/bootstrap`, `/pos/visits` and `/pos/tickets` accept an optional
`date=YYYY-MM-DD` to browse a past business date; a malformed value is a `400`.
Omitting it preserves the live behaviour exactly, so `bootstrap` keeps returning
only `OPEN` visits and the unfiltered lists stay unfiltered. With a date,
`bootstrap` returns that day's `OPEN` and `CLOSED` visits, computes table
occupancy from that day alone instead of live service, and echoes `date` alongside
the `cashDay`. `CANCELLED` and `MERGED` visits are excluded, since a merged source
visit is already counted on the visit it was merged into. `/pos/visits` stays
unopinionated and returns every status, filtered by `status` when given.

### Catalogue and stock mappings

| Method | Route | Permission |
|---|---|---|
| GET/POST | `/api/admin/pos/products` | `pos.view` / `pos.catalog.manage` |
| GET/PATCH/DELETE | `/api/admin/pos/products/{id}` | `pos.view` / `pos.catalog.manage` |
| POST | `/api/admin/pos/products/import-preview` | `pos.catalog.manage` |
| POST | `/api/admin/pos/products/import-confirm` | `pos.catalog.manage` |
| GET/PUT | `/api/admin/pos/products/{id}/stock-rules` | `pos.view` / `pos.stock_mapping.manage` |
| GET/POST/PATCH/DELETE | `/api/admin/pos/categories[/{id}]` | `pos.view` / `pos.catalog.manage` |
| GET | `/api/admin/pos/stock-readiness` | `pos.stock_mapping.manage` |
| GET | `/api/admin/pos/stock-exceptions` | `pos.stock_mapping.manage` |
| POST | `/api/admin/pos/stock-exceptions/replay` | `pos.stock_mapping.manage` |

Stock rules map one sellable product to one or more `(stockItemId, warehouseId, quantityBasePerSale)` records. `PRODUCTION`-only items are rejected. Recipes may be referenced only when their output item equals mapped stock item. Checkout never recursively explodes BOM components.

### Visits, tickets and payment

| Method | Route | Permission |
|---|---|---|
| GET/POST | `/api/admin/pos/visits` | `pos.view` / `pos.sell` |
| GET | `/api/admin/pos/reservations/eligible` | `pos.view`; query `date`, optional `q`; reservation/open-visit selector |
| GET | `/api/admin/pos/reservations/{bookingId}/visit` | `pos.view`; recover existing open visit linked to reservation |
| GET/PATCH | `/api/admin/pos/visits/{id}` | `pos.view` / `pos.visit.manage` |
| POST | `/api/admin/pos/visits/{id}/cancel` | `pos.visit.manage` |
| POST | `/api/admin/pos/visits/{id}/tickets` | `pos.sell` |
| POST | `/api/admin/pos/visits/{id}/close` | `pos.checkout` |
| GET | `/api/admin/pos/tickets[/{id}]` | `pos.view` |
| POST | `/api/admin/pos/tickets/{id}/lines` | `pos.sell` |
| PATCH | `/api/admin/pos/tickets/{id}/lines/{lineId}` | `pos.sell` |
| POST | `/api/admin/pos/tickets/{id}/lines/{lineId}/void` | `pos.line.void` |
| POST | `/api/admin/pos/tickets/{id}/lines/{lineId}/move` | `pos.sell`; move full/partial line between open tickets in same visit |
| POST | `/api/admin/pos/tickets/{id}/void` | `pos.sell`; empty open tickets only |
| POST | `/api/admin/pos/tickets/{id}/discount` | `pos.discount` |
| POST | `/api/admin/pos/tickets/{id}/checkout` | `pos.checkout` |
| POST | `/api/admin/pos/tickets/{id}/refunds` | `pos.refund`; `pos.restock` when any line requests restock |

### Control-rail features

| Method | Route | Permission |
|---|---|---|
| POST | `/api/admin/pos/visits/{id}/park` | `pos.visit.manage`; body `{ parked, note? }`; Aparcar (hold an open comanda) |
| POST | `/api/admin/pos/visits/{id}/merge` | `pos.visit.manage`; body `{ sourceVisitIds[], idempotencyKey }`; Juntar mesas |
| PATCH | `/api/admin/pos/visits/{id}/customer` | `pos.sell`; body `{ customerName?, customerTaxId?, customerAddress? }`; Cliente |
| POST | `/api/admin/pos/tickets/{id}/adjustments` | `pos.discount`; body `{ type: DISCOUNT\|SURCHARGE, mode: AMOUNT\|PERCENT, amountCents\|percent, reason, idempotencyKey }`; Descuento/Recargo |
| POST | `/api/admin/pos/tickets/{id}/lines/{lineId}/comp` | `pos.discount`; body `{ comped, reason }`; Invita |
| PATCH | `/api/admin/pos/tickets/{id}/operator` | `pos.sell`; body `{ operatorMemberId }`; Empleado |
| POST | `/api/admin/pos/drawer/open` | `pos.checkout`; body `{ reason?, note?, idempotencyKey }`; Cajón, requires an open shift |
| GET/POST | `/api/admin/pos/tags` | `pos.view` / `pos.catalog.manage`; tenant tag catalogue |
| POST | `/api/admin/pos/tickets/{id}/tags` | `pos.sell`; body `{ tagId, attach? }` |
| POST | `/api/admin/pos/tickets/{id}/lines/{lineId}/tags` | `pos.sell`; body `{ tagId, attach? }` |

Adjustments are append-only and signed: `DISCOUNT` stores a negative `amountCents`,
`SURCHARGE` a positive one, so discounts and surcharges coexist on one ticket and
`SUM(amount_cents)` is the net movement. `PERCENT` always resolves against the line
base, never against an already adjusted total. Ticket money keeps
`discount_cents` and `surcharge_cents` authoritative, and VAT is recomputed on the
surcharged gross.

Comping a line (`Invita`) discounts the line to zero but keeps it `ACTIVE`, so the
kitchen still fires it and stock is still deducted while revenue drops to zero.

Merging marks each source visit `MERGED` with `merged_into_visit_id`, moves its
active lines onto the target ticket, sums covers and frees the source table.
Sources holding any non-`OPEN`/`VOIDED` ticket are rejected with `409`.

Tips are collected on top of the sale: `payments[].tipCents` is stored on
`pos_payments.tip_cents` and totalled into `pos_tickets.tip_cents`. Tips are
excluded from the payment-vs-total match, subtotal, discount, VAT and
`total_gross_cents`, so net sales and tax are unaffected.

`BAR` joins `DINE_IN`/`TAKEAWAY`/`DELIVERY` as a tableless, coverless fast-sale
channel; covers reporting continues to count `DINE_IN` only.

Checkout body:

```json
{
  "idempotencyKey": "uuid",
  "expectedVersion": 4,
  "payments": [
    { "method": "CASH", "amountCents": 2000, "idempotencyKey": "uuid" },
    { "method": "CARD", "amountCents": 1250, "tipCents": 200, "provider": "STANDALONE", "providerReference": "terminal-receipt-ref", "idempotencyKey": "uuid" }
  ],
  "closeVisit": true
}
```

Manual `CARD` rows require an external terminal reference; PAN/CVV are never accepted. Checkout recalculates totals server-side. `LIVE` stock creates idempotent negative `SALE` movements referenced to immutable `pos_ticket_line_stock` snapshots. Missing mapping yields ticket `stockStatus=PARTIAL` and an exception without repeating or rolling back a successfully recorded payment. Refund does not restore stock unless an authorized refund line sets `restockRequested=true`; restock writes positive `RETURN` movements against original item/warehouse snapshots.

### Shifts, covers and reports

| Method | Route | Permission |
|---|---|---|
| GET | `/api/admin/pos/shifts/current` | `pos.view` |
| POST | `/api/admin/pos/shifts/open` | `pos.shift.manage` |
| POST | `/api/admin/pos/shifts/{id}/close` | `pos.shift.manage` |
| GET | `/api/admin/pos/cash-days/current` | `pos.view`; optional `date=YYYY-MM-DD`, defaults to the cutoff business date |
| POST | `/api/admin/pos/cash-days` | `pos.shift.manage`; `force` skips the unclosed-previous-days guard |
| POST | `/api/admin/pos/cash-days/{id}/close` | `pos.shift.manage`; Z closure, requires `countedCashCents` |
| GET | `/api/admin/pos/cash-days` | `pos.view`; required `from`/`to` `YYYY-MM-DD`, 92 days max; one row per day with a cash day or activity |
| GET | `/api/admin/pos/cash-days/{date}/tables` | `pos.view`; that day's takings broken down by table, visit and ticket |
| GET | `/api/admin/pos/covers` | `pos.reports.view` |
| POST | `/api/admin/pos/covers/adjustments` | `pos.covers.adjust` |
| GET | `/api/admin/pos/covers/reconciliation` | `pos.reports.view` |
| POST | `/api/admin/pos/covers/reconciliation/rebuild` | `pos.settings.manage` |
| GET | `/api/admin/pos/reports/sales` | `pos.reports.view` |
| GET | `/api/admin/pos/reports/stock` | `pos.reports.view` |
| GET | `/api/admin/pos/reports/card-reconciliation` | `pos.reports.view`; daily standalone-terminal amount/reference completeness |
| GET | `/api/admin/pos/reports/sales.csv` | `pos.reports.view` |
| GET | `/api/admin/pos/accounting/export.csv` | `pos.reports.view`; `type=SALES_VAT|PAYMENTS|REFUNDS|STOCK`, optional `from/to`; immutable SHA-256 audited export |
| GET | `/api/admin/pos/health` | `pos.settings.manage` |
| GET | `/api/admin/pos/stock-anomalies` | `pos.stock_mapping.manage` | Lists anomalies joined to ticket/item/warehouse names; `unresolved=1` filters `status='OPEN'`, `limit` (1-500, default 200) |
| POST | `/api/admin/pos/stock-anomalies/{id}/resolve` | `pos.stock_mapping.manage` |
| GET/PUT | `/api/admin/pos/roles/{slug}/permissions` | `pos.settings.manage` | Fine-grained tenant role permissions |

A cash day (`pos_cash_days`) is the restaurant-wide till session for one business date, unique per `(restaurant_id, business_date)`; a shift is per terminal and now belongs to a cash day. `GET /pos/cash-days/current` returns the day for `date` (or the cutoff-derived business date) plus `unclosedPrevious`, the earlier days still `OPEN` with their takings and covers. Opening returns `409 UNCLOSED_PREVIOUS_DAYS` with that list unless `force=true` is sent, which records `forcedOpen`, and `409 CASH_DAY_ALREADY_OPEN` when another terminal wins the unique-key race. Closing is a Z closure that reuses the shift-closure accounting over a day-wide scope and writes the same `pos_cash_closures` snapshot: it rejects `409 OPEN_POS_ITEMS` while any visit or ticket is open, `400 COUNTED_CASH_REQUIRED` without a count, and `400 DISCREPANCY_REASON_REQUIRED` when counted cash differs from expected; on success it also closes any shift still open under that day.

`GET /pos/cash-days?from&to` powers the calendar. The range is inclusive and capped
at 92 days; a missing, malformed or reversed range is a `400`. Days with neither a
cash day nor any activity are omitted, and a day with activity but no cash day is
returned with a `null` status so the caller can flag it as never opened. Each row
carries `totalGrossCents` net of refunds, `ticketCount`, `covers` including manual
adjustments, and the resolved `openedByName`/`closedByName`. Voided tickets are
excluded from both the money and the count.

`GET /pos/cash-days/{date}/tables` returns that day's sales grouped table → visit →
ticket, with the same net-of-refunds money at every level. Voided tickets are still
listed so the operator can see they happened, but contribute nothing. Manual cover
corrections cannot be attributed to a table, so they are returned apart as
`adjustedCovers`; the tables' covers plus that delta reconcile with the day's
covers in the range endpoint. The response sets `readOnly`, which is `false` only
when that date has an `OPEN` cash day: a `CLOSED` day and a date that was never
opened are both history.

Mientras un día de caja está `CLOSED`, los 23 puntos de mutación del TPV
responden `409 { code: "CASH_DAY_CLOSED" }`: crear visita, crear/editar/anular
línea, descuento, ajuste de ticket, invitar línea, mover línea, anular ticket,
crear ticket, editar/cancelar/aparcar/fusionar visita, cliente de la visita,
operario del ticket, etiquetas de ticket y de línea, cobro, devolución, cierre
de visita, ajuste de comensales y comanda de cocina. Una fecha **sin** día de
caja no se considera cerrada, para no bloquear a los restaurantes que todavía no
usan la caja diaria. El propio cierre de día no está sujeto al guard.

### Kitchen display and LIVE activation

| Method | Route | Permission |
|---|---|---|
| GET/POST | `/api/admin/pos/kitchen/stations` | `pos.view` / `pos.kitchen.manage` |
| PATCH | `/api/admin/pos/kitchen/stations/{id}` | `pos.kitchen.manage` |
| GET/POST | `/api/admin/pos/kitchen/routes` | `pos.view` / `pos.kitchen.manage` |
| DELETE | `/api/admin/pos/kitchen/routes/{id}` | `pos.kitchen.manage` |
| POST | `/api/admin/pos/tickets/{id}/kitchen-dispatches` | `pos.sell`; body `idempotencyKey` |
| GET | `/api/admin/pos/kitchen/queue` | `pos.view`; optional `stationId` |
| POST | `/api/admin/pos/kitchen/dispatches/{id}/status` | `pos.kitchen.manage`; controlled state transition |
| GET | `/api/admin/pos/activation-readiness` | `pos.settings.manage` |
| POST | `/api/admin/pos/activation-acceptances` | `pos.settings.manage`; body `type=STOCK_LIVE|COVERS_LIVE`, `evidenceNote` |

Kitchen dispatch stores immutable per-station `ADD`/`VOID` deltas. Payment never dispatches kitchen work. Switching stock or covers to `LIVE` consumes one fresh tenant-scoped acceptance in same transaction.

Covers are counted from closed `DINE_IN` visits, not tickets. Split bills therefore count visit covers once. `TAKEAWAY`, `DELIVERY`, open and cancelled visits contribute zero. `LIVE` mode writes `stock_affluence_daily.source='POS'`; manual stock-affluence writes to a POS-owned key return `409 POS_COVERS_AUTHORITATIVE`.

### GET /api/admin/assistant/ws

Forky AI assistant chat over WebSocket. Auth: backoffice session cookie
(`bo_session`); unauthenticated handshakes get HTTP 401. Origin check follows
the other backoffice sockets (`allowBOWebSocketOrigin`). Any logged-in user.

Frames are JSON text messages:

| Direction | Frame | Notes |
|---|---|---|
| client → server | `{"type":"hello","session_id":null|int}` | null creates a new session (row owned by user+restaurant); an id reuses the caller's session |
| server → client | `{"type":"hello","session_id":int,"history":[{role,content}…]}` | last `ASSISTANT_HISTORY_LIMIT` messages, oldest-first |
| client → server | `{"type":"message","content":"…"}` | one user turn; rejected with a `busy` error while a generation is in flight |
| server → client | `{"type":"status","state":"thinking"}` → `{"type":"delta","text":"…"}*` → `{"type":"done"}` | deltas are streamed MiniMax text (≤120 runes per frame) |
| client → server | `{"type":"ping"}` | server replies `{"type":"pong"}` |
| server → client | `{"type":"error","message":"…"}` | any failure; no `done` follows |

Behavior:
- The user message is persisted before the LLM call; the assistant reply is
  persisted after the stream completes; `done` is only sent after the commit.
- Context for the LLM: the system prompt (Forky persona, Spanish, restaurant
  name/phone from `restaurants`) + the last `ASSISTANT_HISTORY_LIMIT` persisted
  messages + the new user message.
- Model: `ASSISTANT_MINIMAX_MODEL` (default `MiniMax-M3`) via the same
  Anthropic-compatible Messages API as the translation system
  (`MINIMAX_BASE_URL`/`MINIMAX_API_KEY`), `stream: true` (SSE).
- Persistence: `assistant_sessions` / `assistant_messages` (migration 082).
- One generation per connection; client disconnect cancels the LLM call.

Env knobs (defaults): `ASSISTANT_MINIMAX_MODEL` (`MiniMax-M3`),
`ASSISTANT_TIMEOUT_SECONDS` (60), `ASSISTANT_MAX_TOKENS` (1024),
`ASSISTANT_HISTORY_LIMIT` (20).

---

## POST /api/admin/stock/ocr-scan

Camera OCR for the "Nuevo artículo" modal. Accepts a photographed document
(albarán, etiqueta, ficha) and returns structured stock-article data extracted
by MiniMax vision.

- Auth: backoffice session cookie (`bo_session`) + permission `stock.ocr.upload` + AI entitlement.
- Body (multipart `image`, or JSON `{ "image": "data:image/jpeg;base64,…", "mediaType": "image/jpeg" }`).
- Resolution: MiniMax key + model come from `restaurant_minimax_config` (the model
  the user picked, e.g. `MiniMax-M3`); falls back to `MINIMAX_MODEL` env.
- Success: `{ "success": true, "model": "MiniMax-M3", "rawText": "...", "extraction": { "name": string|null, "quantity": number|null, "unit": string|null, "note": string|null } }`
- Failure (no key / model error): `{ "success": false, "message": "…" }` (HTTP 200 so the modal can show the message inline).

The backoffice modal pre-fills the manual "Nuevo artículo" form from `extraction.name`.

## Data migration: stock seed from menu (20260820_seed_stock_from_menu.sql)

One-time, manually-run (`mysql newvillacarmen < file.sql`) seed that turns the
existing catalogue into stock articles + technical sheets:

- Dishes (`comida_items`, `source_type='platos'`) → `SEMI_FINISHED` stock item + DRAFT `stock_recipes` (ficha técnica, empty), linked via `stock_item_id`/`stock_recipe_id` and `production_type='MANUFACTURED'`.
- Desserts (`POSTRES`) → same as dishes.
- Wines (`VINOS`) → `RAW` stock item (`deduction_source='SALE'`), linked via `stock_item_id`; no recipe.
- Beverages (`BEBIDAS`) / Coffees (`CAFES`) → omitted (no `stock_item_id` column yet, no rows).
- Idempotent: only rows with `stock_item_id IS NULL` are touched.

## Restaurant ads (backoffice) (`/api/admin/config/ads`)

Cookie-session auth. All write endpoints require `reservas` role.

- `GET /api/admin/config/ads` — list the active restaurant's ads. Each ad includes `image_generation_status` (`idle` | `pending` | `ready` | `failed`) and `image_generation_started_at` (RFC3339) so the editor can rehydrate skeletons after page reloads. `image_generation_status` is server-managed; clients cannot set it.
- `POST /api/admin/config/ads` — create an ad (initial `image_generation_status` = `idle`).
- `PUT /api/admin/config/ads/{adId}` — update an ad. Status is preserved across unrelated saves.
- `DELETE /api/admin/config/ads/{adId}` — delete the ad.
- `POST /api/admin/config/ads/{adId}/image/upload` — raw upload to BunnyCDN. On success sets `image_generation_status='ready'` (clears any stale `pending`).
- `POST /api/admin/config/ads/{adId}/image/enhance` — AI enhance (WaveSpeed / OpenAI edit). Sets `pending` + `image_generation_started_at` on entry, `ready` on success, `failed` on any error. The status persists across the call so the row stays as a skeleton even if the page is reloaded mid-flight.
- `POST /api/admin/config/ads/{adId}/image/generate` — AI text-to-image (WaveSpeed z-image/turbo). Same status lifecycle as `/enhance`.
