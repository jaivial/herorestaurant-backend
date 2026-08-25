# Deploy producción Docker — newvillacarmen

Estado: **runbook canonical**. El stack dev (`docker-compose.dev.yml` en la raíz
del host) queda retirado de este host una vez ejecutado el cutover.

## Arquitectura final

```
Internet → nginx :443 alqueriavillacarmen.com → http://127.0.0.1:8080
              └─ contenedor backend Go (network host)
                 · SPA preact compilada (STATIC_DIR=/app/static, dist embebida en imagen)
                 · /api/* (públicos + admin legacy)
                 · endpoints legacy raíz (confirm_reservation.php, cancel_reservation.php, ...)

Internet → nginx :443 backoffice.alqueriavillacarmen.com (cert Let's Encrypt)
              ├─ /bot/  → http://127.0.0.1:8080   (webhooks Evolution)
              └─ resto  → http://127.0.0.1:3011   (contenedor backoffice SSR prod)
                 · SSR vike + proxy interno /api/admin → BACKEND_ORIGIN (:8080)
                 · WS fichaje (/api/admin/fichaje/ws)

backend Go → MySQL host 127.0.0.1:3306 (DB newvillacarmen)
           → Evolution API self-hosted (https://eai.jaimedigitalstudio.com,
             credenciales por instancia en DB + EVOLUTION_WEBHOOK_SECRET)

DNS (Bunny): backoffice.alqueriavillacarmen.com A → 65.109.100.94
```

- PHP-FPM deja de servir alqueriavillacarmen.com (full cutover). El webroot
  legacy `/var/www/alqueriavillacarmen` NO se toca (rollback).
- Media sigue saliendo de `villacarmenmedia.b-cdn.net` (sin cambios).
- Evolution API (mismo host) no se toca; solo cambia la webhook URL pública.

## Ficheros de este cambio (repo herorestaurant-backend)

| Fichero | Qué es |
|---|---|
| `Dockerfile.prod` | Imagen backend: build preact dist + build Go + runtime alpine. Context = raíz multi-repo (`/var/www/newvillacarmen`). |
| `Dockerfile.prod.dockerignore` | Whitelist del context (solo backend/go.mod,go.sum,cmd,internal + preactvillacarmen/frontend). |
| `deploy/docker-compose.prod.yml` | Compose prod (`newvillacarmen-prod`): backend + backoffice, network host. |
| `deploy/nginx/alqueriavillacarmen.conf` | Vhost cutover (todo → :8080). |
| `deploy/nginx/backoffice.alqueriavillacarmen.conf` | Vhost backoffice nuevo (:3011, /bot/ → :8080). |
| `.env.prod.example` | Template de secrets del backend (copiar a `.env.prod`, sin commitear). |

Repo `backofficereact`: `Dockerfile.prod` (build vike + runtime bun) y
`.env.prod.example`.

## Redeploys futuros

```bash
# backend + frontend público (dist embebida):
sudo docker compose -f /var/www/newvillacarmen/backend/deploy/docker-compose.prod.yml up -d --build backend

# backoffice:
sudo docker compose -f /var/www/newvillacarmen/backend/deploy/docker-compose.prod.yml up -d --build backoffice
```

## Secuencia de deploy (orden estricto)

1. **PRs mergeados** (backend + backoffice) y `main` actualizado en el host:
   ```bash
   cd /var/www/newvillacarmen/backend && git checkout main && git pull --ff-only
   cd /var/www/newvillacarmen/backoffice && git checkout main && git pull --ff-only
   ```
2. **Env files de prod** en el host (valores reales; ver `.env.prod.example`
   de cada repo como checklist):
   - `/var/www/newvillacarmen/backend/.env.prod`
   - `/var/www/newvillacarmen/backoffice/.env.prod`
   - `chmod 600`; nunca commitear.
3. **Bunny DNS** (API, credenciales por env `BUNNY_API_KEY` root-only):
   ```bash
   export BUNNY_API_KEY=<account api key>   # rotar la que se expuso en chat
   curl -sS -H "AccessKey: $BUNNY_API_KEY" https://api.bunny.net/dnszone | jq '.Items[] | {Id,Domain}'
   # crear registro (si la zona existe):
   curl -sS -X POST -H "AccessKey: $BUNNY_API_KEY" -H 'Content-Type: application/json' \
     https://api.bunny.net/dnszone/<ZONE_ID>/records \
     -d '{"Type":0,"Name":"backoffice","TTL":300,"Value":"65.109.100.94"}'
   dig +short backoffice.alqueriavillacarmen.com   # → 65.109.100.94
   ```
   Si la zona DNS no existe en Bunny → crearla y cambiar NS en el registrador
   (paso manual) antes del cert.
4. **Cert** (tras propagación DNS):
   ```bash
   sudo certbot --nginx -d backoffice.alqueriavillacarmen.com
   ```
5. **nginx**: backup del vhost legacy + instalación de los nuevos:
   ```bash
   sudo cp /etc/nginx/sites-enabled/alqueriavillacarmen.conf \
           /etc/nginx/sites-enabled/alqueriavillacarmen.conf.legacy-php.bak
   sudo cp backend/deploy/nginx/alqueriavillacarmen.conf /etc/nginx/sites-enabled/
   sudo cp backend/deploy/nginx/backoffice.alqueriavillacarmen.conf /etc/nginx/sites-enabled/
   sudo nginx -t && sudo systemctl reload nginx
   ```
6. **Swap de contenedores** (baja dev, sube prod):
   ```bash
   sudo docker compose -f /var/www/newvillacarmen/docker-compose.dev.yml down
   sudo docker compose -f /var/www/newvillacarmen/backend/deploy/docker-compose.prod.yml up -d --build
   ```
7. **Evolution post-deploy**: actualizar la webhook URL de las instancias a
   `https://backoffice.alqueriavillacarmen.com/bot/webhook/evolution/<secret>`
   (panel Evolution o backoffice).

## Rollback

- Web pública: restaurar `alqueriavillacarmen.conf.legacy-php.bak` y
  `sudo systemctl reload nginx` (el PHP sigue en disco, intacto).
- Backoffice: quitar vhost nuevo o repuntar a los contenedores dev.
- Contenedores: `docker compose -f backend/deploy/docker-compose.prod.yml down`
  + re-levantar dev compose si hace falta.

## Contexto persistente del bot WhatsApp

El backend guarda el transcript conversacional del bot en SQLite mediante `BOT_CONTEXT_SQLITE_PATH`. En Docker producción el compose monta el volumen nombrado `whatsapp-bot-context` en `/var/lib/herorestaurant`, por lo que el contexto sobrevive a recreaciones y rebuilds del contenedor. Los mensajes OTP de verificación no se guardan en este transcript.

## Verificación post-deploy

1. `docker compose -f backend/deploy/docker-compose.prod.yml config` OK.
2. `curl -sSI http://127.0.0.1:8080/ | head -3` → 200 SPA index.
3. `curl -sSI http://127.0.0.1:3011/login | head -3` → 200 SSR login.
4. `https://alqueriavillacarmen.com/` → home preact; `/api/menu/vinos` con
   `If-None-Match` → 304 (ETag).
5. Link email viejo:
   `curl -sSI 'https://alqueriavillacarmen.com/confirm_reservation.php?id=1&token=x'`
   → respuesta del backend Go (no 404 nginx).
6. `https://backoffice.alqueriavillacarmen.com/login` → cookie `bo_session`
   Secure; WS fichaje conecta; subida de imagen OK (50m body).
7. Webhook Evolution: POST test `/bot/webhook/evolution/<secret>` → 200.
8. Emails invitación/reset con links `https://backoffice.alqueriavillacarmen.com/...`.

## Riesgos conocidos (aceptados)

- Páginas solo-PHP (`ad_*.php`, `/emailAdvertising/`) dejan de responder.
  Contingencia: location nginx específica → PHP-FPM para las que hagan falta.
- `ADMIN_TOKEN` / `INTERNAL_API_TOKEN` vacíos en dev. En prod: tokens fuertes;
  si el JS legacy o n8n usan valores hardcodeados, reutilizarlos (grep en
  `/var/www/alqueriavillacarmen` + config n8n durante el deploy).
- Bunny API key expuesta en chat → rotar tras el deploy.
