# Backend (Go) reglas del proyecto

Scope: todo lo que cuelga de `backend/`.

## Objetivo
- Mantener API estable y compatible con legacy, con foco en rendimiento y fiabilidad.
- Preservar contratos JSON usados por `preactvillacarmen/frontend` y `backoffice`.
- Minimizar latencia y evitar regressions en rutas `.php` heredadas.

## Skills a usar en este scope
- `villacarmen-backend-api`:
  usar por defecto para cualquier `read/edit/update` de handlers, routing o auth en `backend/`.
- `villacarmen-backend-migrations`:
  usar cuando la tarea toque schema SQL o archivos de `internal/db/migrations/`.
- `villacarmen-contract-sync`:
  usar además si el cambio de backend impacta frontend/backoffice.
- `villacarmen-smoke-check`:
  usar al final para validación rápida antes de cerrar la tarea.

## Stack y arquitectura
- Go (`net/http`) + `chi` para routing.
- MySQL con `database/sql`.
- Entrypoint: `cmd/server/main.go`.
- El backend sirve:
  - API bajo `/api/*`.
  - Alias legacy sin prefijo `/api` para endpoints antiguos.
  - Static SPA desde `../preactvillacarmen/frontend/dist` (o `STATIC_DIR`).

## Rutas y compatibilidad
- Mantener `/api/admin/*` funcionando via rewrite interno a `/admin/*`.
- No eliminar alias legacy `.php` sin migracion equivalente y validada.
- Si se toca routing, validar que no se rompan:
  - Endpoints publicos de Preact.
  - Endpoints admin legacy (`X-Admin-Token`).
  - Endpoints backoffice con cookie de sesion (`bo_session`).

## Contratos de respuesta
- Estandar principal:
  - Exito: `{ success: true, ... }`
  - Error: `{ success: false, message: "..." }`
- Compatibilidad legacy permitida:
  - `{ status: "success|error|warning", ... }`
- No cambiar nombres de campos consumidos por frontend/backoffice sin coordinar migracion.

## Autenticacion y autorizacion
- Admin legacy: `X-Admin-Token` o `Authorization: Bearer ...`.
- Backoffice: cookie `bo_session`.
- Integraciones internas: `X-Api-Token`.
- Nunca loguear secretos ni tokens completos en texto plano.

## Base de datos y timeouts
- Toda operacion de DB debe usar `context` con timeout.
- Mantener pool de conexiones estable y evitar queries sin limites cuando haya listados grandes.
- Migraciones SQL:
  - Archivo nuevo en `internal/db/migrations/`.
  - Registrar en `internal/db/migrations/migrations.go`.
  - Deben ser idempotentes o seguras para despliegue incremental.

## Cache y rendimiento
- Mantener soporte `ETag`/`If-None-Match` en endpoints que ya lo usan (ej. vinos/calendar).
- Evitar trabajo innecesario en handlers (N+1, serializacion duplicada, lecturas repetidas).
- Mantener timeouts HTTP del servidor y no ampliar sin motivo justificado.

## Seguridad
- Nunca hardcodear credenciales o claves.
- Configuracion via variables de entorno (`PORT`, `DB_*`, `ADMIN_TOKEN`, `CORS_ALLOW_ORIGINS`, etc.).
- Validar input y devolver errores consistentes sin filtrar detalles internos.

## Regla de cambios
- Si migras o anades endpoint:
  - Documentar ruta, auth, params y shape en `backend/ENDPOINTS.md`.
  - Mantener compatibilidad con consumidor actual o incluir plan de transicion.
- Evitar refactors masivos fuera de scope.

## Checklist rapido antes de cerrar
1. `go vet ./...` + `go build ./...` (compilacion de paquetes afectados). **Sin TDD ni tests obligatorios**: no escribir ni ejecutar tests salvo peticion expresa del usuario.
2. Verificar formato y compilacion (`go fmt` en archivos tocados cuando aplique).
3. Validar al menos un flujo real del endpoint cambiado.
4. Confirmar que no se rompieron rutas legacy relacionadas.

---

## Workflow por tarea (obligatorio)

Toda tarea nueva sigue este flujo completo, sin omitir pasos:

1. **Plan en worktree nuevo**:
   ```bash
   git worktree add /home/jaime/wt/<repo>-<tarea> -b <tipo>/<tarea> main
   cd /home/jaime/wt/<repo>-<tarea>
   ```
2. **Editar + verificar**: implementar cambios y validar con `go vet ./...` + `go build ./...`. Sin tests ni TDD salvo peticion expresa.
3. **Commit + push** de todos los cambios al branch nuevo:
   ```bash
   git add -A && git commit -m "<conventional commit msg>"
   git push -u origin <tipo>/<tarea>
   ```
4. **Abrir PR** contra `main` del repo base.
5. **Loop de review** con el skill `pr-review`:
   - Corregir todo blocker importante o medio encontrado.
   - Re-correr `pr-review` tras cada fix.
   - Repetir hasta que no queden blockers importantes ni medios.
6. **Merge** del PR.
7. **Refetch + pull** en el repo base:
   ```bash
   git fetch --all --prune
   git checkout main && git pull --ff-only
   ```
8. **Limpiar worktree y branches** del PR ya mergeado:
   ```bash
   git worktree remove /home/jaime/wt/<repo>-<tarea>
   git branch -D <tipo>/<tarea>
   git push origin --delete <tipo>/<tarea>
   ```

Reglas del workflow:
- Una tarea = un worktree = un branch = un PR.
- Nunca editar directo sobre `main`.
- Si una tarea afecta varios repos (`backend`, `preactvillacarmen`, `backoffice`), abrir un PR por repo con los cambios relacionados; coordinar merges.
- El loop `pr-review` → fix → `pr-review` es obligatorio antes del merge.
