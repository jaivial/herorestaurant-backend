#!/usr/bin/env bash
# Release del backend de NewVillaCarmen a produccion, con gate.
#
# Un despliegue en Docker son dos pasos separados: construir la imagen y
# aplicarla. Casi todo el riesgo vive en que se separen. Este script hace
# las dos cosas en el mismo paso, desde un contexto cerrado, y con un gate
# que comprueba el CONTENIDO de la imagen antes de tocar el contenedor.
#
# CONTEXTO MULTI-REPO. El Dockerfile.prod del backend hornea tambien el
# frontend publico, asi que el contexto de build tiene que ser la raiz
# multi-repo (backend/ + preactvillacarmen/frontend/ + ...), no el repo del
# backend solo. Por eso el script arma un arbol de stage con:
#
#   stage/backend/            <- este repo, en el ref pedido
#   stage/preactvillacarmen/  <- el repo del frontend publico, a su HEAD
#
# El frontend publico va por su cuenta: su contenido esta horneado en la
# imagen, asi que un cambio suyo exige un redeploy del backend.
#
# Que hace, en orden:
#   1) arma un contexto cerrado: este repo en el ref exacto + el frontend
#      publico a su HEAD (un arbol que se mueve produce una imagen que no
#      corresponde a lo que crees);
#   2) construye y mira el RETURNCODE, no la salida (los errores de build
#      van a stderr);
#   3) gate de contenido sobre la imagen: los sentinels tienen que estar
#      dentro, o no se despliega;
#   4) compara el tag con el contenedor vivo, y avisa si no coinciden (un
#      build tardio re-etiqueta produccion con un artefacto viejo);
#   5) captura el env del contenedor vivo VERBATIM;
#   6) deja la imagen rodante como rollback;
#   7) recrea con la MISMA especificacion y espera el health;
#   8) si el nuevo no responde, vuelve atras sin reconstruir.
#
# Uso:
#   deploy/release-backend-prod.sh [ref]
#
# El ref por defecto es origin/main. Para un PR se pasa la rama:
#   deploy/release-backend-prod.sh origin/feat/mi-cambio
set -euo pipefail

# ------------------------------------------------------------------ ajustar
IMAGE="newvillacarmen-prod-backend:privileged"
NAME="newvillacarmen-backend-prod"
OLD="${NAME}-old"
ENVFILE="/tmp/${NAME}.env"
ROLLBACK_TAG="newvillacarmen-prod-backend:rollback"
VOLUME="newvillacarmen-prod_whatsapp-bot-context"

# La raiz multi-repo: ahi viven backend/, preactvillacarmen/ y backoffice/.
MULTI_REPO="${NVC_MULTI_REPO:-/var/www/newvillacarmen}"

# Sentinels de contenido: simbolos que SOLO existen si el build llevo el fix.
# El commit NO sirve aqui salvo que el Dockerfile lo hornee con -ldflags, y
# este Dockerfile no lo hace: un check de commit daria siempre falso positivo.
# Son los MENSAJES de error de los guards, no los nombres de las funciones:
# el nombre de una funcion sigue en el binario aunque le cambies una letra
# (grep de un prefijo casa igual), pero su mensaje de error solo esta si el
# guard esta. Un sentinel que un rename no puede borrar es el que de verdad
# prueba algo.
REQUIRED_STRINGS=(
  "operaciones sobre la cuenta de la sesion"
  "gestion de cuentas del panel"
  "es una operacion de gestion de cuentas"
)
PROBE_CMD="strings /app/backend | grep -q"
HEALTH_CMD="curl -fsS http://127.0.0.1:8080/api/health"
# -------------------------------------------------------------------------

REF="${1:-origin/main}"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
STAGE="$(mktemp -d /tmp/nvc-release-XXXXXX)"
trap 'rm -rf "$STAGE"' EXIT

echo "== 1) contexto cerrado =="
git -C "$REPO_ROOT" fetch origin --quiet
if ! git -C "$REPO_ROOT" rev-parse --verify "$REF" >/dev/null 2>&1; then
  echo "!! el ref $REF no existe"
  exit 1
fi
COMMIT=$(git -C "$REPO_ROOT" rev-parse "$REF")
echo "   backend: $REF -> ${COMMIT:0:12}"

mkdir -p "$STAGE/backend"
git -C "$REPO_ROOT" archive "$COMMIT" | tar -x -C "$STAGE/backend"

# El frontend publico, que el Dockerfile tambien hornea. Va a su HEAD: su
# contenido acaba dentro de la imagen, asi que un cambio suyo exige un
# redeploy del backend.
FE_DIR="$MULTI_REPO/preactvillacarmen"
if [ -d "$FE_DIR/.git" ]; then
  FE_COMMIT=$(git -C "$FE_DIR" rev-parse --short HEAD)
  echo "   frontend: $FE_DIR @ ${FE_COMMIT}"
  mkdir -p "$STAGE/preactvillacarmen"
  # Sin .git y sin node_modules: el contexto de build no los quiere.
  tar -C "$FE_DIR" --exclude=.git --exclude=node_modules --exclude=dist -cf - . \
    | tar -x -C "$STAGE/preactvillacarmen"
else
  echo "   AVISO: no se encuentra el frontend publico en $FE_DIR"
  echo "   (la imagen saldra sin el frontend publico)"
fi

echo "== 2) construir =="
# El returncode es lo que decide, no la salida: los errores de docker build
# van a stderr.
if ! sudo -n docker build -t "$IMAGE" -f "$STAGE/backend/Dockerfile.prod" "$STAGE" >/tmp/nvc-build.log 2>&1; then
  echo "!! el build fallo:"
  tail -20 /tmp/nvc-build.log | sed 's/^/   /'
  exit 1
fi
echo "   $(sudo -n docker image inspect "$IMAGE" --format '{{.Id}}' | cut -c1-12)"

echo "== 3) gate: el contenido de la imagen, antes de tocar nada =="
IMAGE_ID=$(sudo -n docker image inspect "$IMAGE" --format "{{.Id}}")
echo "   $IMAGE -> ${IMAGE_ID:0:12}"
MISSING=""
for s in "${REQUIRED_STRINGS[@]}"; do
  if ! sudo -n docker run --rm --entrypoint sh "$IMAGE" -c "$PROBE_CMD '$s'" >/dev/null 2>&1; then
    MISSING="$MISSING $s"
  fi
done
if [ -n "$MISSING" ]; then
  echo "!! la imagen ${IMAGE_ID:0:12} NO contiene:$MISSING"
  echo "   (construida desde un arbol viejo; no se despliega)"
  exit 1
fi
echo "   los sentinels estan en la imagen"

echo "== 4) el contenedor vivo es la verdad, no el tag =="
RUNNING_ID=$(sudo -n docker inspect "$NAME" --format "{{.Image}}" 2>/dev/null || echo "")
if [ -n "$RUNNING_ID" ] && [ "$RUNNING_ID" != "$IMAGE_ID" ]; then
  echo "   AVISO: el tag ${IMAGE_ID:0:12} no es la imagen que corre ${RUNNING_ID:0:12}"
  echo "   (un build tardio re-etiqueto produccion; se corrige al final)"
fi

echo "== 5) capturar el env del contenedor actual (verbatim) =="
# Sin filtrar por prefijo: filtrar es la forma facil de perder justo las
# variables criticas de base de datos, y la ausencia de esas variables no
# suele fallar: la aplicacion conecta con sus defaults.
sudo -n docker inspect "$NAME" | python3 -c '
import json,sys
d=json.load(sys.stdin)[0]
print("\n".join(d["Config"]["Env"]))
' | grep -vE '^(PATH|HOME|HOSTNAME)=' > "$ENVFILE"
chmod 600 "$ENVFILE"
echo "   $(wc -l < "$ENVFILE") variables -> $ENVFILE"

echo "== 6) dejar la imagen rodante como rollback =="
sudo -n docker tag "$RUNNING_ID" "$ROLLBACK_TAG"
echo "   la imagen rodante queda como $ROLLBACK_TAG"

echo "== 7) parar y renombrar el actual =="
sudo -n docker stop "$NAME" >/dev/null
sudo -n docker rename "$NAME" "$OLD"

echo "== 8) recrear con la misma especificacion =="
sudo -n docker run -d \
  --name "$NAME" \
  --network host \
  --restart unless-stopped \
  --env-file "$ENVFILE" \
  -v "$VOLUME:/var/lib/herorestaurant" \
  "$IMAGE" >/dev/null

echo "== 9) verificar el env critico del contenedor NUEVO =="
for v in DB_NAME DB_HOST DB_PORT PORT; do
  got=$(sudo -n docker inspect "$NAME" --format '{{range .Config.Env}}{{println .}}{{end}}' | grep "^$v=" | head -1)
  if [ -z "$got" ]; then
    echo "!! el contenedor nuevo no tiene $v: se restaura el anterior"
    sudo -n docker rm -f "$NAME" >/dev/null 2>&1 || true
    sudo -n docker rename "$OLD" "$NAME"
    sudo -n docker start "$NAME" >/dev/null
    exit 1
  fi
  echo "   $v ok"
done

echo "== 10) esperar el health =="
ok=0
for i in $(seq 1 60); do
  if eval "$HEALTH_CMD" >/dev/null 2>&1; then
    echo "   health OK en ${i}s"
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

echo "== 11) limpiar y dejar el tag en la verdad =="
sudo -n docker rm -f "$OLD" >/dev/null 2>&1 || true
# El tag tiene que apuntar a la imagen que corre. Si un build tardio lo movio,
# se corrige aqui.
sudo -n docker tag "$(sudo -n docker inspect "$NAME" --format '{{.Image}}')" "$IMAGE"

echo "== estado =="
sudo -n docker ps --filter "name=$NAME" --format "   {{.Names}}  {{.Status}}  {{.Image}}"
echo "   rollback: $ROLLBACK_TAG"
