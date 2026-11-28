# GraphRAG multi-tenant (neo4j-rust) — diseño (vitess31, solo local)

Coordination ids: graphrag_tenant_instance_v1, graphrag_ingest_v1, graphrag_retrieve_v1.

## 1. Modelo de amenazas
| Amenaza | Vector | Mitigación |
|---|---|---|
| Lectura/escritura cross-tenant | neo4j-rust tiene UN token global y no autoriza por grafo, así que los named graphs NO son una frontera | Un proceso por restaurante, con su propio data dir, su propio token 0600 y su propio puerto loopback. Con el token de A no se puede abrir la instancia de B. |
| Prompt injection que dirige una tool a otro tenant | El modelo pide "busca en el restaurante 7" o inventa un graph, puerto o token | La tool de retrieval no tiene parámetros de tenant. restaurant_id sale solo del contexto autenticado (tenant.go / ActiveRestaurantID / instancia WA). Puerto y token se resuelven server-side desde registry.tsv. |
| Cypher libre | El modelo o el cliente envía Cypher | Solo hay plantillas Cypher read-only fijas en el backend. El texto del usuario entra como $param, nunca concatenado. Las labels salen de una allowlist. |
| Fuga de token | Logs, ps, env, backups | Token en un fichero 0600 por tenant, pasado como NEO4J_RUST_TOKEN_FILE (no en argv). Nunca se imprime. Los backups excluyen el token. El registry es 0600. |
| Backups mezclados | Restaurar el tar de A sobre B | El nombre del backup lleva el rid y el tar se crea dentro de ~/graphrag/<rid>/backups. Restore manual solo sobre el mismo rid (ver §7). |
| Exposición en red | bind 0.0.0.0 | Solo --bind/--http 127.0.0.1. graph-ingest rechaza cualquier host distinto de 127.0.0.1. |
| Agotamiento de recursos | Un tenant consume la máquina | Unidad systemd --user con CPUQuota=200% y MemoryMax=2G por tenant. |

## 2. Aislamiento elegido
Un proceso neo4j-rust por restaurant_id:
- Datos en ~/graphrag/<rid>/data y token en ~/graphrag/<rid>/token (0600).
- Puertos: http = 20000+2·rid y bolt = http+1, ambos en 127.0.0.1.
- Mapeo exclusivo rid → (puerto, fichero de token) en ~/graphrag/registry.tsv (0600), leído solo por el backend o graph-ingest.
- Se descartan: named graphs en una instancia compartida (token global) e instancia compartida + filtro por propiedad (un fallo en una plantilla filtraría datos).

## 3. Modelo de datos
- (:Source {source_key, restaurant_id, kind, scope, path, content_hash, ingested_at})
- -[:HAS_CHUNK]->(:Chunk:<Prompt|Policy|EdgeCase|Faq|Menu|Rule> {restaurant_id, ord, title, body, content_hash})
- Fase 2, relaciones semánticas: (Chunk)-[:APPLIES_TO]->(:Rule), (EdgeCase)-[:OVERRIDES]->(Policy), (Faq)-[:ABOUT]->(:Menu), (Prompt)-[:USES]->(Policy).
- restaurant_id se guarda también como propiedad en cada nodo, como defensa en profundidad: cada plantilla lo filtra aunque la instancia ya sea del tenant.

## 4. Inventario de fuentes (worktree; producción/Kraken no leídos)
Plantilla global, la misma para todos los tenants. Rellenada con datos de cada tenant en runtime:

| path | bytes | contenido |
|---|---|---|
| internal/api/whatsapp_bot_prompt.go | 45160 | renderBotSystemPrompt: system prompt del bot WA |
| internal/api/whatsapp_bot_hard_policy.go | 15769 | políticas duras |
| internal/api/whatsapp_bot_special_policy.go | 19474 | políticas de fechas especiales |
| internal/api/whatsapp_bot_rag.go | 19323 | botDefaultKnowledge V1/V2: chunks FAQ |
| internal/api/whatsapp_bot_privileged.go | 16264 | reglas de contactos privilegiados |
| internal/api/whatsapp_bot_engine.go | 25122 | reglas de enrutado |
| internal/api/assistant_llm.go | 34131 | prompt del asistente BO |
| internal/api/assistant_memory.go | 8944 | prompt de extracción de memoria |
| docs/whatsapp-bot-phase1-policy-postgres.md | 22644 | casos límite / política fase 1 |
| docs/wa-bot-backoffice-coverage.md | 11320 | cobertura y casos límite |
| docs/AI_USAGE_AND_WHATSAPP_LOGS.md | 4952 | reglas de uso y logs |

Total ≈ 223 KB. Machine-readable list: docs/graphrag/sources.tsv.

Específico de cada tenant, en tablas del PG público (no leídas por el worker; ingesta en fase 2 desde PG, sin MySQL):
- whatsapp_bot_config (25 lecturas en el código)
- restaurant_bot_ai_config
- bot_knowledge_fts: índice derivado por restaurante en el esquema bot_context; se reconstruye, no es fuente
- assistant_memory_nodes/edges: memoria por (restaurant_id, user_id)

No hay ficheros markdown de edge cases por tenant en el repo.

Observación: los prompts globales están dentro de código Go. La fase 1 ingiere el fichero troceado por cabecera, o como un solo chunk si no es markdown. La fase 2 debe exportar las constantes y plantillas a .md versionados para que el grafo y el código compartan una única fuente.

## 5. Ingesta (cmd/graph-ingest)
- Uso: `graph-ingest -restaurant-id N [-inventory docs/graphrag/sources.tsv] [-registry ~/graphrag/registry.tsv] [-apply]`. Sin -apply es dry-run: imprime path, bytes, chunks y prefijo de hash.
- Rechaza ejecutarse en estos casos:
  - falta rid o rid <= 0
  - registry no 0600
  - token no 0600
  - rid no registrado
  - host distinto de 127.0.0.1
- Es idempotente por content_hash (sha256 del fichero):
  - Si el Source existe con el mismo hash, lo salta.
  - Si no, en un /tx all-or-nothing borra los Chunk y el Source y los recrea.
  - neo4j-rust no tiene MERGE en su subset, de ahí el patrón borrar y recrear.
- Atribución: cabecera X-Source: graph-ingest:<rid>, que queda registrada en el history de neo4j-rust.

## 6. API de retrieval (backend, fase 2, no implementada)
- Función interna `botGraphRetrieve(ctx, routes, text)`. El rid se toma de ctx. No es un endpoint público y no recibe tenant, puerto ni graph del cliente ni del modelo.
- Plantillas fijas read-only, por ejemplo:
  `MATCH (s:Source {restaurant_id:$r})-[:HAS_CHUNK]->(c:Chunk) WHERE c.restaurant_id=$r AND c.title CONTAINS $q RETURN c.title, c.body LIMIT 8`
  - CONTAINS no está garantizado en el subset de neo4j-rust; si falta, se hace un prefiltro por label y el ranking en Go.
- Se envía a /query con el token del tenant y X-Source: bot:<rid>.
- Antes de usar la respuesta se comprueba que restaurant_id == rid en cada fila. Si no coincide se descarta y se registra el evento graphrag_tenant_mismatch.
- Si la instancia cae, se vuelve al bot_knowledge_fts actual, que ya está aislado por restaurant_id. El bot no falla.

## 7. Backup/restore por tenant
- `tenant-instance.sh backup <rid>`: para el servicio, crea data.tar.gz en ~/graphrag/<rid>/backups (0600) y vuelve a arrancar. El token queda fuera.
- Restore manual:
  1. stop
  2. mover data a data.prev
  3. tar -xzf sobre ~/graphrag/<rid>
  4. start
- Se verifica con /stats y con el history de neo4j-rust (restore a versión con POST /restore).
- Los grafos son derivados (reingesta desde repo + PG), así que el backup es una conveniencia, no la fuente de verdad.

## 8. Presupuesto en el escritorio
- RAM: 59 GB en total; el techo es 2 GB por tenant (MemoryMax). Con ~223 KB de texto por tenant el RSS real esperado es de decenas de MB (sin medir).
- Disco: /home al 92 % (79 GB libres). Estimación por tenant: datos ≈ 1–5 MB (texto ×~5 por WAL, snapshot e history) más backups de ~1 MB comprimido.
  - 4 tenants de desarrollo: < 50 MB.
  - Política: conservar solo los 3 últimos backups por tenant (por ahora a mano).

## 9. Encaje tras el cutover a PG
- Fuentes: repo + tablas del PG público (whatsapp_bot_config, restaurant_bot_ai_config) y bot_context. No hay dependencia de MySQL.
- La ingesta por tenant se dispara al guardar la config del bot (fase 2, mediante hook) o a mano. El backend lee registry.tsv al arrancar y arranca aunque falte la instancia de un tenant, usando el fallback FTS.
- Todo queda local. Nada en producción ni en Kraken hasta que haya aprobación explícita.
