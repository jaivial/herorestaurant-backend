# result-pgnative-g — GraphRAG con datos de PostgreSQL (source/build only)

Autoridad: Main 14:13 ("Go" de Jaime para GraphRAG con datos de PG y QA en desktop/scratch; sin cutover, sin deploy, sin kraken). Worktree pgnative, sin commit. **Nada ejecutado** contra BD ni instancias.

## Construido
- **internal/graphrag** (nuevo):
  - `Lookup(registry, rid)`:
    - lee el registro del servidor (`VC_GRAPHRAG_REGISTRY` o ~/graphrag/registry.tsv);
    - exige 0600 en el registro y en el fichero de token;
    - rechaza un restaurante repetido o un puerto compartido por dos restaurantes;
    - el host siempre es 127.0.0.1;
    - el token no se exporta ni se registra en logs.
  - `LoopbackOnly()`: lee /proc/net/tcp y tcp6 y falla si el puerto escucha fuera de loopback.
  - `Query(ctx, restaurantID, text, k)`:
    - solo activa con `VC_GRAPHRAG=1` (por defecto OFF);
    - lee hasta 2000 nodos `Doc` del restaurante con una plantilla Cypher fija;
    - ranking en Go por términos (minúsculas, sin acentos);
    - descarta filas con otro restaurant_id;
    - cualquier error devuelve vacío.
- **Bot** (internal/api/whatsapp_bot_rag.go):
  - con el flag ON, `botRetrieveKnowledge` antepone hasta 4 snippets del grafo (timeout 3 s) al RAG actual. Con el flag OFF el comportamiento es idéntico al anterior.
  - El restaurantID es el de la sesión del bot y la instancia sale solo del registro.
  - El log `wa_bot_graphrag_v1` registra solo restaurant_id, la longitud del texto y el número de hits.
- **cmd/graph-ingest** (reescrito):
  - Exige `DB_DRIVER=postgres` y `PG_DSN`.
  - Transacción READ ONLY revertida; todas las consultas llevan `WHERE restaurant_id = $1` (restaurants por id).
  - **Dry-run (por defecto):** valida el registro, los permisos 0600 y el bind loopback (antes no se validaba en dry-run; corregido) e imprime los recuentos por tipo.
  - **`-apply`:** upsert idempotente por uid estable y hash de contenido.
  - **`-resync`:** borra solo los nodos de ese restaurante que ya no están en PG.
  - Las aristas del restaurante se reconstruyen en cada aplicación.
  - **`-query`:** QA manual de la recuperación; imprime uid, tipo y título.
  - La ingesta de ficheros del repo (sources.tsv) se ha retirado de este comando.
- **scripts/graphrag/qa-tenants.md**: receta manual 901/902 (seed, dos instancias, ingesta, aislamiento, token cruzado → 401, reinicio, resync, borrado).

## Tablas leídas (esquema nativo)
- restaurants, restaurant_info
- comida_categories, comida_items (platos y demás source_type)
- "POSTRES", "BEBIDAS"
- group_menu_wines_v2: los vinos; **no existe tabla VINOS en el origen**
- menus (incluye seo_faq como FAQ), group_menu_sections_v2 + group_menu_section_dishes_v2
- openinghours (de hoy a hoy + 60 días)

Las FAQ/conocimiento del bot (bot_knowledge, chunks) viven en el store bot_context y no se leen.

## Esquema del grafo
- **Nodos:** etiquetas `Doc:<Restaurant|Category|Dish|Dessert|Drink|Wine|Menu|MenuDish|OpeningHours>`.
  - Propiedades: `uid` (`restaurant:<id>:<kind>:<id fuente>`), `restaurant_id`, `kind`, `title`, `body` (texto con precio, alérgenos, descripción…), `price`, `hash`.
- **Aristas:**
  - Restaurant -HAS_CATEGORY-> Category
  - Category -HAS_ITEM-> Dish (o Restaurant -OFFERS-> Dish sin categoría)
  - Restaurant -OFFERS-> Dessert/Drink/Wine/Menu
  - Menu -HAS_DISH-> MenuDish
  - Restaurant -HAS_HOURS-> OpeningHours

## Flags y entorno
- `VC_GRAPHRAG=1`: activa la recuperación en el bot. Por defecto OFF.
- `VC_GRAPHRAG_REGISTRY`: ruta del registro (por defecto ~/graphrag/registry.tsv).
- graph-ingest: `-restaurant-id`, `-apply`, `-resync`, `-registry`, `-query`; entorno `DB_DRIVER=postgres`, `PG_DSN`.

## Build
`go build -p 2 ./...` OK; `go vet ./internal/graphrag ./cmd/graph-ingest` OK.

| binario (CGO_ENABLED=0) | sha256 |
|---|---|
| /tmp/villacarmen-bot-20261111/graph-ingest | 79efccebb41868d6d189dee5418eda1014e149d0f366927c76cd6d9f23bcac88 |
| /tmp/villacarmen-bot-20261111/vc-server-pgnative-static | 00fbd5665f184f8a6b0a3bad42d937520731e2f22437c7f5145295420e24f9dc |

Este servidor incluye también el arreglo de E (importación de stock por app.norm).

## No verificado
- **Subconjunto Cypher de neo4j-rust:** no se ha probado que acepte estas sentencias:
  - CREATE con varias etiquetas (`:Doc:Dish`);
  - MATCH con varios patrones separados por coma para crear aristas;
  - `DELETE e` sobre un patrón de relación;
  - `LIMIT` literal.

  Si algo falla, el QA lo mostrará: graph-ingest fallará en `-apply` y Query devolverá vacío, sin afectar al bot.
- Tipos devueltos por /query (restaurant_id como número JSON).
- Calidad del ranking por términos: no hay embeddings.
- Rendimiento con cartas grandes: lectura de 2000 nodos por pregunta.
- Que restaurant_info, "BEBIDAS" y openinghours existan con esas columnas en todos los tenants. Un fallo de cualquier consulta aborta la ingesta de forma explícita.
- go.mod: `golang.org/x/text` se usa ahora de forma directa pero sigue marcado `// indirect`. El build pasa; no se ha tocado go.mod.
