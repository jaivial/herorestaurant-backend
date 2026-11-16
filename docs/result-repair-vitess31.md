# result-repair-vitess31 — GraphRAG multi-tenant: diseño + scaffolding (solo local)

## Qué existe
- docs/graphrag/design.md: amenazas, aislamiento, modelo de datos, inventario, ingesta, retrieval, backup, presupuesto y encaje post-PG.
- docs/graphrag/sources.tsv: 11 fuentes globales (~223 KB).
- scripts/graphrag/tenant-instance.sh: comandos create/start/stop/status/backup por rid.
  - Unidades systemd --user con CPUQuota=200% y MemoryMax=2G.
  - Solo loopback.
  - Token aleatorio en fichero 0600, sin imprimirse.
  - Registry ~/graphrag/registry.tsv (0600).
  - Validado solo con `bash -n`.
- cmd/graph-ingest (Go, solo stdlib):
  - dry-run por defecto
  - rechaza sin rid
  - host fijo 127.0.0.1
  - permisos 0600 obligatorios
  - idempotente por sha256
  - X-Source graph-ingest:<rid>
- Build: `CGO_ENABLED=0 go build -p 2 -trimpath` en el cgroup heredado. Hash: graph-ingest sha256 154111f61a00bb4a539deabd0cc5e25438116a7b5d446cbeb95cd6be7f74450d. go.mod sin cambios.
- No se ha ejecutado nada: ni servidor, ni neo4j-rust, ni systemd, ni DB. No se han tocado ~/neo4j-rust-data, ~/.config/neo4j-rust, Kraken ni la config MCP.

## Qué NO está validado
- Que el subset Cypher de neo4j-rust acepte los patrones usados:
  - DELETE de rel y nodo en una sola sentencia
  - CREATE de rel desde MATCH
  - propiedades en el patrón de MATCH
  - CONTAINS en el retrieval
- Que serve-bolt con --http arranque bajo las directivas systemd elegidas.
- La retrieval del backend (fase 2) no está implementada ni cableada. Tampoco la ingesta de las fuentes tenant de PG.
- El RSS y el disco reales.

## QA manual (más adelante, local y aislado, con aprobación)
1. `scripts/graphrag/tenant-instance.sh create 901` y `create 902` (rids de prueba), luego `start` ambos. `status` debe mostrar active, y `ss -ltn` solo 127.0.0.1:21802/21803 y 21804/21805.
2. Para cada instancia, `curl -s http://127.0.0.1:<port>/health` → ok, y sin token /stats → 401.
3. Ingestar datos distintos:
   - `graph-ingest -restaurant-id 901 -apply` con el inventario normal.
   - Para 902, un inventario alternativo con un único .md marcador: `-inventory /tmp/inv902.tsv`.
   - Repetir ambos y ver unchanged = sources (idempotencia).
4. Aislamiento por token: con el token de 901 (leído por el shell en una variable, sin imprimirlo), hacer POST /query a 21804 → 401.
5. Datos: en 901, `MATCH (c:Chunk) RETURN count(c)` no debe incluir el marcador de 902; y al revés.
6. Intentos forjados con graph-ingest:
   - `-restaurant-id 0` → rechazo.
   - rid no registrado → rechazo.
   - Editar una copia del registry con otro puerto pero modo 0644 → rechazo.
   - Fichero de token 0644 → rechazo.
7. Named graph forjado: POST /graphs/otro/query en 21802 solo ve la instancia 901, nunca 902. Es la prueba de que la frontera es el proceso.
8. Backup: `tenant-instance.sh backup 901` → existe el tar, el token no está dentro (`tar tzf`) y el servicio vuelve a active.
9. Cuando exista la retrieval del backend: a través del endpoint del bot del tenant A, con un prompt que pida datos "del restaurante 902" o un puerto o graph concreto, solo deben salir chunks con restaurant_id 901. Revisar el log graphrag_tenant_mismatch.
10. Limpieza: stop, borrar unidades y ~/graphrag/90x. Es manual y deliberado.
