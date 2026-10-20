# IA y WhatsApp: registro de consumo y mensajes

Nueva pestaña **Estadísticas > IA y WhatsApp** (`/app/estadisticas`) que muestra,
en tiempo real, cuántas llamadas de IA hace el backend, cuántos tokens consume y
cuánto cuesta, además del registro completo de mensajes de WhatsApp enviados y
recibidos. Coordination ids: `ai_usage_logs_v1`, `whatsapp_message_logs_v1`,
`ai_usage_stats_api_v1`, `whatsapp_message_logs_api_v1`.

## Modelo de datos

Dos tablas append-only (migraciones `182_ai_usage_and_whatsapp_logs.sql` y `183_minimax_m31_only.sql`).

### `ai_usage_logs`

Una fila por cada petición al LLM.

| Columna | Tipo | Notas |
|---|---|---|
| `restaurant_id` | INT | Tenant; FK a `restaurants`. |
| `feature` | VARCHAR(64) | `forky_assistant`, `whatsapp_bot`, `translation`, `stock_ai`, `stock_ocr`. |
| `provider` | VARCHAR(48) | Proveedor normalizado (`deepseek`, `minimax`). |
| `model` | VARCHAR(120) | Modelo tal cual lo reporta la configuración. |
| `input_tokens`, `output_tokens`, `cache_read_tokens`, `cache_write_tokens` | BIGINT | Uso reportado por el proveedor. |
| `cost_usd` | DECIMAL(14,8) | Coste calculado al insertar con la tarifa vigente. |
| `priced` | TINYINT(1) | 0 si el modelo no está en la tarifa conocida (coste 0). |
| `is_fallback` | TINYINT(1) | 1 cuando entró el modelo de fallback del bot. |
| `created_at` | DATETIME(3) | Momento de la llamada. |

### `whatsapp_message_logs`

Una fila por mensaje de WhatsApp registrado.

| Columna | Tipo | Notas |
|---|---|---|
| `direction` | ENUM | `inbound` (cliente) / `outbound` (restaurante). |
| `message_type` | VARCHAR(32) | `text`, `location`, `contact`, `media`. |
| `user_phone` | VARCHAR(64) | Teléfono del cliente. |
| `content` | MEDIUMTEXT | Texto del mensaje. |
| `source` | VARCHAR(64) | Origen (`inbound`, `agent`, `manual_whatsapp`, `booking_notification`…). |
| `status` | ENUM | `received`, `sent`, `failed`. |
| `tool_name` | VARCHAR(128) | Tool del bot que originó un envío, si aplica. |
| `created_at` | DATETIME(3) | Momento del registro. |

El registro se escribe desde `botRecordConversationMessage`, el único punto por
el que pasan todos los mensajes que el backend persiste (bot, respuestas
manuales y notificaciones). Escribir en MySQL es best-effort: un fallo del
registro nunca impide enviar ni recibir.

## Tarifas

Coste en USD por 1M tokens. Se calcula al insertar, no al consultar, para que el
histórico no cambie si el proveedor mueve precios.

| Modelo | Input | Output | Cache read | Notas |
|---|---|---|---|---|
| MiniMax-M3.1-Flash | $0.15 | $0.60 | $0.015 | Tarifa plana. Único modelo en uso. |
| MiniMax-M3 (legacy) | $0.30 | $1.20 | $0.06 | Solo para filas históricas; ya no se selecciona. |

`MiniMax-M3.1-Flash` es el **único modelo** de la aplicación: bot de WhatsApp
(principal, sin fallback a otro modelo), ForKy, traducciones y stock. La
migración `183_minimax_m31_only.sql` apunta a ese modelo la configuración de
cada restaurante y vacía el fallback del router. Los modelos sin tarifa conocida
se guardan con coste 0 y se cuentan en `unpricedCalls` para que el panel avise.

Semántica de tokens por wire: el endpoint Anthropic devuelve `input_tokens` sin
los cacheados y `cache_read_input_tokens` aparte; el formato OpenAI incluye los
cacheados en `prompt_tokens`. El backend normaliza ambos a la convención
Anthropic antes de calcular el coste, para no cobrar dos veces el mismo token.

## API

- `GET /api/admin/analytics/ai-usage?from&to&granularity` — resumen, series,
  desglose por modelo y por funcionalidad, y tarifas aplicadas.
- `GET /api/admin/analytics/whatsapp-messages?from&to&granularity&direction&search&page&limit`
  — resumen, series y listado paginado.

Ambas requieren sesión de backoffice con sección `estadisticas`. Detalle completo
en `backend/ENDPOINTS.md`.

## Tiempo real

Cada inserción publica en el socket global del backoffice
(`GlobalSocketProvider`, topic por restaurante):

- `ai_usage` → `{ "type": "ai_usage_recorded", ... }`
- `whatsapp_log` → `{ "type": "whatsapp_message_recorded", ... }`

El componente `MessagingDashboard` se suscribe a ambos topics y recarga con un
debounce de 800 ms, de modo que un turno del agente con varias llamadas al LLM
solo provoca una recarga.

## Forky

Dos tools nuevas (sección `estadisticas`, solo backoffice) reutilizan la misma
agregación que los endpoints:

- `ai_usage_report` — consumo de IA por semana/mes/año, por modelo y
  funcionalidad, con coste USD y tarifas.
- `whatsapp_messages_list` — resumen y mensajes de WhatsApp con filtro de
  dirección y búsqueda.

La matriz completa está en `backend/docs/FORKY_API.md`.

## Puntos de observabilidad

- Checkpoints de log: `[ai_usage_logs_v1]`, `[whatsapp_message_logs_v1]` para
  fallos de inserción.
- Checkpoint previo existente: `wa_bot_llm_fallback_used` cuando entra el modelo
  de fallback.
- Topics de socket: `ai_usage`, `whatsapp_log`.
