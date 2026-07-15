# commit-tracker-9c4a1e33

me move rices and schedule OUT of the whatsapp bot system prompt. now bot fetch them always with custom tools that hit go internals with restaurant id. schedule check two way: default weekly schedule (with open weekdays) and per-day override.

## why

user want the available rices and the schedule not written static in the prompt. instead always checked live. schedule two tools: default (from /app/config?content=restaurante, include days of week open) and individual day override.

## files me touch

- `internal/api/whatsapp_bot_tools.go` — tool defs: keep `get_rice_menu` (stronger desc "nunca inventes"). REMOVE `get_opening_hours_with_capacity`. ADD `get_default_schedule` (no params) + `get_day_schedule` (date param).
- `internal/api/whatsapp_bot_executor.go` — remove botOpeningHoursFor + botToolOpeningHours. ADD:
  - `botWeekdayKeys` map (time.Weekday Sunday=0 → weekday_open json keys).
  - `botResolveDaySchedule(ctx, rid, dateISO)` → default weekday config (loadReservationDefaults.WeekdayOpen) + per-day override from `openinghours` (hoursarray + opening_mode). open logic: override → open if has hours; else → weekday_open AND has hours.
  - `botToolDefaultSchedule` → opening_mode, morning/night hours, daily_limit, weekday_open map, open_days (spanish names).
  - `botToolDaySchedule` → date, weekday, weekday_open, has_override, opening_mode, morning/night hours, open.
  - switch: get_default_schedule / get_day_schedule cases.
- `internal/api/whatsapp_bot_prompt.go` — remove the "TIPOS DE ARROZ", "HORARIOS DE HOY" and "Límite diario" inline sections. add "## CARTA Y HORARIOS (CONSULTA SIEMPRE CON HERRAMIENTAS)" section telling bot to use get_rice_menu / get_default_schedule / get_day_schedule / capacity tools and never invent. botPromptData still keep RiceTypes/Hours/DailyLimit fields (used by the admin UI data card, not by the prompt).
- `ENDPOINTS.md` — document the new tools + their JSON shapes.

## tests (TDD, wrote/updated first)

- whatsapp_bot_tools_test.go: CoreToolsPresent now expect get_default_schedule + get_day_schedule instead of get_opening_hours_with_capacity.
- whatsapp_bot_prompt_test.go: prompt must contain the tool names, must NOT inline "Paella Valenciana"/"Arroz Negro"/"13:30"/"TIPOS DE ARROZ"/"HORARIOS DE HOY".
- whatsapp_bot_db_test.go: NEW TestBotToolSchedule_DB — seed reservation defaults (open fri/sat/sun) → get_default_schedule returns 3 open_days + hours + weekday_open. get_day_schedule: closed monday no override → open=false; open saturday → open=true; then insert openinghours override for monday (morning 12:00-13:00) → has_override=true, open=true, opening_mode=morning.
- all bot tests green + full `go test ./...` green with TEST_DB_DSN.

## deploy

- backend rebuilt + restarted, active.
