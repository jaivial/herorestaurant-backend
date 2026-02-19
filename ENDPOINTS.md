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

## Conventions

- Most endpoints return JSON with either:
  - `{ "success": true, ... }` / `{ "success": false, "message": "..." }`, or
  - legacy `{ "status": "success|error|warning", ... }` for some admin UIs.
- Legacy form endpoints usually accept `multipart/form-data` (FormData) and also `application/x-www-form-urlencoded`.
- Some endpoints accept `application/json` bodies where the legacy JS sends JSON.

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
- Jerarquía de importancia (0-100):
  - `root = 100`, `admin = 90`, resto por debajo.
- Sección de miembros/roles:
  - Los endpoints de miembros y roles requieren importancia `>= 90` (admin/root).

### `POST /api/admin/login`
Body (JSON):
- `identifier` (string, recomendado; acepta email o username)
- `email` (string, compat legacy)
- `password` (string)

Response:
- `{ success: true, session: { user, restaurants, activeRestaurantId } }`
- `session.user` incluye `role`, `roleImportance`, `sectionAccess`, `username?` y `mustChangePassword`.
- `{ success: false, message: string }`

### `POST /api/admin/logout`
Response:
- `{ success: true }`

### `GET /api/admin/me`
Response:
- `{ success: true, session: { user, restaurants, activeRestaurantId } }`
- `session.user` incluye `role`, `roleImportance`, `sectionAccess`, `username?` y `mustChangePassword`.

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

Response:
- `{ success: true, member: Member, user?, role?, invitation?, provisioning? }`

### `POST /api/admin/members/{id}/invitation/resend`
Regenera invitación para un miembro activo.

Behavior:
- Invalida tokens activos anteriores del mismo miembro.
- Requiere que el miembro tenga al menos email o teléfono.

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

Response:
- `{ success: true, member: Member }`

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
- `q` (optional): searches **only** by `customer_name` (`LIKE %q%`)
- `sort` (optional, default `reservation_time`): `reservation_time|added_date`
- `dir` (optional, default `asc`): `asc|desc`
- `page` (optional, default `1`) (1-based)
- `count` (optional, default `15`) (max `25`)

Legacy compatibility:
- If `page`/`count` are absent, the endpoint also accepts `limit`/`offset`.

Response:
- `{ success: true, bookings: Booking[], floors: Floor[], total_count: number, total: number, page: number, count: number }`
- `floors` usa la misma estructura `Floor` de config y representa el estado activo del dia consultado.

### `GET /api/admin/bookings/export`
Exports **all** bookings for a date (no filters; used for PDF export).

Query params:
- `date` (required `YYYY-MM-DD`)

Response:
- `{ success: true, bookings: Booking[] }`

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
- `special_menu` (boolean)
- If `special_menu=true`:
  - `menu_de_grupo_id` (number, required)
  - `principales_json` (array, optional; rows `{ name, servings }`)
- If `special_menu=false`:
  - `arroz_types` (string[])
  - `arroz_servings` (number[])

Response:
- `{ success: true, booking: Booking }` (or `{ success: true, id: number }` best-effort)
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
- settings (`included_coffee`, `beverage`, `comments`, `min_party_size`, `main_dishes_limit`, `main_dishes_limit_number`)
- `sections[]` and nested `dishes[]`

Response:
- `{ success: true, menu: { ... } }`

### `PATCH /api/admin/group-menus-v2/{id}/basics`
Upserts menu metadata and settings (patch semantics).

Body (JSON, any subset):
- `menu_title`, `price`, `active`, `is_draft`, `menu_type`
- `menu_subtitle` (`string[]`)
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
- `sections`: array of `{ id?, title, kind }`

Rules:
- At least 1 section is required.
- Removed section IDs are deleted.

Response:
- `{ success: true }`

### `PUT /api/admin/group-menus-v2/{id}/sections/{sectionId}/dishes`
Replaces dishes for one section (ordered).

Body (JSON):
- `dishes`: array of `{ id?, catalog_dish_id?, title, description, allergens, supplement_enabled, supplement_price, active? }`

Response:
- `{ success: true }`

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

### `GET /api/admin/config/defaults`
Returns restaurant-level default config used as fallback in daily config.

Response:
- `{ success: true, openingMode, morningHours, nightHours, weekdayOpen, hours, dailyLimit, mesasDeDosLimit, mesasDeTresLimit }`
- `weekdayOpen`: objeto por dia con claves `monday..sunday` y valor booleano `open/closed`.

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

Response:
- Same shape as `GET /api/admin/config/defaults`.

### `GET /api/admin/config/day?date=YYYY-MM-DD`
Returns open/closed day state.
- Fallback si no existe override en `restaurant_days`: se usa `weekdayOpen` de `restaurant_reservation_defaults`.

Response:
- `{ success: true, date, isOpen }`

### `POST /api/admin/config/day`
Upserts open/closed day state.

Body (JSON):
- `date` (`YYYY-MM-DD`)
- `isOpen` (boolean)

Response:
- `{ success: true, date, isOpen }`

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

Response:
- `{ success: true, floors: Floor[] }`

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

Response:
- `{ success: true, date, floors: Floor[] }`

`Floor`:
- `{ id, floorNumber, name, isGround, active }`

## Public Menu / Navigation

### `GET /api/menu-visibility` (alias: `GET /menu-visibility`)
Returns the current visibility flags used by navigation.

Response:
- `{ success: true, menuVisibility: { menudeldia: boolean, menufindesemana: boolean, ... } }`

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
- `sections` (`PublicMenuSection[]`)
- `special_menu_image_url` (string; empty when not set)
- `legacy_source_table` (string; optional, e.g. `DIA|FINDE`)
- `created_at`, `modified_at` (string)

`PublicMenuSection`:
- `id` (number)
- `title` (string)
- `kind` (string; normalized kind)
- `position` (number)
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

### `GET /api/menuVisibilityBackend/getMenuVisibility.php`
Query params:
- `menu_key` (optional). If absent, returns all menus.

Response:
- `{ success: true, menu: {...} }` or `{ success: true, menus: [...] }`

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

### `GET /api/menuDeGruposBackend/getActiveMenusForDisplay.php`
Response:
- `{ success: true, menus: MenuDeGrupoDisplay[] }`

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

### `GET /api/fetch_arroz.php`
Returns a bare JSON array of rice dish descriptions:
- `string[]`

### `POST /api/fetch_daily_limit.php`
Form:
- `date` (`YYYY-MM-DD`)

Response:
- `{ success: true, date, dailyLimit, totalPeople, freeBookingSeats }`

### `POST /api/fetch_month_availability.php`
Form:
- `month` (int `1-12`)
- `year` (int)

Response:
- `{ success: true, month: number, year: number, availability: { [YYYY-MM-DD]: { dailyLimit: number, totalPeople: number, freeBookingSeats: number } } }`

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
- `partySize` (int)

Response:
- `{ success: true, menus: [...] }`

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

### `GET /api/fetch_closed_days.php`
Response:
- `{ success: true, closed_days: string[], opened_days: string[] }`

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

### `POST /api/insert_booking_front.php`
Form:
- Standard reservation fields (name/email/date/time/party_size/phone, etc.)
- Optional arroz selection JSON fields (as in legacy JS)
- Optional group menu fields:
  - `special_menu=1`
  - `menu_de_grupo_id`
  - `principales_enabled`
  - `principales_json` (JSON array)

Response:
- `{ success: true, booking_id: number, notifications_sent: false, email_sent: false, whatsapp_sent: false }`

### `POST /api/insert_booking.php` (admin)
Form:
- `date`, `time`, `nombre`, `phone`, `special_menu`, etc.

Response:
- `{ success: true, booking_id: number, whatsapp_sent: false }`

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
