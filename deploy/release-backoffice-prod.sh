#!/usr/bin/env bash
# Release del backoffice de NewVillaCarmen a produccion, con gate.
#
# Mismo principio que release-backend-prod.sh: un despliegue son dos pasos
# (construir y aplicar) y el riesgo esta en que se separen. Aqui el riesgo
# concreto es que un build tardio re-etiquete el tag de produccion con un
# bundle viejo, y el siguiente despliegue instale un panel sin la seccion de
# conversaciones privilegiadas.
#
# Uso:
#   deploy/release-backoffice-prod.sh [ref]
set -euo pipefail

# ------------------------------------------------------------------ ajustar
IMAGE="newvillacarmen-prod-backoffice:privileged"
NAME="newvillacarmen-backoffice-prod"
OLD="${NAME}-old"
ENVFILE="/tmp/${NAME}.env"
ROLLBACK_TAG="newvillacarmen-prod-backoffice:rollback"

# Sentinels del bundle: el texto de la seccion y el endpoint que la consume.
REQUIRED_STRINGS=(
  "Conversaciones privilegiadas"
  "privileged-contacts"
)
PROBE_CMD="grep -rq"
HEALTH_CMD="curl -fsS -m 5 http://127.0.0.1:3011/"
# -------------------------------------------------------------------------

# El backoffice es otro repo (jaivial/backofficereact), no este. Su ruta se
# puede pasar por entorno; por defecto, el checkout de produccion.
REF="${1:-origin/dev}"
BACKOFFICE_REPO="${NVC_BACKOFFICE_REPO:-/var/www/newvillacarmen/backoffice}"
STAGE="$(mktemp -d /tmp/nvc-bo-release-XXXXXX)"
trap 'rm -rf "$STAGE"' EXIT

echo "== 1) contexto cerrado: exportar $REF =="
git -C "$BACKOFFICE_REPO" fetch origin --quiet
if ! git -C "$BACKOFFICE_REPO" rev-parse --verify "$REF" >/dev/null 2>&1; then
  echo "!! el ref $REF no existe"
  exit 1
fi
COMMIT=$(git -C "$BACKOFFICE_REPO" rev-parse "$REF")
echo "   $REF -> ${COMMIT:0:12}"
mkdir -p "$STAGE/backoffice"
git -C "$BACKOFFICE_REPO" archive "$COMMIT" | tar -x -C "$STAGE/backoffice"
echo "   (el Dockerfile hace COPY . ., asi que el contexto es el repo entero)"

echo "== 2) construir =="
if ! sudo -n docker build -t "$IMAGE" -f "$STAGE/backoffice/Dockerfile.prod" "$STAGE/backoffice" >/tmp/nvc-bo-build.log 2>&1; then
  echo "!! el build fallo:"
  tail -20 /tmp/nvc-bo-build.log | sed 's/^/   /'
  exit 1
fi
echo "   $(sudo -n docker image inspect "$IMAGE" --format '{{.Id}}' | cut -c1-12)"

echo "== 3) gate: el contenido del bundle, antes de tocar nada =="
IMAGE_ID=$(sudo -n docker image inspect "$IMAGE" --format "{{.Id}}")
echo "   $IMAGE -> ${IMAGE_ID:0:12}"
MISSING=""
for s in "${REQUIRED_STRINGS[@]}"; do
  if ! sudo -n docker run --rm --entrypoint sh "$IMAGE" -c \
        "$PROBE_CMD '$s' /app/dist" >/dev/null 2>&1; then
    MISSING="$MISSING $s"
  fi
done
if [ -n "$MISSING" ]; then
  echo "!! la imagen ${IMAGE_ID:0:12} NO contiene:$MISSING"
  echo "   (construida desde un arbol viejo; no se despliega)"
  exit 1
fi
echo "   la seccion de conversaciones privilegiadas esta en la imagen"

echo "== 4) el contenedor vivo es la verdad, no el tag =="
RUNNING_ID=$(sudo -n docker inspect "$NAME" --format "{{.Image}}" 2>/dev/null || echo "")
if [ -n "$RUNNING_ID" ] && [ "$RUNNING_ID" != "$IMAGE_ID" ]; then
  echo "   AVISO: el tag ${IMAGE_ID:0:12} no es la imagen que corre ${RUNNING_ID:0:12}"
fi

echo "== 5) capturar el env del contenedor actual (verbatim) =="
sudo -n docker inspect "$NAME" | python3 -c '
import json,sys
d=json.load(sys.stdin)[0]
print("\n".join(d["Config"]["Env"]))
' | grep -vE '^(PATH|HOME|HOSTNAME)=' > "$ENVFILE"
chmod 600 "$ENVFILE"
echo "   $(wc -l < "$ENVFILE") variables -> $ENVFILE"

echo "== 6) dejar la imagen rodante como rollback =="
sudo -n docker tag "$RUNNING_ID" "$ROLLBACK_TAG"

echo "== 7) parar y renombrar el actual =="
sudo -n docker stop "$NAME" >/dev/null
sudo -n docker rename "$NAME" "$OLD"

echo "== 8) recrear con la misma especificacion =="
sudo -n docker run -d \
  --name "$NAME" \
  --network host \
  --restart unless-stopped \
  --env-file "$ENVFILE" \
  "$IMAGE" >/dev/null

echo "== 9) esperar a que responda =="
ok=0
for i in $(seq 1 60); do
  if eval "$HEALTH_CMD" >/dev/null 2>&1; then
    echo "   responde en ${i}s"
    ok=1
    break
  fi
  sleep 1
done
if [ "$ok" != "1" ]; then
  echo "!! el contenedor nuevo no responde: se restaura el anterior"
  sudo -n docker rm -f "$NAME" >/dev/null 2>&1 || true
  sudo -n docker rename "$OLD" "$NAME"
  sudo -n docker start "$NAME" >/dev/null
  exit 1
fi

echo "== 10) limpiar y dejar el tag en la verdad =="
sudo -n docker rm -f "$OLD" >/dev/null 2>&1 || true
sudo -n docker tag "$(sudo -n docker inspect "$NAME" --format '{{.Image}}')" "$IMAGE"

echo "== estado =="
sudo -n docker ps --filter "name=$NAME" --format "   {{.Names}}  {{.Status}}  {{.Image}}"
echo "   rollback: $ROLLBACK_TAG"
