# commit-tracker-7ab2e0c4

me build multi-tenant whatsapp bot. bot live inside go backend. bot use MiniMax-M3 same as translator. bot talk to customer, make booking, cancel booking, change booking, send picture, send location, send contact card.

## files me touch

- `internal/db/migrations/056_whatsapp_bot.sql` — NEW. three table: `whatsapp_bot_sessions`, `whatsapp_bot_messages`, `whatsapp_bot_config`. migration auto-run on server start, already applied.
- `internal/config/config.go` — add bot knobs: `BotModel`, `BotTimeout`, `BotMaxTokens`, `BotMaxIterations`, `BotHistoryLimit`, `BotDailyTurnsCap`. env: `BOT_MINIMAX_MODEL`, `BOT_MINIMAX_TIMEOUT_SECONDS`, `BOT_MINIMAX_MAX_TOKENS`, `BOT_MAX_ITERATIONS`, `BOT_HISTORY_LIMIT`, `BOT_DAILY_TURNS_CAP`. all have good default, no env change needed.
- `internal/api/whatsapp_bot_llm.go` — NEW. MiniMax Messages API client with tools. copy shape from translateToEnglish. types: botBlock, botMessage, botToolDef, botLLMResponse.
- `internal/api/whatsapp_bot_engine.go` — NEW. agent loop: call LLM, run tool_use blocks, feed tool_result back, stop on end_turn or max iterations.
- `internal/api/whatsapp_bot_tools.go` — NEW. tool definitions (send_message, get_restaurant_info, get_rice_menu, availability tools, create/cancel/modify booking, send_image, send_document, send_location, send_contact, send_menu_buttons). tenant config parse. attachments gated by `disable_attachments`.
- `internal/api/whatsapp_bot_prompt.go` — NEW. dynamic system prompt per restaurant. pull brand from restaurant_branding/restaurants, contact from restaurant_info, rices from FINDE, hours from reservation defaults. tenant custom instructions injected.
- `internal/api/whatsapp_bot_executor.go` — NEW. tool implementations. all queries tenant-scoped by restaurant_id. bookings insert into `bookings` table, cancel move row to `cancelled_bookings` with cancelled_by='whatsapp', modify update in place. ownership check by phone before cancel/modify. capacity check before create. no same-day bookings.
- `internal/api/whatsapp_bot_history.go` — NEW. transcript persistence + history load with role alternation normalize.
- `internal/api/whatsapp_bot_webhook.go` — NEW. `POST /bot/webhook`. parse UAZAPI payload, resolve tenant by instance_token (fallback owner phone), gate on whatsapp_pack subscription (NEEDS_SUBSCRIPTION), dedup by messageid, daily cap, process async.
- `internal/api/whatsapp_bot_uazapi.go` — NEW. botUazapiSend helper for /send/{kind}.
- `internal/api/whatsapp_bot_admin.go` — NEW. GET/PUT `/api/admin/bot/config` (ajustes gate + role>=90).
- `internal/api/server.go` — add botSeen/botCap state to Server struct. mount `/bot/webhook` (public) and `/admin/bot/config` routes.
- `ENDPOINTS.md` — document webhook + admin config endpoints + env knobs.

## tests me write (TDD, all green)

- `whatsapp_bot_webhook_test.go` — payload parse (text, vote/button, group ignore, fromMe), dedup.
- `whatsapp_bot_llm_test.go` — request shape (model/system/tools/input_schema), tool_use parse, error paths, block serialization.
- `whatsapp_bot_engine_test.go` — loop with scripted fake LLM: send_message flow, max iterations cap, tool error survival, message alternation in second request.
- `whatsapp_bot_tools_test.go` — tool presence, attachment gating, schema validity, tenant config parse, date parse.
- `whatsapp_bot_prompt_test.go` — prompt include tenant data, language rules, rice section omit.
- `whatsapp_bot_uazapi_test.go` — endpoint building per kind, token in query, error on non-2xx.
- `whatsapp_bot_db_test.go` — integration (TEST_DB_DSN): create/list/cancel booking with ownership check, modify booking, history round trip.
- `whatsapp_bot_e2e_test.go` — full webhook→LLM→UAZAPI flow with fake servers: NEEDS_SUBSCRIPTION gate, processed turn, transcript persisted, 401 unknown token.

run: `TEST_DB_DSN='root:...@tcp(127.0.0.1:3306)/newvillacarmen?parseTime=true&charset=utf8mb4' /usr/local/go/bin/go test ./... -count=1` — all ok.

## deploy

- build with `/usr/local/go/bin/go build -o server.new ./cmd/server`, installed over `server`, restarted `newvillacarmen-backend.service`. active.
- migration 056 applied automatic on restart (checked schema_migrations).
- smoke: `POST /bot/webhook` bogus token → 401 unknown instance. no-message payload → processed:false. good.

## decisions

- MiniMax-M3 with same credentials as translator (user say so). no anthropic sdk import — MiniMax speak Messages API already.
- language: prompt tell bot answer in customer language always, default from tenant config (es/en). translator reuse happen via loadRiceTypes english variants in rice tool output.
- attachments (user say add): send_image + send_document via UAZAPI /send/media, send_location + send_contact from restaurant_info data. gate per tenant with disable_attachments.
- send_message is a TOOL (like old C# bot) — bot never reply plain text.
- history replay only user/assistant text turns, not tool turns (stale data).
- dedup + daily cap in-process (no redis). fine for one instance.
