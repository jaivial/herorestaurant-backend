# GraphRAG QA manual — restaurantes 901 / 902 (scratch padnat1, solo loopback)

Ejecutada el 16-nov en desktop (resultado: docs/graphrag/result-qa-20261116.md). Lo ejecuta el coordinador dentro del contenedor scratch padnat1 (network none), sin producción, sin kraken y sin tocar ~/neo4j-rust-data ni su token. Nunca imprimas tokens ni DSN.

## 0. Binarios dentro del contenedor
Copiar al contenedor (`docker cp`, en /tmp/qa-graph/):
- `~/neo4j-rust/target/release/neo4j-rust`
- `/tmp/villacarmen-bot-20261111/graph-ingest`

## 1. Datos de prueba en el scratch (psql, COMMIT solo en el scratch)
```sql
BEGIN;
INSERT INTO restaurants (id, slug, name, location) VALUES (901, 'qa-graph-901', 'QA Graph Norte', 'Calle Norte 1'), (902, 'qa-graph-902', 'QA Graph Sur', 'Calle Sur 2');
INSERT INTO comida_categories (restaurant_id, food_type, name, slug, active) VALUES (901, 'platos', 'QA Norte Arroces', 'qa-norte-arroces', 1), (902, 'platos', 'QA Sur Carnes', 'qa-sur-carnes', 1);
INSERT INTO comida_items (restaurant_id, source_type, nombre, precio, descripcion, active) VALUES
  (901, 'platos', 'Paella Zafiro Norte', 18.50, 'arroz con bogavante azul', 1),
  (902, 'platos', 'Chuletón Rubí Sur', 32.00, 'carne madurada 45 días', 1);
INSERT INTO group_menu_wines_v2 (restaurant_id, tipo, nombre, precio, bodega, active) VALUES
  (901, 'blanco', 'Albariño Zafiro', 21, 'Bodega Norte', 1),
  (902, 'tinto', 'Ribera Rubí', 27, 'Bodega Sur', 1);
COMMIT;
```
Los nombres distinguibles son "Zafiro" en 901 y "Rubí" en 902. Si alguna tabla tiene otros NOT NULL en el scratch, ajusta los valores; no inventes otros datos.

## 2. Dos instancias, cada una con su token y sus datos
```sh
cd /tmp/qa-graph; umask 077
for r in 901 902; do
  mkdir -p g$r/data; head -c 32 /dev/urandom | base64 | tr -d '\n=' > g$r/token; chmod 600 g$r/token
  http=$((20000 + r*2)); bolt=$((http+1))
  NEO4J_RUST_TOKEN_FILE=$PWD/g$r/token ./neo4j-rust serve-bolt --data $PWD/g$r/data --bind 127.0.0.1:$bolt --http 127.0.0.1:$http > g$r/log 2>&1 &
  printf '%s\t%s\t%s\t%s\n' $r $http $bolt $PWD/g$r/token >> registry.tsv
done
chmod 600 registry.tsv
```

## 3. Ingesta
```sh
export DB_DRIVER=postgres PG_DSN='host=/var/run/postgresql dbname=<scratch> user=<ro user>' VC_GRAPHRAG_REGISTRY=/tmp/qa-graph/registry.tsv
./graph-ingest -restaurant-id 901          # dry-run: registry ok, 0600, bind=loopback, recuentos por kind
./graph-ingest -restaurant-id 901 -apply
./graph-ingest -restaurant-id 902 -apply
./graph-ingest -restaurant-id 901 -apply   # idempotente: created=0 updated=0
```

## 4. Aislamiento
```sh
export VC_GRAPHRAG=1
./graph-ingest -restaurant-id 901 -query "paella zafiro"   # >=1 hit, todos uid restaurant:901:*
./graph-ingest -restaurant-id 901 -query "chuleton rubi"   # hits=0 (datos de 902 invisibles)
./graph-ingest -restaurant-id 902 -query "albariño zafiro" # hits=0
./graph-ingest -restaurant-id 902 -query "ribera rubi"     # >=1 hit restaurant:902:*
```
- **Token de 901 contra el puerto de 902** → 401. Leer el token en una variable sin imprimirla:
  `T=$(cat g901/token); wget -q -S -O - --header "Authorization: Bearer $T" --header 'Content-Type: application/json' --post-data '{"cypher":"MATCH (n) RETURN count(n)","params":{}}' http://127.0.0.1:21804/query 2>&1 | head -1`
- **Registro manipulado:** 903 inexistente, o registry/token en 0644 → graph-ingest y Query rechazan; con `-query` sale hits=0.
- **Puerto compartido:** dos líneas del registro con el mismo puerto → "port shared" y rechazo.

## 5. Reinicio, resync y borrado
- **Reinicio:** matar los dos procesos y relanzarlos con el mismo comando. Las consultas del paso 4 dan el mismo resultado (persistencia WAL).
- **Resync:** `UPDATE comida_items SET active=0 WHERE restaurant_id=901 AND nombre='Paella Zafiro Norte';` y después `./graph-ingest -restaurant-id 901 -apply -resync` → deleted=1. La query "paella" en 901 da hits=0 ("paella zafiro" sigue dando el Albariño Zafiro por "zafiro", es correcto). 902 sin cambios.
- **Borrado de una instancia:**
  1. parar g902;
  2. mover g902/data a g902/data.qa-removed;
  3. quitar su línea del registro.

  Resultado esperado: 901 intacto (paso 4 igual); `-query` sobre 902 da hits=0 ("not registered").

## 6. Limpieza (solo scratch)
`DELETE` de las filas 901/902 en las cuatro tablas, parar los procesos y mover /tmp/qa-graph aparte. Nada fuera del contenedor.
