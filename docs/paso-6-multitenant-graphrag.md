# Paso 6 - Multitenant reproducible + Graph RAG

Recon 2026-10-11 (dev). Objetivo original: "Hacer que sea lo mas reproducible posible para ser multitenant, y tener un graph rag para aprender de cada usuario de cada tenant y cada restaurant de cada tenant."

## Estado real

- Schema ya es multitenant profundo: tabla `restaurants` + ~30 tablas `restaurant_*` (branding, domains, websites, floors, salons, tables, stripe_connect, ai_provider_keys, minimax_config, uazapi_instances...).
- `bo_user_restaurants(user_id, restaurant_id, role)`: 7 filas, 4 restaurantes en dev.
- `internal/api/tenant.go`: middleware de resolucion por host (X-Forwarded-Host/Host -> `restaurant_domains`, cache con TTL), restaurantID en contexto de request. 6710 refs a `restaurant_id` en la API.
- `assistant_memory_nodes`/`assistant_memory_edges`: grafo de memoria por (restaurant_id, user_id); user_id=0 = memoria compartida del tenant. Extraccion por LLM con prompt dedicado. En dev apenas ejercitado (5 nodos, 2 edges).
- Identidad hardcoded: 233 refs villacarmen/alqueria en backend Go (no-test); muchas son defaults de config (CDN, DB, storage zone) ya sobreescribibles por env. Backoffice FE y bot-pipeline: 0 refs.
- `backend/graph/` (Neo4j): code graph de desarrollo, separado del producto; sirve para auditar cobertura tenant.

## Gaps

1. Cobertura de scoping: verificar que los 675 endpoints pasan por el middleware de tenant; detectar paths con asuncion global.
2. Reproducibilidad: el alta de un tenant nuevo no esta empaquetada. Hace falta onboarding (fila restaurants + dominios + satellites + seeds + storage zones + instancia WA) como flujo unico.
3. Defaults hardcoded: convertir en config por tenant los que rompan un segundo tenant (no cosmeticos).
4. Graph RAG de producto: la memoria existe pero falta retrieval (inyeccion de memorias relevantes en el contexto del asistente), edges mas ricos y metricas de acierto conectadas a Jev.
5. El restaurante demo (Alqueria) como plantilla clonable.

## Plan de trabajo (dev only)

- [x] Auditoria de cobertura tenant con el code graph: endpoints cuya cadena no llega a restaurantIDFromContext.
- [ ] Flujo/endpoint de onboarding de tenant (clonado desde plantilla).
- [ ] Retrieval de memoria en el loop del asistente + eval con Jev.
- [ ] Limpieza de defaults hardcoded que rompan un segundo tenant.

## Auditoria gap 1 - cobertura tenant (2026-10-11)

Metodo: code graph Neo4j (8036 nodos, 35707 edges) + revision de montaje de rutas en server.go. 1046 endpoints clasificados.

Resultado: SIN agujeros de cobertura inexplicados. Tres vias de resolucion de tenant, todas correctas:

1. Middleware por host (tenant.go): X-Forwarded-Host/Host -> restaurant_domains, cache TTL, restaurantID en ctx. Sitios publicos.
2. Middleware de sesion BO (requireBOSession -> backoffice_middleware.go): inyecta ActiveRestaurantID. Cubre los 584 endpoints /admin/* + site-builder + fichaje + bot config. NOTA: el code graph no ve middleware (no son CALLS edges), asi que una query pura de grafo marca falsos positivos; hay que cruzar con el montaje chi (r.With(...)).
3. Resolucion propia en superficies publicas (public_booking_api.go, webhooks UAZAPI): ctx primero, fallback documentado a DEFAULT_RESTAURANT_ID para despliegues single-domain.

Detalles verificados:
- 0 tenant IDs hardcodeados en codigo (el unico "1" era un comentario de doc en public_ads.go).
- assistant_*_tools: la firma (ctx, rid, input) ignora rid pero delega en los handlers BO reales con el ctx (assistantCallHandler), asi que el scoping fluye por contexto. Falsa alarma.
- mcp: OAuth 2.1 PKCE propio, inyecta ActiveRestaurantID del token.
- website-builder publico: render de fragmentos, global por diseno.
- DEFAULT_RESTAURANT_ID: fallback intencionado y documentado, no un agujero.

Conclusion: el sustrato multitenant esta mas sano de lo esperado (ver coordination ids stripe_connect_multitenant_v1). Gap 1 cerrado. Siguiente: gap 2 (onboarding de tenant empaquetado).

## Gap 2 - onboarding de tenant (2026-10-11)

Resultado: YA EXISTE como API de plataforma. No hace falta empaquetar nada nuevo, solo documentar el runbook.

Flujo real (verificado en codigo):
1. POST /admin/platform/restaurants (requireBOSession + requireBOSuperadmin): crea la fila restaurants (slug, name, CIF, contacto).
2. provisionRestaurantDefaults: siembra idempotente (INSERT IGNORE) de branding, reservation_defaults, info, bot_ai_config, page_visibility.
3. provisionRestaurantOwner: membership owner del creador en bo_user_restaurants.
4. Dominios: gestion via plataforma (restaurant_domains); la resolucion por host (tenant.go) los usa en cuanto existen.
5. WhatsApp: pool UAZAPI con aprovisionamiento por capacidad (backoffice_uazapi_provisioning.go) + connect/QR desde /app/config.
6. Desactivar/reactivar: POST /platform/restaurants/{id}/deactivate|activate.

Runbook para tenant nuevo:
- Crear via platform API (superadmin) -> restaurante operativo con defaults en segundos.
- Asignar dominio(s) en plataforma -> el sitio publico resuelve por host.
- Conectar WhatsApp desde el backoffice del tenant -> sale del pool UAZAPI.
- Invitar usuarios (bo_member_invitation_tokens) con rol por restaurante.

Mejora opcional detectada: sembrar un dominio por defecto en create (p.ej. {slug}.menustudioai.com) para que el tenant resuelva sin paso manual. Decision de producto, no bug.

Conclusion: gap 2 cerrado. La reproducibilidad ya es real: crear tenant = una llamada + dominio + conectar WA.
