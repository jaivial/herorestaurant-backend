# commit-tracker-2f8b5a17

me add per-restaurant LLM model override for whatsapp bot + prompt preview endpoints for root IA tab in backoffice.

## files me touch

- `internal/api/whatsapp_bot_tools.go` — botTenantConfig grow `model` field (json `model`). empty mean use global BotModel.
- `internal/api/whatsapp_bot_llm.go` — botLLMCall now take modelOverride first arg. priority: tenant model > cfg.BotModel > cfg.MiniMaxModel.
- `internal/api/whatsapp_bot_engine.go` — botRunAgentLoop take model param, pass through to botLLMCall.
- `internal/api/whatsapp_bot_webhook.go` — botProcessMessage pass tenant.Model to loop.
- `internal/api/whatsapp_bot_admin.go` — NEW handlers: GET/PUT `/api/admin/bot/settings/{restaurantId}`. root-only. return config + promptPreview (buildBotSystemPrompt render with sample pushname "Cliente") + defaultModel. PUT save to whatsapp_bot_config upsert then re-render preview.
- `internal/api/server.go` — mount routes with `rootOnlyGate` (importance 100).
- `ENDPOINTS.md` — document.

## tests (TDD, wrote first)

- `internal/api/whatsapp_bot_admin_test.go` — NEW:
  - TestBotLLMCall_ModelOverride: override win, empty fall back to BotModel.
  - TestParseBotTenantConfig_Model: json parse.
  - TestBOBotSettings_DB: PUT save model+tone, GET round-trip + promptPreview contains header + saved tone, defaultModel correct, rid=0 → 400.
- fixed call sites in whatsapp_bot_llm_test.go / whatsapp_bot_engine_test.go for new signature.
- full suite green with TEST_DB_DSN.

## deploy

- backend rebuilt + restarted, active. smoke: GET /api/admin/bot/settings/1 no session → 401 good.
- backoffice restarted after frontend changes (other tracker).

## update 2: editable rules + live tenant data + phone prefill

- `whatsapp_bot_tools.go` — botTenantConfig grow `rules` field. empty = use default rules.
- `whatsapp_bot_prompt.go` — critical rules extracted to `botDefaultRules` const. renderBotSystemPrompt use tenant.Rules when set. buildBotSystemPrompt split into `loadBotPromptData` (fetch multi-tenant data) + render, so admin endpoint can expose the raw data.
- `whatsapp_bot_admin.go` — GET/PUT response now include: `defaultRules`, `restaurant` object (brandName, phone, address, email, website, menuUrl, riceTypes, hours, dailyLimit) from live DB, and `config.contact_phone` prefilled from restaurant_info.telefono when override empty. PUT normalize: rules identical to defaults stored as empty (tenant keep receiving default improvements).
- test TestBOBotSettings_DB extended: seed restaurant_info, assert phone prefill '+34 900 111 222', restaurant data, defaultRules contains send_message.
- verified against real DB: restaurant 1 (Alqueria Villa Carmen) telefono +34692747052 in restaurant_info feeds the prefill.
- backend rebuilt + both services restarted, active.

## update 3: live system prompt preview while editing

- `whatsapp_bot_admin.go` — NEW POST `/api/admin/bot/settings/{restaurantId}/preview`: render prompt for a draft config WITHOUT persisting. same response shape.
- `server.go` — route mounted root-only.
- test extended: draft rules appear in preview, saved config untouched after preview call.
