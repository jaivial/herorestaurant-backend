# Campañas: filtros de destinatarios y continuación de envíos

Coordination id: `camp` (backend), `camp-resume_v1` (endpoint de reanudación).
Estado: filtros corregidos y continuación preparada. **Envíos siguen PARADOS por
decisión de producto: el consentimiento promocional no está resuelto** (ver
"Qué falta" al final). Ningún envío se ha reanudado ni se reanudará sin
decisión humana explícita.

## Qué filtros existían ya (y se conservan)

Selección de audiencia (`campaignAudience`, backoffice_campaigns.go):

| Filtro | Mecanismo | Excluye por canal |
| --- | --- | --- |
| Bajas manuales (unsubscribe público, bot WhatsApp, legacy) | `campaign_suppressions` source `unsubscribe`/`legacy`; una baja cubre ambos canales (las ramas del UNION expanden email+teléfono vía `bookings`) | ambos |
| Bounces verificados (hard NDR Titan / rechazo SMTP 5.1.x) | `campaign_suppressions` source `bounce`/`invalid` + `invalid_emails` legacy | solo email |
| Bounces no concluyentes (soft) | source `pending_verify` (bloqueado hasta decisión, nunca marcado inválido) | solo email |
| Teléfonos inválidos | `invalid_phones` legacy | solo whatsapp |
| Opt-outs legacy `no_marketing` | tabla legacy | ambos |
| Emails imposibles (sintaxis, errata de dominio, desechables, NXDOMAIN) | `campaignEmailLooksDeliverable` (syntax + listas + MX/A con DNS definitivo; timeout/SERVFAIL nunca tira un buen contacto) | solo email |
| Dedupe | `uq_campaign_target (campaign_id, channel, target)` + `INSERT IGNORE`: un contacto ya en el ledger (enviado, pendiente, pausado, fallido, saltado) nunca se vuelve a encolar | — |

Además `runCampaignSend` relee la lista de do-not-contact **antes de cada
entrega**: quien se da de baja durante un envío largo nunca recibe el mensaje.

## Qué fallaba y queda corregido en este PR

1. **Las filas `paused` eran un callejón sin salida** (la causa real del estado
   "84 enviados / resto parado"): el breaker de rebotes pausa filas en
   `campaign_recipients.status='paused'`, pero no existía ninguna vía para
   devolverlas a `pending`. `POST /campanas/{id}/send` solo añade audiencia
   nueva (no-op sobre filas existentes) y el runner solo drena `pending`.
   → **Corregido**: nuevo endpoint `POST /campanas/{campaignId}/resume`
   (backoffice, misma auth que envío): re-pon `paused → pending` tras
   re-comprobar la lista de bajas (los ahora suprimidos pasan a `skipped`,
   nunca a `pending`), resetea el breaker en memoria (siguiente poll
   re-baselinea) y **arranca `runCampaignSend` en ese momento** (no en el
   arranque del servidor). **Requiere `ack` obligatorio** con la causa
   resuelta, que se registra en el log (`campaign.resume.requested`,
   coord_id `camp-<uuid>`): deja rastro auditable de la decisión del
   operador. No toca `sent`/`failed`/`skipped`. No ha sido invocado.
2. **Estado final que mentía**: al terminar el runner la campaña pasaba a
   `sent` aunque hubiera filas pausadas. → **Corregido** (combinado con la
   semántica ya revisada en `dev` PR #539): si al salir quedan `paused` o
   `pending`, la campaña **nunca** pasa a `sent`; un corte por rebotes la
   devuelve a `draft` (reanudable) y cualquier otro residual la deja en
   `sending` (reanudable); la lectura de stats es guard: si falla no se
   escribe ningún estado (log `campaign.send.stats_failed`) y cualquier
   interrupción se loguea con `campaign.send.interrupted`.
3. **La previsualización de audiencia no distinguía ya-enviados**: el
   operador veía el total del audiencia, no el trabajo real restante. →
   **Corregido SIN romper consumidores**: los campos históricos conservan su
   semántica original (`total`, `emails`, `whatsapp`, `sample` = audiencia
   completa; es lo que consumen `CampaignAudiencePreview` y los contadores/ETA
   del editor) y se **AÑADEN** campos explícitos: `new` (+ `new_emails`,
   `new_whatsapp`, `new_sample`) = los únicos que un envío añadiría,
   `already_in_ledger` = los ya en el ledger y `stats` = ledger por estados.
4. **`POST /send` reportaba `queued = total audiencia`** mezclando realidad e
   intención. → **Corregido sin redefinir `queued`**: `queued` conserva el
   valor histórico (audiencia solicitada, es lo que lee el toast del BO) y se
   añaden `queued_new` (filas realmente insertadas, RowsAffected del INSERT
   IGNORE), `already` (ya presentes en el ledger) e `insert_errors` (rechazos
   de la BD, con warn `campaign.send.insert_failed`).
5. **Un error transitorio al leer supresiones quemaba el destinatario** (la
   fila pasaba a `failed` permanente con "no se pudo comprobar la lista de
   bajas"). → **Corregido**: la fila se queda `pending`, se respeta 5 s y
   tras 3 fallos consecutivos se aborta el run dejando la campaña
   reanudable (log `campaign.delivery.suppression_read_failed`).
6. **Emails de la audiencia sin normalizar**: se guardaban con la caja del
   origen y cada punto del circuito comparaba distinto (ledger `ToLower+Trim`,
   comparación solo `ToLower`, insert sin `Trim`). → **Corregido con una única
   fuente**: `campaignCanonicalTarget(channel, target)` (email =
   `LOWER(TRIM(x))` según contrato de la migración 130; WhatsApp =
   `normalizeWhatsAppNumber`) es ahora la normalización usada por el
   constructor de audiencia, la clave del ledger, la comparación de supresión
   (`blocked`) y la carga del filtro: dos representaciones del mismo contacto
   no pueden divergir.
7. **Inveribilidad**: `campaignStats` no exponía `paused`/`skipped` y el
   ledger no permitía filtrar por esos estados. → **Corregido**: stats
   aditivas `paused` + `skipped`; `GET /campanas/{id}/recipients?status=`
   acepta también `paused` y `skipped` (los tipos del BO, que solo declaran
   `pending|sent|failed`, siguen siendo válidos: los campos son aditivos).

## Continuación: quién drena qué (`/send` vs `/resume`) — pending residual

El ledger de `campaign_recipients` distingue `pending` (por drenar) y
`paused` (detenidos por el breaker de rebotes). Cada estado tiene su puerta
de entrada y **ninguna se autoejecuta**:

| Estado residual | Quién lo drena | Efecto |
| --- | --- | --- |
| `pending` (creados por `/send`, o residuales tras un run abortado por errores de lectura de supresiones) | `POST /send` (vuelve a calcular audiencia, INSERT IGNORE solo añade filas nuevas y drena todas las `pending`), o un runner vivo que re-lee `pending` por lote | Envío al ritmo configurado, con re-chequeo de bajas por entrega |
| `paused` (corte por rebotes u otra pausa futura) | **Solo** `POST /resume`: re-pon `paused → pending` (los ahora suprimidos → `skipped`), resetea el breaker y **arranca `runCampaignSend` en ese momento** (no al arrancar el servidor: ningún arranque de proceso envía nada por sí solo) | Reanudación tras decisión del operador con `ack` auditable |

Puntos clave del pending residual:

- Un run interrumpido (3 fallos consecutivos leyendo la lista de bajas) deja
  las filas en `pending` y la campaña en `sending`: **no hace falta
  `/resume`**; con llamar a `/send` (o con que exista un runner vivo) basta
  para drenarlas. `/resume` es exclusivamente para filas `paused`.
- `/send` y `/resume` son ambos endpoints de backoffice con la misma auth
  (sesión + capability `campanas`), re-comprueban la lista de do-not-contact
  y **no arrancan solos en boot**: solo actúan cuando un operador los llama.
- **Ninguno debe ejecutarse mientras el consentimiento promocional siga sin
  resolver** (ver "Qué falta"). El título o la descripción de un PR no son
  autoridad sobre esto: los envíos permanecen parados por decisión de
  producto, y este PR no invoca `/send` ni `/resume` ni arranca runners.

## Cómo verificar sin enviar nada (solo lectura)

Contra la BD dev (`nvc-dev-mysql`, puerto local 3307):

```sql
-- Estado por campaña/canal/estado del ledger
SELECT campaign_id, channel, status, COUNT(*) FROM campaign_recipients
GROUP BY campaign_id, channel, status ORDER BY campaign_id, channel, status;

-- Estado de las campañas (sending con filas paused = reanudables vía /resume)
SELECT id, name, status, sent_at FROM campaigns ORDER BY id DESC;

-- Supresiones por canal y origen (bounce = verificado, pending_verify = en decisión)
SELECT channel, source, COUNT(*) FROM campaign_suppressions
GROUP BY channel, source ORDER BY channel, source;

-- Cuántos pausados de una campaña están AHORA suprimidos (se saltarían al reanudar)
SELECT r.channel, COUNT(*) FROM campaign_recipients r
WHERE r.campaign_id = ? AND r.status = 'paused'
  AND EXISTS (SELECT 1 FROM campaign_suppressions s
              WHERE s.restaurant_id = r.restaurant_id AND s.channel = r.channel
                AND s.target = IF(r.channel='email', LOWER(TRIM(r.target)), TRIM(r.target)))
GROUP BY r.channel;
```

Estado observado en dev el 2026-10-21: campaigns 23/24/25 `sent`, 26/27
`draft`; ledger sin `paused` ni `skipped`; supresiones: email invalid=123,
legacy=2; whatsapp invalid=829, legacy=2; sin bounces ni bajas de
unsubscribe registradas en dev.

## Qué falta antes de reanudar (no es código)

- **Consentimiento promocional sin resolver** (asunto humano/legal): la
  reanudación de los envíos parados requiere decisión explícita. El endpoint
  `/resume` obliga a declararlo en el `ack` y lo registra en el log, pero no
  lo evalúa ni lo sustituye.
- Nadie debe llamar a `/send` ni `/resume` hasta esa decisión; este PR no
  arranca runners ni reanuda nada.
