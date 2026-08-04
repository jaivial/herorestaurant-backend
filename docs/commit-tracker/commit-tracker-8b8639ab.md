# Commit Tracker - 8b8639ab

Session: 2026-07-23T13:16:03Z

## Changes

| Time | File | Action | What Done |
|------|------|--------|-----------|
| 13:16 | internal/api/whatsapp_bot_pool_db_test.go | edit | Test Evolution-only pool selection. |
| 13:16 | internal/api/backoffice_whatsapp_connection_test.go | add | Test entitlement, isolation, safe payload. |
| 13:22 | internal/api/backoffice_whatsapp_connection_ws.go | add | Add scoped connection WebSocket hub. |
| 13:22 | internal/api/server.go | edit | Wire hub and settings routes. |
| 13:22 | internal/api/backoffice_uazapi_provisioning.go | edit | Enforce entitlement and Evolution pool. |
| 13:22 | internal/api/whatsapp_bot_webhook.go | edit | Broadcast UAZAPI connection state. |
| 13:22 | internal/api/whatsapp_bot_webhook_evolution.go | edit | Broadcast Evolution connection state. |
| 13:25 | internal/api/whatsapp_gateway_evolution_test.go | edit | Test connected phone parsing. |
| 13:26 | internal/api/whatsapp_gateway_evolution.go | edit | Parse phone and pairing state. |
| 13:26 | internal/api/backoffice_premium.go | edit | Broadcast entitlement lifecycle. |
| 13:58 | internal/api/backoffice_uazapi_servers_admin.go | edit | Expose provider in pool API. |
| 13:58 | internal/api/whatsapp_gateway_evolution.go | edit | Request base64 QR webhooks. |
| 13:58 | internal/api/whatsapp_gateway_evolution_test.go | edit | Test base64 QR webhook config. |
| 13:58 | .env.example | edit | Document Evolution webhook secret. |
| 13:58 | ENDPOINTS.md | edit | Document onboarding and WebSocket API. |
| 14:02 | internal/api/backoffice_whatsapp_connection_ws.go | edit | Reject wildcard WebSocket origins. |
| 14:15 | internal/api/whatsapp_bot_pool_db_test.go | edit | Test UAZAPI provisioning fallback. |
| 14:20 | internal/api/backoffice_uazapi_provisioning.go | edit | Fall back to UAZAPI capacity. |
| 14:20 | ENDPOINTS.md | edit | Document provider fallback. |
| 14:30 | internal/api/whatsapp_bot_lifecycle_db_test.go | edit | Test legacy instance adoption. |
| 14:34 | internal/api/backoffice_uazapi_provisioning.go | edit | Adopt configured legacy instance. |
| 14:34 | internal/api/whatsapp_gateway_uazapi.go | edit | Reject provider HTTP errors. |
| 14:56 | internal/api/whatsapp_bot_uazapi.go | edit | Use deployed webhook endpoint. |
| 14:56 | internal/api/whatsapp_bot_uazapi_test.go | edit | Test deployed webhook path. |
| 14:56 | internal/api/whatsapp_gateway_uazapi.go | edit | Parse owner phone from status. |
| 15:10 | internal/api/backoffice_whatsapp_connection_test.go | edit | Test instant local disconnect. |
| 15:15 | internal/api/backoffice_uazapi_provisioning.go | edit | Return before provider disconnect. |
| 15:15 | internal/api/backoffice_whatsapp_connection_ws.go | edit | Keep inactive disconnect state. |
| 15:35 | internal/api/backoffice_whatsapp_connection_test.go | edit | Test delayed provider QR. |
| 15:38 | internal/api/backoffice_uazapi_provisioning.go | edit | Watch and broadcast delayed QR. |
| 15:50 | internal/api/backoffice_whatsapp_connection_test.go | edit | Test disconnected normalization. |
| 15:52 | internal/api/backoffice_uazapi_provisioning.go | edit | Keep inactive status disconnected. |
| 16:15 | internal/api/backoffice_uazapi_provisioning.go | edit | Wait 60s for physical QR. |
| 16:30 | internal/api/backoffice_uazapi_provisioning.go | edit | Ignore transient provider disconnect. |
| 16:40 | internal/api/whatsapp_bot_uazapi_test.go | edit | Test real UAZAPI status shape. |
| 16:42 | internal/api/whatsapp_gateway_uazapi.go | edit | Parse real status deterministically. |
| 16:50 | internal/api/backoffice_whatsapp_connection_test.go | edit | Test QR event preservation. |
| 16:52 | internal/api/backoffice_uazapi_provisioning.go | edit | Preserve QR through partial events. |
| 17:10 | internal/api/whatsapp_gateway_test.go | edit | Test Evolution PNG QR preference. |
| 17:12 | internal/api/whatsapp_gateway_evolution.go | edit | Parse Evolution PNG QR deterministically. |
| 17:20 | internal/api/whatsapp_gateway_test.go | edit | Test Evolution interactive list wire. |
| 17:25 | ENDPOINTS.md | edit | Document self-hosted Evolution deployment. |
| 17:35 | internal/api/whatsapp_gateway_evolution.go | edit | Use working Evolution reply buttons. |
| 17:35 | internal/api/whatsapp_gateway_test.go | edit | Test button wire route. |
| 17:35 | ENDPOINTS.md | edit | Document broken list route. |
| 17:37 | internal/api/whatsapp_gateway_evolution_test.go | edit | Expect working button route. |

## Verification

- `/usr/local/go/bin/go test ./... -count=1` — pass.
- Focused DB integration tests — pass against configured DB.
- Migration `059_whatsapp_provider.sql` — applied; backup stored under `/tmp`.
- `newvillacarmen-backend.service` — active on port 8085.
- Restaurant 1 — migrated to self-hosted Evolution Baileys.
- Disconnect DB regression — immediate response and inactive local state pass.
- Delayed QR DB regression — provider QR persisted after later status pass.
- Production disconnect state — API/UI/DB all report disconnected.
- Production live E2E — real login, QR rendering, disconnect cleanup passed.
- Evolution interactive list wire regression — pass.
- Live Evolution text + reply buttons — accepted and stored.
