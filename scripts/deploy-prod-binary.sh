#!/usr/bin/env bash
# Deploy del BINARIO del backend en prod (Villa Carmen): imagen = BASE + COPY del binario estatico.
# Sin RUN, sin red, sin compilar. Conserva /app/static y el env verbatim.
# BASE = imagen newvillacarmen-prod-backend:privileged (la imagen que FIJA el estatico publicado), NO la imagen del
# contenedor en ejecucion, para que un recreate no pierda assets anadidos al estatico en caliente.
# Uso: BIN=/ruta/backend [FORCE_STATIC=1] bash deploy-prod-binary.sh   (avisar antes: recrear mata los runners de campanas)
set -euo pipefail
export PATH=$PATH:/usr/local/go/bin
NAME=newvillacarmen-backend-prod; OLD=${NAME}-old
BIN=${BIN:?BIN=/ruta/al/binario estatico}
CUR_IMG=$(sudo -n docker inspect $NAME --format '{{.Image}}')
CUR_SHORT=${CUR_IMG#sha256:}; CUR_SHORT=${CUR_SHORT:0:12}
BASE_TAG=newvillacarmen-prod-backend:privileged
BASE_IMG=$(sudo -n docker image inspect "$BASE_TAG" --format '{{.Id}}')
echo "== 0b) guarda de estatico (base $BASE_TAG ${BASE_IMG:7:12} vs contenedor ${CUR_SHORT}) =="
LIVE_IDX=$(sudo -n docker exec $NAME md5sum /app/static/index.html | cut -d' ' -f1)
BASE_IDX=$(sudo -n docker run --rm --entrypoint sh "$BASE_IMG" -c 'md5sum /app/static/index.html' | cut -d' ' -f1)
echo "   index.html vivo=$LIVE_IDX base=$BASE_IDX"
if [ "$LIVE_IDX" != "$BASE_IDX" ] && [ "${FORCE_STATIC:-0}" != 1 ]; then echo "!! el estatico vivo difiere del de la imagen base: fijarlo antes (docker cp static + build FROM + COPY static, tag :privileged) o FORCE_STATIC=1"; exit 1; fi
# todos los assets que referencia index.html deben existir en la base
for a in $(sudo -n docker run --rm --entrypoint sh "$BASE_IMG" -c 'cat /app/static/index.html' | grep -oE '/assets/[^"]+'); do sudo -n docker run --rm --entrypoint sh "$BASE_IMG" -c "test -f /app/static$a" || { echo "!! falta $a en la base"; exit 1; }; done
echo "   estatico base ok"
NEWTAG=newvillacarmen-prod-backend:privileged
RB=newvillacarmen-prod-backend:rollback-$CUR_SHORT
ENVFILE=/tmp/$NAME.env
VOLUME=newvillacarmen-prod_whatsapp-bot-context
echo "== 0) estado y binario =="
file "$BIN" | grep -q "statically linked" || { echo "!! binario no estatico"; exit 1; }
echo "imagen viva: $CUR_SHORT"
echo "== 1) backup DB completo =="
set +e; . /tmp/nvlib.sh; set -e
TS=$(date -u +%Y%m%d-%H%MZ); OUT=$HOME/backups/newvillacarmen-pre-deploy-$TS.sql.gz
MYSQL_PWD="$DB_PASSWORD" mysqldump -h "$DB_HOST" -P "${DB_PORT:-3306}" -u "$DB_USER" --single-transaction --no-tablespaces --routines --ignore-table="$DB_NAME.recent_conversations" "$DB_NAME" 2>/tmp/dump.err | gzip > "$OUT"
zcat "$OUT" | tail -2 | grep -q "Dump completed" || { echo "!! backup incompleto"; cat /tmp/dump.err | head -3; exit 1; }
[ -s "$OUT" ] && echo "   $OUT $(du -h "$OUT"|cut -f1)"
echo "== 2) imagen = actual + COPY binario =="
D=$(mktemp -d /tmp/nvc-img-XXXXXX); cp "$BIN" $D/backend; chmod 755 $D/backend
printf 'FROM %s\nCOPY backend /app/backend\n' "$BASE_IMG" > $D/Dockerfile
sudo -n docker tag "$CUR_IMG" "$RB"
sudo -n docker build -q -t newvillacarmen-prod-backend:deploy-$(date -u +%Y%m%d-%H%M) "$D" >/dev/null
NEW_IMG=$(sudo -n docker image inspect newvillacarmen-prod-backend:deploy-$(date -u +%Y%m%d-%H%M) --format '{{.Id}}')
rm -rf $D
echo "   nueva ${NEW_IMG:7:12} rollback tag $RB"
echo "== 3) env verbatim =="
sudo -n docker inspect "$NAME" | python3 -c 'import json,sys; d=json.load(sys.stdin)[0]; print("\n".join(d["Config"]["Env"]))' | grep -vE '^(PATH|HOME|HOSTNAME)=' > "$ENVFILE"; chmod 600 "$ENVFILE"; echo "   $(wc -l < "$ENVFILE") vars"
echo "== 3b) esperar envio WA c2 nuevo =="
set +e; . /tmp/nvlib.sh; set -e
Q="SELECT COALESCE(MAX(sent_at),0) FROM campaign_recipients WHERE campaign_id=2 AND channel='whatsapp'"
L0=$(my -N -e "$Q" 2>/dev/null)
for i in $(seq 1 400); do L=$(my -N -e "$Q" 2>/dev/null); [ "$L" != "$L0" ] && { echo "   nuevo envio WA $L, recreo ya"; break; }; sleep 1; done
echo "== 4) recrear =="
sudo -n docker stop "$NAME" >/dev/null; sudo -n docker rename "$NAME" "$OLD"
sudo -n docker run -d --name "$NAME" --network host --restart unless-stopped --env-file "$ENVFILE" -v "$VOLUME:/var/lib/herorestaurant" "$NEW_IMG" >/dev/null
rollback(){ echo "!! rollback"; sudo -n docker rm -f "$NAME" >/dev/null 2>&1||true; sudo -n docker rename "$OLD" "$NAME"; sudo -n docker start "$NAME" >/dev/null; exit 1; }
for v in DB_NAME DB_HOST DB_PORT PORT; do sudo -n docker inspect "$NAME" --format '{{range .Config.Env}}{{println .}}{{end}}' | grep -q "^$v=" || rollback; done
ok=0; for i in $(seq 1 60); do curl -fsS http://127.0.0.1:8080/healthz >/dev/null 2>&1 && { ok=1; echo "   health OK ${i}s"; break; }; sleep 1; done
[ "$ok" = 1 ] || rollback
IDXSRC=$(curl -fsS http://127.0.0.1:8080/ | grep -oE '/assets/[^"]+' | head -5)
for a in $IDXSRC; do c=$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:8080$a); [ "$c" = 200 ] || { echo "!! asset $a -> $c"; rollback; }; done
echo "   assets del index sirven 200"
sudo -n docker rm -f "$OLD" >/dev/null 2>&1 || true
sudo -n docker tag "$NEW_IMG" "$NEWTAG"
sudo -n docker ps --filter name=$NAME --format '   {{.Names}} {{.Status}} {{.Image}}'
echo "rollback image: $RB"
