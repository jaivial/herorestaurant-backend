package api

import (
	"context"
	"encoding/json"
)

// assistantToolHandler executes a single Forky tool against the authenticated
// active restaurant. restaurantID is always derived from the session, never
// from model input. Handlers return the JSON string that becomes the
// tool_result block.
type assistantToolHandler func(s *Server, ctx context.Context, restaurantID int, input json.RawMessage) (string, error)

// assistantTool is the single source of truth for a Forky custom tool. The
// registry drives the tool list exposed to the model, the RBAC section/write
// gates, the confirmation policy and the executor — there are no parallel
// switches to keep in sync.
type assistantTool struct {
	Name           string
	Description    string
	Schema         json.RawMessage
	Section        string // backoffice RBAC section (see assistantToolAllowed)
	Write          bool   // mutation: requires write capability
	Confirm        bool   // mutation requires a server-side confirmation token
	BackofficeOnly bool   // requires an authenticated backoffice session even for reads
	Handler        assistantToolHandler
}

var assistantToolRegistry = []assistantTool{
	// --- Reservations (reservas) ---
	{
		Name: "restaurant_info", Description: "Lee datos básicos del restaurante activo.",
		Schema: json.RawMessage(`{"type":"object","properties":{}}`), Section: "reservas",
		Handler: func(s *Server, ctx context.Context, rid int, _ json.RawMessage) (string, error) {
			return s.assistantRestaurantInfo(ctx, rid)
		},
	},
	{
		Name: "bookings_summary", Description: "Devuelve resumen de reservas del restaurante activo para una fecha o rango opcional (totales y personas).",
		Schema: json.RawMessage(`{"type":"object","properties":{"date":{"type":"string"},"date_from":{"type":"string"},"date_to":{"type":"string"}}}`), BackofficeOnly: true, Section: "reservas",
		Handler: func(s *Server, ctx context.Context, rid int, input json.RawMessage) (string, error) {
			return s.assistantBookingsSummary(ctx, rid, input)
		},
	},
	{
		Name: "bookings_list", Description: "Devuelve el detalle individual de las reservas del restaurante activo (cliente, fecha, hora, personas, estado) para una fecha o rango opcional. Para construir tablas de reservas.",
		Schema: json.RawMessage(`{"type":"object","properties":{"date":{"type":"string"},"date_from":{"type":"string"},"date_to":{"type":"string"},"limit":{"type":"integer"}}}`), BackofficeOnly: true, Section: "reservas",
		Handler: func(s *Server, ctx context.Context, rid int, input json.RawMessage) (string, error) {
			return s.assistantBookingsListHandler(ctx, rid, input)
		},
	},
	{
		Name: "restaurant_query", Description: "Consulta datos agregados seguros del restaurante activo. resource: bookings, menus o wines.",
		Schema: json.RawMessage(`{"type":"object","properties":{"resource":{"type":"string","enum":["bookings","menus","wines"]},"date_from":{"type":"string"},"date_to":{"type":"string"}},"required":["resource"]}`), Section: "reservas",
		Handler: func(s *Server, ctx context.Context, rid int, input json.RawMessage) (string, error) {
			return s.assistantRestaurantQuery(ctx, rid, input)
		},
	},
	{
		Name: "create_booking", Description: "Crea reserva del restaurante activo con las reglas del flujo real (fecha, hora, personas, nombre y teléfono). Requiere confirmed=true.", Write: true, Confirm: true,
		Schema:         json.RawMessage(`{"type":"object","properties":{"date":{"type":"string"},"time":{"type":"string"},"people":{"type":"integer"},"name":{"type":"string"},"contact_phone":{"type":"string"},"contact_phone_country_code":{"type":"string"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["date","time","people","name","confirmed"]}`),
		BackofficeOnly: true, Section: "reservas",
		Handler: func(s *Server, ctx context.Context, rid int, input json.RawMessage) (string, error) {
			return s.assistantCreateBooking(ctx, rid, input)
		},
	},
	{
		Name: "update_booking", Description: "Actualiza reserva del restaurante activo con las reglas del flujo real. Requiere confirmed=true.", Write: true, Confirm: true,
		Schema:         json.RawMessage(`{"type":"object","properties":{"booking_id":{"type":"integer"},"date":{"type":"string"},"time":{"type":"string"},"people":{"type":"integer"},"name":{"type":"string"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["booking_id","confirmed"]}`),
		BackofficeOnly: true, Section: "reservas",
		Handler: func(s *Server, ctx context.Context, rid int, input json.RawMessage) (string, error) {
			return s.assistantUpdateBooking(ctx, rid, input)
		},
	},
	{
		Name: "delete_booking", Description: "Cancela reserva del restaurante activo. Requiere confirmed=true.", Write: true, Confirm: true,
		Schema:         json.RawMessage(`{"type":"object","properties":{"booking_id":{"type":"integer"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["booking_id","confirmed"]}`),
		BackofficeOnly: true, Section: "reservas",
		Handler: func(s *Server, ctx context.Context, rid int, input json.RawMessage) (string, error) {
			return s.assistantDeleteBooking(ctx, rid, input)
		},
	},
	{
		Name: "customers_list", Description: "Lista clientes y fuentes del restaurante activo.",
		Schema: json.RawMessage(`{"type":"object","properties":{"search":{"type":"string"},"limit":{"type":"integer"}}}`), BackofficeOnly: true, Section: "reservas",
		Handler: catalogHandler("customers_list"),
	},
	{
		Name: "booking_limits_update", Description: "Establece el límite diario de reservas para una fecha del restaurante activo. Requiere confirmed=true.", Write: true, Confirm: true,
		Schema:  json.RawMessage(`{"type":"object","properties":{"date":{"type":"string"},"daily_limit":{"type":"integer"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["date","daily_limit","confirmed"]}`),
		Section: "reservas", BackofficeOnly: true,
		Handler: func(s *Server, ctx context.Context, rid int, input json.RawMessage) (string, error) {
			return s.assistantBookingLimitsUpdate(ctx, rid, input)
		},
	},
	{
		Name: "booking_limits_get", Description: "Lee el límite diario de reservas, la ocupación y las plazas libres de una fecha del restaurante activo.",
		Schema: json.RawMessage(`{"type":"object","properties":{"date":{"type":"string"}},"required":["date"]}`), BackofficeOnly: true, Section: "reservas",
		Handler: func(s *Server, ctx context.Context, rid int, input json.RawMessage) (string, error) {
			return s.assistantBookingLimitsGet(ctx, rid, input)
		},
	},

	// --- Catalog / menu resources (comida) ---
	{
		Name: "catalog_list", Description: "Lista recursos del restaurante activo: comida, bebidas, cafés, vinos, menús, productos POS, stock o miembros.",
		Schema: json.RawMessage(`{"type":"object","properties":{"resource":{"type":"string","enum":["comida","bebidas","cafes","vinos","menus","pos_products","stock_items","members"]},"search":{"type":"string"},"limit":{"type":"integer"}},"required":["resource"]}`), BackofficeOnly: true, Section: "comida",
		Handler: catalogHandler("catalog_list"),
	},
	{
		Name: "catalog_get", Description: "Obtiene recurso por ID dentro del restaurante activo.",
		Schema: json.RawMessage(`{"type":"object","properties":{"resource":{"type":"string"},"id":{"type":"integer"}},"required":["resource","id"]}`), BackofficeOnly: true, Section: "comida",
		Handler: catalogHandler("catalog_get"),
	},
	{
		Name: "catalog_create", Description: "Crea recurso del restaurante activo. Requiere confirmed=true.", Write: true, Confirm: true,
		Schema:         json.RawMessage(`{"type":"object","properties":{"resource":{"type":"string"},"name":{"type":"string"},"description":{"type":"string"},"price":{"type":"number"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["resource","name","confirmed"]}`),
		BackofficeOnly: true, Section: "comida",
		Handler: catalogHandler("catalog_create"),
	},
	{
		Name: "catalog_update", Description: "Actualiza recurso del restaurante activo. Requiere confirmed=true.", Write: true, Confirm: true,
		Schema:         json.RawMessage(`{"type":"object","properties":{"resource":{"type":"string"},"id":{"type":"integer"},"name":{"type":"string"},"description":{"type":"string"},"price":{"type":"number"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["resource","id","confirmed"]}`),
		BackofficeOnly: true, Section: "comida",
		Handler: catalogHandler("catalog_update"),
	},
	{
		Name: "catalog_delete", Description: "Elimina/desactiva recurso del restaurante activo. Requiere confirmed=true.", Write: true, Confirm: true,
		Schema:         json.RawMessage(`{"type":"object","properties":{"resource":{"type":"string"},"id":{"type":"integer"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["resource","id","confirmed"]}`),
		BackofficeOnly: true, Section: "comida",
		Handler: catalogHandler("catalog_delete"),
	},

	// --- Menus (menus) ---
	{
		Name: "menus_list", Description: "Lista los menús de grupo del restaurante activo.",
		Schema: json.RawMessage(`{"type":"object","properties":{"include_drafts":{"type":"boolean"}}}`), BackofficeOnly: true, Section: "menus",
		Handler: func(s *Server, ctx context.Context, rid int, input json.RawMessage) (string, error) {
			return s.assistantMenusList(ctx, rid, input)
		},
	},
	{
		Name: "menu_get", Description: "Obtiene un menú de grupo del restaurante activo con sus secciones.",
		Schema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer"}},"required":["id"]}`), BackofficeOnly: true, Section: "menus",
		Handler: func(s *Server, ctx context.Context, rid int, input json.RawMessage) (string, error) {
			return s.assistantMenuGet(ctx, rid, input)
		},
	},
	{
		Name: "menu_sections_get", Description: "Lista los platos de una sección concreta de un menú del restaurante activo.",
		Schema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer"},"section_id":{"type":"integer"}},"required":["id","section_id"]}`), BackofficeOnly: true, Section: "menus",
		Handler: func(s *Server, ctx context.Context, rid int, input json.RawMessage) (string, error) {
			return s.assistantMenuSectionsGet(ctx, rid, input)
		},
	},
	{
		Name: "menu_toggle_active", Description: "Activa o desactiva un menú del restaurante activo. Requiere confirmed=true.", Write: true, Confirm: true,
		Schema:  json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["id","confirmed"]}`),
		Section: "menus", BackofficeOnly: true,
		Handler: func(s *Server, ctx context.Context, rid int, input json.RawMessage) (string, error) {
			return s.assistantMenuToggleActive(ctx, rid, input)
		},
	},

	// --- Analytics (estadisticas) ---
	{
		Name: "analytics_report", Description: "Devuelve métricas y series del restaurante activo para gráficos.",
		Schema: json.RawMessage(`{"type":"object","properties":{"metric":{"type":"string","enum":["bookings","bookings_by_hour","revenue","revenue_by_day","products","stock"]},"date_from":{"type":"string"},"date_to":{"type":"string"}},"required":["metric"]}`), BackofficeOnly: true, Section: "estadisticas",
		Handler: func(s *Server, ctx context.Context, rid int, input json.RawMessage) (string, error) {
			return s.assistantAnalyticsTool(ctx, rid, input)
		},
	},

	// --- Schedules (horarios) ---
	{
		Name: "schedules_list", Description: "Lista horarios laborales del restaurante activo.",
		Schema: json.RawMessage(`{"type":"object","properties":{"date":{"type":"string"},"limit":{"type":"integer"}}}`), BackofficeOnly: true, Section: "horarios",
		Handler: catalogHandler("schedules_list"),
	},
	{
		Name: "schedules_by_date", Description: "Lista los horarios de todo el personal del restaurante activo para una fecha (por defecto hoy).",
		Schema: json.RawMessage(`{"type":"object","properties":{"date":{"type":"string"}}}`), BackofficeOnly: true, Section: "horarios",
		Handler: func(s *Server, ctx context.Context, rid int, input json.RawMessage) (string, error) {
			return s.assistantSchedulesByDate(ctx, rid, input)
		},
	},
	{
		Name: "schedules_month", Description: "Resumen de cobertura de horarios del restaurante activo por día para un mes (year/month, por defecto el actual).",
		Schema: json.RawMessage(`{"type":"object","properties":{"year":{"type":"integer"},"month":{"type":"integer"}}}`), BackofficeOnly: true, Section: "horarios",
		Handler: func(s *Server, ctx context.Context, rid int, input json.RawMessage) (string, error) {
			return s.assistantSchedulesMonth(ctx, rid, input)
		},
	},
	{
		Name: "schedules_create", Description: "Asigna un horario a un miembro del restaurante activo. Requiere confirmed=true.", Write: true, Confirm: true,
		Schema:  json.RawMessage(`{"type":"object","properties":{"date":{"type":"string"},"member_id":{"type":"integer"},"start_time":{"type":"string"},"end_time":{"type":"string"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["date","member_id","start_time","end_time","confirmed"]}`),
		Section: "horarios", BackofficeOnly: true,
		Handler: func(s *Server, ctx context.Context, rid int, input json.RawMessage) (string, error) {
			return s.assistantSchedulesCreate(ctx, rid, input)
		},
	},
	{
		Name: "schedules_update", Description: "Actualiza la hora de un horario del restaurante activo. Requiere confirmed=true.", Write: true, Confirm: true,
		Schema:  json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer"},"start_time":{"type":"string"},"end_time":{"type":"string"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["id","start_time","end_time","confirmed"]}`),
		Section: "horarios", BackofficeOnly: true,
		Handler: func(s *Server, ctx context.Context, rid int, input json.RawMessage) (string, error) {
			return s.assistantSchedulesUpdate(ctx, rid, input)
		},
	},
	{
		Name: "schedules_delete", Description: "Elimina un horario del restaurante activo. Requiere confirmed=true.", Write: true, Confirm: true,
		Schema:  json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["id","confirmed"]}`),
		Section: "horarios", BackofficeOnly: true,
		Handler: func(s *Server, ctx context.Context, rid int, input json.RawMessage) (string, error) {
			return s.assistantSchedulesDelete(ctx, rid, input)
		},
	},

	// --- Fichaje (fichaje) ---
	{
		Name: "fichaje_state_get", Description: "Devuelve el estado actual de fichaje del restaurante activo (miembro, entrada activa, horario de hoy, entradas activas).",
		Schema: json.RawMessage(`{"type":"object","properties":{}}`), BackofficeOnly: true, Section: "fichaje",
		Handler: func(s *Server, ctx context.Context, rid int, input json.RawMessage) (string, error) {
			return s.assistantFichajeStateGet(ctx, rid, input)
		},
	},
	{
		Name: "fichaje_entries_list", Description: "Lista los fichajes (entradas/salidas) de un miembro del restaurante activo en una fecha (por defecto hoy).",
		Schema: json.RawMessage(`{"type":"object","properties":{"date":{"type":"string"},"member_id":{"type":"integer"},"limit":{"type":"integer"}}}`), BackofficeOnly: true, Section: "fichaje",
		Handler: func(s *Server, ctx context.Context, rid int, input json.RawMessage) (string, error) {
			return s.assistantFichajeEntriesList(ctx, rid, input)
		},
	},
	{
		Name: "fichaje_admin_start", Description: "Registra la entrada (fichaje) de un miembro del restaurante activo. Requiere confirmed=true.", Write: true, Confirm: true,
		Schema:  json.RawMessage(`{"type":"object","properties":{"member_id":{"type":"integer"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["member_id","confirmed"]}`),
		Section: "fichaje", BackofficeOnly: true,
		Handler: func(s *Server, ctx context.Context, rid int, input json.RawMessage) (string, error) {
			return s.assistantFichajeAdminStart(ctx, rid, input)
		},
	},
	{
		Name: "fichaje_admin_stop", Description: "Registra la salida (fichaje) de un miembro del restaurante activo. Requiere confirmed=true.", Write: true, Confirm: true,
		Schema:  json.RawMessage(`{"type":"object","properties":{"member_id":{"type":"integer"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["member_id","confirmed"]}`),
		Section: "fichaje", BackofficeOnly: true,
		Handler: func(s *Server, ctx context.Context, rid int, input json.RawMessage) (string, error) {
			return s.assistantFichajeAdminStop(ctx, rid, input)
		},
	},
	{
		Name: "fichaje_start", Description: "Registra tu entrada (fichaje) como miembro del restaurante activo. Requiere confirmed=true.", Write: true, Confirm: true,
		Schema:  json.RawMessage(`{"type":"object","properties":{"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["confirmed"]}`),
		Section: "fichaje", BackofficeOnly: true,
		Handler: func(s *Server, ctx context.Context, rid int, input json.RawMessage) (string, error) {
			return s.assistantFichajeStart(ctx, rid, input)
		},
	},
	{
		Name: "fichaje_stop", Description: "Registra tu salida (fichaje) como miembro del restaurante activo. Requiere confirmed=true.", Write: true, Confirm: true,
		Schema:  json.RawMessage(`{"type":"object","properties":{"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["confirmed"]}`),
		Section: "fichaje", BackofficeOnly: true,
		Handler: func(s *Server, ctx context.Context, rid int, input json.RawMessage) (string, error) {
			return s.assistantFichajeStop(ctx, rid, input)
		},
	},

	// --- Stock (stock) ---
	{
		Name: "stock_warehouses_list", Description: "Lista los almacenes del restaurante activo.",
		Schema: json.RawMessage(`{"type":"object","properties":{}}`), BackofficeOnly: true, Section: "stock",
		Handler: func(s *Server, ctx context.Context, rid int, input json.RawMessage) (string, error) {
			return s.assistantStockWarehousesList(ctx, rid, input)
		},
	},
	{
		Name: "stock_categories_list", Description: "Lista las categorías de stock del restaurante activo.",
		Schema: json.RawMessage(`{"type":"object","properties":{}}`), BackofficeOnly: true, Section: "stock",
		Handler: func(s *Server, ctx context.Context, rid int, input json.RawMessage) (string, error) {
			return s.assistantStockCategoriesList(ctx, rid, input)
		},
	},
	{
		Name: "stock_items_list", Description: "Lista artículos de stock del restaurante activo con niveles, stock total y filtros (búsqueda, tipo, categoría, almacén).",
		Schema: json.RawMessage(`{"type":"object","properties":{"search":{"type":"string"},"kind":{"type":"string"},"category_id":{"type":"integer"},"warehouse_id":{"type":"integer"},"page":{"type":"integer"},"page_size":{"type":"integer"},"sort":{"type":"string"}}}`), BackofficeOnly: true, Section: "stock",
		Handler: func(s *Server, ctx context.Context, rid int, input json.RawMessage) (string, error) {
			return s.assistantStockItemsList(ctx, rid, input)
		},
	},
	{
		Name: "stock_item_movements_list", Description: "Lista los movimientos de un artículo de stock del restaurante activo.",
		Schema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer"},"page":{"type":"integer"},"page_size":{"type":"integer"}},"required":["id"]}`), BackofficeOnly: true, Section: "stock",
		Handler: func(s *Server, ctx context.Context, rid int, input json.RawMessage) (string, error) {
			return s.assistantStockItemMovementsList(ctx, rid, input)
		},
	},
	{
		Name: "stock_summary", Description: "Resumen de stock del restaurante activo: totales, bajo mínimo, bajo pedido, agotados y negativos.",
		Schema: json.RawMessage(`{"type":"object","properties":{}}`), BackofficeOnly: true, Section: "stock",
		Handler: func(s *Server, ctx context.Context, rid int, input json.RawMessage) (string, error) {
			return s.assistantStockSummary(ctx, rid, input)
		},
	},
	{
		Name: "stock_movement_create", Description: "Registra un movimiento manual de stock (ajuste o merma) para un artículo. Requiere confirmed=true.", Write: true, Confirm: true,
		Schema:  json.RawMessage(`{"type":"object","properties":{"item_id":{"type":"integer"},"warehouse_id":{"type":"integer"},"quantity":{"type":"number"},"unit_id":{"type":"integer"},"type":{"type":"string"},"direction":{"type":"string"},"waste_reason":{"type":"string"},"note":{"type":"string"},"idempotency_key":{"type":"string"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["item_id","warehouse_id","quantity","unit_id","type","idempotency_key","confirmed"]}`),
		Section: "stock", BackofficeOnly: true,
		Handler: func(s *Server, ctx context.Context, rid int, input json.RawMessage) (string, error) {
			return s.assistantStockMovementCreate(ctx, rid, input)
		},
	},
	{
		Name: "stock_transfer_create", Description: "Transfiere stock de un almacén a otro del restaurante activo. Requiere confirmed=true.", Write: true, Confirm: true,
		Schema:  json.RawMessage(`{"type":"object","properties":{"item_id":{"type":"integer"},"from_warehouse_id":{"type":"integer"},"to_warehouse_id":{"type":"integer"},"quantity":{"type":"number"},"unit_id":{"type":"integer"},"idempotency_key":{"type":"string"},"note":{"type":"string"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["item_id","from_warehouse_id","to_warehouse_id","quantity","unit_id","idempotency_key","confirmed"]}`),
		Section: "stock", BackofficeOnly: true,
		Handler: func(s *Server, ctx context.Context, rid int, input json.RawMessage) (string, error) {
			return s.assistantStockTransferCreate(ctx, rid, input)
		},
	},
	{
		Name: "recipes_list", Description: "Lista recetas del restaurante activo.",
		Schema: json.RawMessage(`{"type":"object","properties":{"limit":{"type":"integer"}}}`), BackofficeOnly: true, Section: "stock",
		Handler: catalogHandler("recipes_list"),
	},
	{
		Name: "production_list", Description: "Lista producción del restaurante activo.",
		Schema: json.RawMessage(`{"type":"object","properties":{"limit":{"type":"integer"}}}`), BackofficeOnly: true, Section: "stock",
		Handler: catalogHandler("production_list"),
	},
	{
		Name: "waste_costs_list", Description: "Lista mermas y costes del restaurante activo.",
		Schema: json.RawMessage(`{"type":"object","properties":{"limit":{"type":"integer"}}}`), BackofficeOnly: true, Section: "stock",
		Handler: catalogHandler("waste_costs_list"),
	},

	// --- POS / TPV (pos) ---
	{
		Name: "pos_visits_list", Description: "Lista visitas POS del restaurante activo (opcional filtrar por estado).",
		Schema: json.RawMessage(`{"type":"object","properties":{"status":{"type":"string"}}}`), BackofficeOnly: true, Section: "pos",
		Handler: func(s *Server, ctx context.Context, rid int, input json.RawMessage) (string, error) {
			return s.assistantPOSVisitsList(ctx, rid, input)
		},
	},
	{
		Name: "pos_tickets_list", Description: "Lista tickets POS del restaurante activo (opcional filtrar por estado).",
		Schema: json.RawMessage(`{"type":"object","properties":{"status":{"type":"string"}}}`), BackofficeOnly: true, Section: "pos",
		Handler: func(s *Server, ctx context.Context, rid int, input json.RawMessage) (string, error) {
			return s.assistantPOSTicketsList(ctx, rid, input)
		},
	},
	{
		Name: "pos_cash_closures_list", Description: "Lista los cierres de caja POS del restaurante activo.",
		Schema: json.RawMessage(`{"type":"object","properties":{"shift_id":{"type":"integer"}}}`), BackofficeOnly: true, Section: "pos",
		Handler: func(s *Server, ctx context.Context, rid int, input json.RawMessage) (string, error) {
			return s.assistantPOSCashClosuresList(ctx, rid, input)
		},
	},
	{
		Name: "pos_cash_summary", Description: "Resumen de caja POS del turno activo del restaurante.",
		Schema: json.RawMessage(`{"type":"object","properties":{"shift_id":{"type":"integer"}}}`), BackofficeOnly: true, Section: "pos",
		Handler: func(s *Server, ctx context.Context, rid int, input json.RawMessage) (string, error) {
			return s.assistantPOSCashSummary(ctx, rid, input)
		},
	},
	{
		Name: "pos_cash_closure_create", Description: "Cierra el turno de caja POS del restaurante activo. Requiere confirmed=true.", Write: true, Confirm: true,
		Schema:  json.RawMessage(`{"type":"object","properties":{"shift_id":{"type":"integer"},"terminal_key":{"type":"string"},"closure_type":{"type":"string","enum":["X","Y","Z"]},"counted_cash_cents":{"type":"integer"},"note":{"type":"string"},"discrepancy_reason":{"type":"string"},"idempotency_key":{"type":"string"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["closure_type","idempotency_key","confirmed"]}`),
		Section: "pos", BackofficeOnly: true,
		Handler: func(s *Server, ctx context.Context, rid int, input json.RawMessage) (string, error) {
			return s.assistantPOSCashClosureCreate(ctx, rid, input)
		},
	},
	{
		Name: "pos_visit_create", Description: "Abre una visita POS en el restaurante activo. Requiere confirmación.", Write: true, Confirm: true,
		Schema:         json.RawMessage(`{"type":"object","properties":{"channel":{"type":"string"},"covers":{"type":"integer"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["channel","covers","confirmed"]}`),
		BackofficeOnly: true, Section: "pos",
		Handler: posMutationHandler("pos_visit_create"),
	},
	{
		Name: "pos_ticket_create", Description: "Crea un ticket POS para una visita propia. Requiere confirmación.", Write: true, Confirm: true,
		Schema:         json.RawMessage(`{"type":"object","properties":{"visit_id":{"type":"integer"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["visit_id","confirmed"]}`),
		BackofficeOnly: true, Section: "pos",
		Handler: posMutationHandler("pos_ticket_create"),
	},
	{
		Name: "pos_ticket_line_add", Description: "Añade una línea de producto a un ticket POS abierto del restaurante activo. Requiere confirmed=true.", Write: true, Confirm: true,
		Schema:  json.RawMessage(`{"type":"object","properties":{"ticket_id":{"type":"integer"},"product_id":{"type":"integer"},"quantity":{"type":"number"},"notes":{"type":"string"},"idempotency_key":{"type":"string"},"unit_price_override_cents":{"type":"integer"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["ticket_id","product_id","quantity","idempotency_key","confirmed"]}`),
		Section: "pos", BackofficeOnly: true,
		Handler: func(s *Server, ctx context.Context, rid int, input json.RawMessage) (string, error) {
			return s.assistantPOSTicketLineAdd(ctx, rid, input)
		},
	},
	{
		Name: "pos_payment_create", Description: "Registra un pago POS en ticket del restaurante activo. Requiere confirmación.", Write: true, Confirm: true,
		Schema:         json.RawMessage(`{"type":"object","properties":{"ticket_id":{"type":"integer"},"method":{"type":"string","enum":["CASH","CARD","BANK","OTHER"]},"amount_cents":{"type":"integer"},"idempotency_key":{"type":"string"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["ticket_id","method","amount_cents","idempotency_key","confirmed"]}`),
		BackofficeOnly: true, Section: "pos",
		Handler: posMutationHandler("pos_payment_create"),
	},
	{
		Name: "pos_refund_create", Description: "Reembolsa un ticket POS. Requiere confirmación.", Write: true, Confirm: true,
		Schema:         json.RawMessage(`{"type":"object","properties":{"ticket_id":{"type":"integer"},"amount_cents":{"type":"integer"},"reason":{"type":"string"},"payment_method":{"type":"string","enum":["CASH","CARD","BANK","OTHER"]},"idempotency_key":{"type":"string"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["ticket_id","amount_cents","reason","payment_method","idempotency_key","confirmed"]}`),
		BackofficeOnly: true, Section: "pos",
		Handler: posMutationHandler("pos_refund_create"),
	},

	// --- Miembros (miembros) ---
	{
		Name: "members_list", Description: "Lista los miembros (personal) activos del restaurante activo.",
		Schema: json.RawMessage(`{"type":"object","properties":{}}`), BackofficeOnly: true, Section: "miembros",
		Handler: func(s *Server, ctx context.Context, rid int, input json.RawMessage) (string, error) {
			return s.assistantMembersList(ctx, rid, input)
		},
	},
	{
		Name: "member_get", Description: "Obtiene un miembro del restaurante activo por id.",
		Schema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer"}},"required":["id"]}`), BackofficeOnly: true, Section: "miembros",
		Handler: func(s *Server, ctx context.Context, rid int, input json.RawMessage) (string, error) {
			return s.assistantMemberGet(ctx, rid, input)
		},
	},
	{
		Name: "member_balance_get", Description: "Estado de cuenta (balance trimestral) de un miembro del restaurante activo.",
		Schema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer"},"date":{"type":"string"}},"required":["id"]}`), BackofficeOnly: true, Section: "estado_cuenta",
		Handler: func(s *Server, ctx context.Context, rid int, input json.RawMessage) (string, error) {
			return s.assistantMemberBalanceGet(ctx, rid, input)
		},
	},
	{
		Name: "member_compensation_create", Description: "Registra un periodo salarial/compensación para un miembro del restaurante activo. Requiere confirmed=true.", Write: true, Confirm: true,
		Schema:  json.RawMessage(`{"type":"object","properties":{"member_id":{"type":"integer"},"pay_type":{"type":"string"},"gross_amount":{"type":"number"},"monthly_hours":{"type":"number"},"employer_cost_pct":{"type":"number"},"effective_from":{"type":"string"},"effective_to":{"type":"string"},"notes":{"type":"string"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["member_id","pay_type","gross_amount","effective_from","confirmed"]}`),
		Section: "miembros", BackofficeOnly: true,
		Handler: func(s *Server, ctx context.Context, rid int, input json.RawMessage) (string, error) {
			return s.assistantMemberCompensationCreate(ctx, rid, input)
		},
	},

	// --- Invoices (facturas) ---
	{
		Name: "invoices_list", Description: "Lista facturas del restaurante activo con filtros (búsqueda, estado, rango de fechas).",
		Schema: json.RawMessage(`{"type":"object","properties":{"search":{"type":"string"},"status":{"type":"string"},"date_from":{"type":"string"},"date_to":{"type":"string"},"page":{"type":"integer"},"limit":{"type":"integer"}}}`), BackofficeOnly: true, Section: "facturas",
		Handler: func(s *Server, ctx context.Context, rid int, input json.RawMessage) (string, error) {
			return s.assistantInvoicesList(ctx, rid, input)
		},
	},
	{
		Name: "invoice_get", Description: "Obtiene una factura del restaurante activo por id.",
		Schema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer"}},"required":["id"]}`), BackofficeOnly: true, Section: "facturas",
		Handler: func(s *Server, ctx context.Context, rid int, input json.RawMessage) (string, error) {
			return s.assistantInvoiceGet(ctx, rid, input)
		},
	},

	// --- Platform / settings (plataforma) ---
	{
		Name: "restaurant_settings_get", Description: "Lee configuración del restaurante activo.",
		Schema: json.RawMessage(`{"type":"object","properties":{}}`), BackofficeOnly: true, Section: "plataforma",
		Handler: catalogHandler("restaurant_settings_get"),
	},
	{
		Name: "integrations_get", Description: "Lee la configuración de integraciones del restaurante activo.",
		Schema: json.RawMessage(`{"type":"object","properties":{}}`), BackofficeOnly: true, Section: "plataforma",
		Handler: func(s *Server, ctx context.Context, rid int, input json.RawMessage) (string, error) {
			return s.assistantIntegrationsGet(ctx, rid, input)
		},
	},
	{
		Name: "branding_get", Description: "Lee el branding (logo, colores, marca) del restaurante activo.",
		Schema: json.RawMessage(`{"type":"object","properties":{}}`), BackofficeOnly: true, Section: "plataforma",
		Handler: func(s *Server, ctx context.Context, rid int, input json.RawMessage) (string, error) {
			return s.assistantBrandingGet(ctx, rid, input)
		},
	},
	{
		Name: "whatsapp_bot_config_get", Description: "Lee configuración del bot WhatsApp del restaurante activo.",
		Schema: json.RawMessage(`{"type":"object","properties":{}}`), BackofficeOnly: true, Section: "plataforma",
		Handler: catalogHandler("whatsapp_bot_config_get"),
	},
	{
		Name: "whatsapp_bot_config_update", Description: "Actualiza la configuración del bot WhatsApp del restaurante activo. Requiere confirmed=true.", Write: true, Confirm: true,
		Schema:  json.RawMessage(`{"type":"object","properties":{"model":{"type":"string"},"language_default":{"type":"string"},"tone":{"type":"string"},"greeting_style":{"type":"string"},"disable_attachments":{"type":"boolean"},"custom_instructions":{"type":"string"},"contact_phone":{"type":"string"},"rules":{"type":"string"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["confirmed"]}`),
		Section: "plataforma", BackofficeOnly: true,
		Handler: func(s *Server, ctx context.Context, rid int, input json.RawMessage) (string, error) {
			return s.assistantWhatsappBotConfigUpdate(ctx, rid, input)
		},
	},
	{
		Name: "site_published_content_get", Description: "Lee contenido publicado del sitio del restaurante activo.",
		Schema: json.RawMessage(`{"type":"object","properties":{}}`), BackofficeOnly: true, Section: "plataforma",
		Handler: catalogHandler("site_published_content_get"),
	},
	{
		Name: "site_publish", Description: "Publica el sitio del restaurante activo (crea versión publicada). Requiere confirmed=true.", Write: true, Confirm: true,
		Schema:  json.RawMessage(`{"type":"object","properties":{"site_id":{"type":"string"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["site_id","confirmed"]}`),
		Section: "website", BackofficeOnly: true,
		Handler: func(s *Server, ctx context.Context, rid int, input json.RawMessage) (string, error) {
			return s.assistantSitePublish(ctx, rid, input)
		},
	},
}

// catalogHandler binds one of the shared catalog/typed-domain read handlers to a
// fixed tool name (the handler still dispatches internally by that name).
func catalogHandler(name string) assistantToolHandler {
	return func(s *Server, ctx context.Context, rid int, input json.RawMessage) (string, error) {
		return s.assistantCatalogTool(ctx, rid, name, input)
	}
}

// posMutationHandler binds a POS mutation to its registry entry.
func posMutationHandler(name string) assistantToolHandler {
	return func(s *Server, ctx context.Context, rid int, input json.RawMessage) (string, error) {
		return s.assistantPOSMutation(ctx, rid, name, input)
	}
}

// assistantToolLookup returns the registry entry for name.
func assistantToolLookup(name string) (assistantTool, bool) {
	for _, t := range assistantToolRegistry {
		if t.Name == name {
			return t, true
		}
	}
	return assistantTool{}, false
}

// assistantToolDefs exposes the registry as the tool list sent to the model.
func assistantToolDefs() []assistantToolDef {
	defs := make([]assistantToolDef, 0, len(assistantToolRegistry))
	for _, t := range assistantToolRegistry {
		defs = append(defs, assistantToolDef{Name: t.Name, Description: t.Description, InputSchema: t.Schema})
	}
	return defs
}

// assistantToolWrites reports whether name is a mutation tool.
func assistantToolWrites(name string) bool {
	t, ok := assistantToolLookup(name)
	return ok && t.Write
}

// assistantToolConfirmRequired reports whether name requires a confirmation token.
func assistantToolConfirmRequired(name string) bool {
	t, ok := assistantToolLookup(name)
	return ok && t.Confirm
}

// ToolDoc is the registry metadata exported for documentation generation.
type ToolDoc struct {
	Name           string
	Description    string
	Section        string
	Write          bool
	Confirm        bool
	BackofficeOnly bool
	Schema         string
}

// ToolDocs returns the registry metadata (ordered) for docs/tooling.
func ToolDocs() []ToolDoc {
	out := make([]ToolDoc, 0, len(assistantToolRegistry))
	for _, t := range assistantToolRegistry {
		out = append(out, ToolDoc{
			Name: t.Name, Description: t.Description, Section: t.Section,
			Write: t.Write, Confirm: t.Confirm, BackofficeOnly: t.BackofficeOnly,
			Schema: string(t.Schema),
		})
	}
	return out
}
