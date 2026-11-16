# NewVillaCarmen: resumen de cutover MySQL -> PostgreSQL (propuesta, no ejecutada)

Estado 13 Nov 11:25. Nada de esto se ejecuta sin el GO explícito de Jaime, paso a paso. Producción (Kraken) intacta y solo lectura.

## Qué está validado (en scratch aislado, sin red)
- Importación completa 253 tablas hash-verificada; claves de colación (`__wk`), índices pad, DDL temporal, triggers ON UPDATE: gate PASS (excepto shadow FK por 731 huérfanos).
- QA manual HTTP con flag ON: ~30 escritores (categorías, mesas, áreas, proveedores, alias, permisos, POS visita/ticket/checkout/fiscal, clientes, import de categorías, import de stock multipart, ajustes de plataforma). Concurrencia: 8 altas iguales -> 1 OK + 7 duplicados; 8 tickets simultáneos -> 8 números distintos. Gate PASS tras cada una.
- QA encontró 4 bugs reales que el análisis de texto no vio (clave NULL en vacío, 16 escritores sin cablear, clave fiscal mal calculada, regex sobre columna ci_ai). Arreglados. Moraleja: lo no ejercitado no está validado.

## Qué NO está validado
- Bot de WhatsApp end-to-end (prohibido salvo al número de prueba); workers (recordatorios, pre-shift, outbox); pagos/Stripe/proveedores; claves IA (necesita VAULT_KEY).
- Rutas de lectura masiva y búsquedas aproximadas (~29 lookups aproximados, REGEXP aproximado, full-text/acentos).
- Escritores sin cablear: `backoffice_stock_documents.go:680` (bug original, decidido no tocar), `backfill-translations:386`, `comida-stock-backfill:229/240`.
- Rango unsigned, `campaign_email_guard.go:329`, paquete `hack` (unsafe) en la copia de Vitess.
- Bloqueo de índices: `ix__bot_inbound_queue__uq_inbound_msg` no se puede retirar (sin gemelo `__wk`).
- Huérfanos: 731 filas (comments 1, invoice_line_items 726, mappings 3 + page 1). Nunca borradas; sin FK sombra para ellas.
- El servidor rechaza PostgreSQL en ejecución normal (`cmd/server/main.go:34`); un arranque real exige quitar esa barrera = cambio de código + decisión de Jaime.

## Pasos propuestos (cada uno con GO)
1. Congelar versión: merge del worktree a una rama de release (no a main) y build con límites.
2. Backup: dump MySQL de Kraken (solo lectura) + sha256; copia de seguridad del dump anterior.
3. Importar el dump final en PG de desktop y aplicar DDL, ixpad, `__wk`, backfill; gate con PADQA_WK=1. Esperado: solo shadow FK por huérfanos.
4. Retirar índices ci_ai crudos (116) con el SQL de vitess28; conservar 4 enum + 1 bloqueado.
5. Arrancar servidor PG en loopback con workers y WhatsApp apagados; QA manual repetida; luego bot solo contra +34692747052.
6. Decidir ventana: parar escrituras en Kraken (modo mantenimiento), re-importar delta/dump final, comprobar conteos y hashes.
7. Conmutar tráfico al servidor PG de desktop (DNS/tunel: decisión de Jaime sobre dónde corre).
8. Vigilancia 24 h: gate periódico, logs, conteos.

## Rollback
- Antes del paso 7: se apaga el PG de desktop; Kraken sigue siendo la fuente. Coste cero.
- Después del paso 7: volver al servidor MySQL de Kraken exige re-sincronizar lo escrito en PG durante la ventana (reservas, tickets, mensajes). No existe sincronización inversa automática. Mitigación: ventana corta, exportar escrituras PG->MySQL manualmente o aceptar pérdida solo con GO. Recomendación: no conmutar hasta tener un plan de reversa o una ventana de solo lectura.

## Riesgos principales
1. Escritura perdida en rollback (arriba).
2. Escritores no ejercitados (bot, workers, pagos) fallando en caliente.
3. Búsquedas aproximadas que cambian resultados (lookups, REGEXP, full-text).
4. Huérfanos: facturas sin padre; la migración los conserva pero no los repara.
5. Dependencia de una copia vendorizada de Vitess (mantenimiento, `unsafe`).
6. Desktop como host: disco al 92 % (79 GB libres), una sola máquina, sin redundancia.

## 731 huérfanos: recomendación
Conservar tal cual, sin FK sombra para esas relaciones, y registrarlos en un anexo. Solo si Jaime quiere repararlos: recuperar los padres desde `~/newvillacarmen/.dev-dump.sql.gz` (requiere su aprobación) y verificar que corresponden por clave. Nunca inventar padres.

## Licencia GPL de las tablas de pesos: recomendación
La copia vendorizada es de Vitess (Apache-2.0, avisos de modificación conservados). La duda es de procedencia: las tablas de pesos de colación reproducen las de MySQL (GPLv2 con FOSS exception) aunque Vitess las distribuye bajo Apache-2.0. Recomendación (no es asesoría legal): uso interno en servidores propios no implica distribución, por lo que el riesgo práctico es bajo; mantener LICENSE y avisos, no redistribuir el código ni las tablas a terceros ni publicarlo, y documentar la procedencia. Si en algún momento se publica el código o se entrega a clientes, pedir revisión legal o regenerar las tablas desde datos Unicode/UCA públicos. Necesita su respuesta antes de cualquier despliegue.

## Decisiones que necesita de Jaime
1. Licencia (arriba).  2. Plan de huérfanos.  3. Quitar la barrera `main.go:34` y dónde corre el servidor.  4. Arreglar o no el bug de stock `:680` (PR aparte).  5. Estrategia de rollback/ventana.  6. Alcance de la QA del bot con WhatsApp al número de prueba.
