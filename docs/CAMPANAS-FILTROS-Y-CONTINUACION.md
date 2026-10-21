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
   (backoffice, misma auth que envío): re-pona `paused → pending` tras
   re-comprobar la lista de bajas (los ahora suprimidos pasan a `skipped`,
   nunca a `pending`), resetea el breaker en memoria (siguiente poll
   re-baselinea) y arranca el runner. **Requiere `ack` obligatorio** con la
   causa resuelta, que se registra en el log (`campaign.resume.requested`,
   coord_id `camp-<uuid>`): deja rastro auditable de la decisión del
   operador. No toca `sent`/`failed`/`skipped`.
2. **Estado final que mentía**: al terminar el runner la campaña pasaba a
   `sent` aunque hubiera filas pausadas. → **Corregido**: si al salir quedan
   `paused` o `pending`, la campaña se queda en `sending` (semántica
   "reanudable" ya existente) y se loguea `campaign.send.interrupted`;
   solo pasa a `sent` cuando no queda nada por enviar.
3. **La previsualización de audiencia no distinguía ya-enviados**: el
   operador veía el total del audiencia, no el trabajo real restante. →
   **Corregido**: `GET /campanas/{id}/audience` devuelve ahora
   `new` (los únicos que un envío añadiría), `already_in_ledger` y `stats`
   (ledger completo por estado); `sample` muestra solo los nuevos.
4. **`POST /send` reportaba `queued = total audiencia`** incluyendo filas que
   el ledger ya tenía. → **Corregido**: `queued` = filas realmente insertadas
   (RowsAffected del INSERT IGNORE) y `already` = las ya presentes.
5. **Un error transitorio al leer supresiones quemaba el destinatario** (la
   fila pasaba a `failed` permanente con "no se pudo comprobar la lista de
   bajas"). → **Corregido**: la fila se queda `pending`, se respeta 5 s y
   tras 3 fallos consecutivos se aborta el run dejando la campaña
   reanudable (log `campaign.delivery.suppression_read_failed`).
6. **Emails de la audiencia sin normalizar**: se guardaban con la caja del
   origen. → **Corregido**: se almacenan `LOWER(TRIM(x))` según el contrato
   de normalización (migración 130), asegurando el dedupe y la comparación
   de supresión con un valor canónico.
7. **Inveribilidad**: `campaignStats` no exponía `paused`/`skipped` y el
   ledger no permitía filtrar por esos estados. → **Corregido**: stats
   aditivas `paused` + `skipped`; `GET /campanas/{id}/recipients?status=`
   acepta también `paused` y `skipped`.

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
