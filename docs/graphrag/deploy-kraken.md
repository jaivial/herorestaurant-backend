# GraphRAG en Kraken — receta de despliegue (una instancia neo4j-rust por restaurante)

Estado: receta, NO aplicada. Probada en desktop el 16-nov con copia del PG de producción (ver `result-qa-20261116.md`).
Nunca imprimas tokens ni el DSN. Todo es loopback y nada se expone por nginx.

## 0. Modelo
- Cada restaurante tiene su propio proceso neo4j-rust, ligado a `127.0.0.1` y con su directorio de datos y su token 0600.
- La correspondencia restaurante → (puerto, token) solo existe en `/home/jaime/graphrag/registry.tsv` (0600).
  - El backend recibe únicamente el `restaurant_id` de la sesión del bot; nunca un puerto ni un token.
  - El backend descarta además cualquier fila cuyo `restaurant_id` no coincida.
- `graphrag.Lookup` exige que el registro y cada token sean ficheros regulares en modo **0600**.
  - **No** comprueba el propietario.
  - Rechaza un restaurante repetido, un puerto compartido entre dos restaurantes y los restaurantes no registrados.
- Binario: neo4j-rust build `d509e4e`, sha256 `ed148c5423890903d583c557bc6966d5603ebdbc5c7062db62fa3a9a8dfcefd8`.

## 1. Estructura de ficheros (usuario jaime)
```
/home/jaime/graphrag/                 0700 jaime
  bin/neo4j-rust                      0755 (copia fija del build d509e4e)
  bin/graph-ingest                    0755 (CGO_ENABLED=0, ver result-qa)
  registry.tsv                        0600  rid<TAB>http_port<TAB>bolt_port<TAB>/home/jaime/graphrag/<rid>/token
  ingest.env                          0600  DB_DRIVER=postgres, PG_DSN=..., VC_GRAPHRAG_REGISTRY=...
  <rid>/token                         0600
  <rid>/ports.env                     0644  HTTP_PORT=...  BOLT_PORT=...
  <rid>/data/                         0700 (WAL + snapshots de neo4j-rust)
```
Puertos: asigna pares consecutivos a partir de 27100 (http par, bolt = http+1). Antes de usarlos, comprueba con `ss -ltn` que están libres. **No** reutilices el puerto ni el directorio de datos del neo4j-rust de memoria (`~/neo4j-rust-data`).

Alta del restaurante `R` (ejemplo para R=1 con http 27100):
```sh
R=1; H=27100; G=/home/jaime/graphrag; umask 077
mkdir -p $G/bin $G/$R/data
head -c 32 /dev/urandom | base64 | tr -d '\n=' > $G/$R/token; chmod 600 $G/$R/token
printf 'HTTP_PORT=%s\nBOLT_PORT=%s\n' $H $((H+1)) > $G/$R/ports.env; chmod 644 $G/$R/ports.env
printf '%s\t%s\t%s\t%s\n' $R $H $((H+1)) $G/$R/token >> $G/registry.tsv; chmod 600 $G/registry.tsv
```

## 2. Unidad systemd plantilla
`/etc/systemd/system/neo4j-rust-graphrag@.service`:
```ini
[Unit]
Description=neo4j-rust GraphRAG restaurante %i (solo loopback)
After=network.target

[Service]
User=jaime
Group=jaime
UMask=0077
EnvironmentFile=/home/jaime/graphrag/%i/ports.env
Environment=NEO4J_RUST_TOKEN_FILE=/home/jaime/graphrag/%i/token
ExecStart=/home/jaime/graphrag/bin/neo4j-rust serve-bolt --data /home/jaime/graphrag/%i/data --bind 127.0.0.1:${BOLT_PORT} --http 127.0.0.1:${HTTP_PORT}
Restart=on-failure
RestartSec=5
MemoryMax=512M
NoNewPrivileges=yes
ProtectSystem=strict
ReadWritePaths=/home/jaime/graphrag/%i/data
PrivateTmp=yes

[Install]
WantedBy=multi-user.target
```
```sh
sudo systemctl daemon-reload
sudo systemctl enable --now neo4j-rust-graphrag@1
ss -ltn | grep -E '127.0.0.1:2710[01] '     # solo 127.0.0.1
```
Al reiniciar la instancia los datos se conservan (WAL); probado en el QA.

## 3. Ingesta (en el host, contra el PG de producción)
`/home/jaime/graphrag/ingest.env` (0600). No lo imprimas:
```
DB_DRIVER=postgres
PG_DSN=host=127.0.0.1 port=55432 dbname=newvillacarmen_business user=<rol de lectura> password=<...> sslmode=disable
VC_GRAPHRAG_REGISTRY=/home/jaime/graphrag/registry.tsv
```
graph-ingest solo lee el PG, en una transacción READ ONLY que se revierte. Si hay un rol de solo lectura, úsalo.
```sh
set -a; . /home/jaime/graphrag/ingest.env; set +a
G=/home/jaime/graphrag/bin/graph-ingest
$G -restaurant-id 1                  # dry-run: registro 0600, bind=loopback, recuentos por kind
$G -restaurant-id 1 -apply -resync   # upsert idempotente + borra lo que ya no está en PG
VC_GRAPHRAG=1 $G -restaurant-id 1 -query "vino tinto"   # lo que recibiría el bot (k=4, timeout 3 s)
```
Usa siempre `-apply -resync`. Con `-apply` solo, los platos o menús desactivados seguirían visibles para el bot hasta el siguiente resync. El restaurante 1 tarda unos 0,03 s (193 nodos).

## 4. Resync periódico
`/etc/systemd/system/graphrag-resync.service`:
```ini
[Unit]
Description=GraphRAG resync de todos los restaurantes del registro
[Service]
Type=oneshot
User=jaime
EnvironmentFile=/home/jaime/graphrag/ingest.env
ExecStart=/bin/sh -c 'for r in $(cut -f1 /home/jaime/graphrag/registry.tsv | grep -E "^[0-9]+$"); do /home/jaime/graphrag/bin/graph-ingest -restaurant-id $r -apply -resync || echo "resync $r FAILED"; done'
```
`/etc/systemd/system/graphrag-resync.timer`:
```ini
[Timer]
OnCalendar=*:0/30
Persistent=true
[Install]
WantedBy=timers.target
```
Cada 30 minutos, porque el horario usa una ventana `current_date … +60 días` y la carta cambia desde el backoffice:
- activar: `sudo systemctl enable --now graphrag-resync.timer`
- ver la última ejecución: `journalctl -u graphrag-resync -n 50`

## 5. Montaje en el contenedor `newvillacarmen-backend-prod` (network host)
- El backend corre como **root** dentro del contenedor: ni `Dockerfile.prod` ni `deploy/pgnative/Dockerfile.overlay` declaran `USER`.
  - Root lee los ficheros 0600 de jaime y `Lookup` no mira el propietario.
  - Verificado en desktop: alpine como root, `--network host`, bind mount `:ro` de un directorio 0700 con un registro y un token 0600 de uid 1000. `Lookup` aceptó, hubo hits del restaurante correcto y un restaurante no registrado dio "not registered".
- Monta `/home/jaime/graphrag` **en la misma ruta** y en solo lectura. Así las rutas de token del registro valen igual en el host (graph-ingest) y en el contenedor (backend):
  ```
  -v /home/jaime/graphrag:/home/jaime/graphrag:ro
  ```
- Antes de activar, comprueba sin cambiar nada que Docker no usa userns-remap ni modo rootless (con remap, el root del contenedor no podría leer los 0600):
  - `docker info --format '{{.SecurityOptions}}'` no debe contener `userns` ni `rootless`;
  - `docker exec newvillacarmen-backend-prod id -u` debe dar `0`.
- Con `--network host`, `127.0.0.1:<puerto>` del contenedor es el loopback del host, así que llega a las instancias.
- **Pendiente obligatorio antes de activar:** los scripts que recrean el contenedor solo conservan el env y `-v $VOLUME:/var/lib/herorestaurant`, y perderían el montaje en el siguiente despliegue:
  - `scripts/cutover/40-switch.sh`
  - `deploy/release-backend-prod.sh`
  - `scripts/deploy-prod-binary.sh`

  Añade el `-v /home/jaime/graphrag:/home/jaime/graphrag:ro` a su `docker run`, o recrea a mano con ese flag. Las variables sí se conservan, porque los scripts copian el env con `docker inspect`.

## 6. Activar / desactivar
Variables del contenedor:
```
VC_GRAPHRAG=1
VC_GRAPHRAG_REGISTRY=/home/jaime/graphrag/registry.tsv
```
`VC_GRAPHRAG_REGISTRY` es obligatoria: dentro del contenedor HOME=/root y el valor por defecto `~/graphrag/registry.tsv` no existe.

- **Activar:**
  1. Copia el env del contenedor actual con `docker inspect` (como hacen los scripts), en un fichero 0600.
  2. Añade las dos variables.
  3. Recrea con el mismo `docker run` de `scripts/deploy-prod-binary.sh` (incluida la espera a un hueco sin envío de WhatsApp) más el `-v` del paso 5.
- **Desactivar:**
  - Recrea igual con `VC_GRAPHRAG=0`, o sin la variable. Con el flag apagado el bot se comporta exactamente como antes; el registro se queda montado y no se consulta.
  - Sin recrear el contenedor: `sudo systemctl stop neo4j-rust-graphrag@<rid>`. Las consultas fallan, el bot recibe 0 snippets y sigue con el RAG actual. Lo único que se ve es `graph_hits=0` en el log.
- **Comprobación tras activar:** en los logs del backend, `wa_bot_graphrag_v1 restaurant_id=<rid> text_len=… graph_hits=N`. Solo se registran recuentos.

## 7. Baja de un restaurante
1. Para y deshabilita la instancia: `sudo systemctl disable --now neo4j-rust-graphrag@R`.
2. Quita su línea del registro (manteniendo el 0600).
3. Mueve `/home/jaime/graphrag/R` aparte.

Las consultas del bot para R dan 0 snippets ("not registered") y el resto de restaurantes no se ve afectado. Probado en el QA.
