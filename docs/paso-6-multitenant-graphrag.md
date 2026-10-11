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

- [ ] Auditoria de cobertura tenant con el code graph: endpoints cuya cadena no llega a restaurantIDFromContext.
- [ ] Flujo/endpoint de onboarding de tenant (clonado desde plantilla).
- [ ] Retrieval de memoria en el loop del asistente + eval con Jev.
- [ ] Limpieza de defaults hardcoded que rompan un segundo tenant.
