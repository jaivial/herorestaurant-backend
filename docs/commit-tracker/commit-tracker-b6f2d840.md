# commit-tracker-b6f2d840

me add whatsapp bot tools so the ai can read menus (with category type), each menu detail (dishes per section + settings), and the coffee, drinks and wines cartas. all multi-tenant by restaurant id.

## new tools

- `list_menus` — active bookable menus with category (closed_conventional, closed_group, a_la_carte, a_la_carte_group, special), spanish category_label, price, subtitle. sorted by category.
- `get_menu_details` (menu_id) — full menu: sections[{title, kind, dishes[{title, description, price, supplement_enabled, supplement_price}]}] + settings{beverage_type, beverage_label, unlimited_drinks, drink_price_per_person, has_supplement, supplement_price, coffee_included, min_party_size, has_max_main_dishes_per_table, max_main_dishes_per_table, comments} + legacy entrantes/principales/postre.
- `get_coffee_menu` — CAFES rows (name, price, description, group, supplement).
- `get_drinks_menu` — BEBIDAS rows.
- `get_wines_menu` — VINOS grouped-friendly (name, price, type, winery, denomination, year, abv).

## files me touch

- `internal/api/whatsapp_bot_menu_tools.go` — NEW. all loaders + helpers:
  - botMenuCategoryLabel (menu_type → spanish label).
  - botBeverageLabel + botMenuBeverageSettings (parse beverage json blob: type/price_per_person/has_supplement/supplement_price → unlimited_drinks + labels).
  - botToolListMenus, botToolMenuDetails, botLoadMenuSections (v2 sections + active dishes join), botToolCoffeeMenu, botToolDrinksMenu, botLoadSimpleMenu (shared for CAFES/BEBIDAS), botToolWinesMenu.
  - reuse existing helpers: normalizeV2MenuType, normalizeV2SectionKind, decodeJSONOrFallback, anySliceToStringList, anyToString.
- `internal/api/whatsapp_bot_tools.go` — 5 new tool defs after get_rice_menu.
- `internal/api/whatsapp_bot_executor.go` — switch cases for the 5 tools.
- `internal/api/whatsapp_bot_prompt.go` — CARTA Y HORARIOS section now also points to list_menus/get_menu_details + coffee/drinks/wines tools.
- `ENDPOINTS.md` — document the new tools.

## data sources (multi-tenant, all scoped by restaurant_id)

- menus table (menu_type, price, beverage json, min_party_size, main_dishes_limit(_number), included_coffee, comments, entrantes/principales/postre json).
- group_menu_sections_v2 + group_menu_section_dishes_v2 (active=1) for dishes per section.
- CAFES / BEBIDAS (nombre, precio, descripcion, titulo, suplemento, active).
- VINOS (nombre, precio, descripcion, tipo, bodega, denominacion_origen, anyo, graduacion, active).

## tests (TDD, wrote first)

- `internal/api/whatsapp_bot_menu_tools_test.go`:
  - TestBotMenuCategoryLabel, TestBotBeverageLabel (pure).
  - TestBotMenuToolDefs_Present.
  - TestBotToolMenus_DB: seed closed_group menu (beverage ilimitada, price_per_person 8, coffee included, min_party_size 8) + section + dish with supplement → list_menus category/label, get_menu_details fields incl settings, unknown menu_id → "no encontrado".
  - TestBotToolBeverages_DB: seed CAFES/BEBIDAS/VINOS → assert names, description, tipo, denominacion, bodega present.
- all bot tests + full `go test ./...` green with TEST_DB_DSN.

## deploy

- backend rebuilt + restarted, active. IA-tab prompt preview reflects the new tools automatically (server-rendered).
