package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
)

// Coordination id: forky_domain_read_v1
//
// One reusable read core for the long tail of backoffice GET endpoints with
// no dedicated tool. Each domain tool (pos_read, ...) declares an explicit
// allowlist of resources, and every resource reuses the EXISTING backoffice
// handler through assistantCallHandler, so tenant scoping and business logic
// come from the handler itself. Only handlers verified as side-effect-free
// GETs with session-safe output are allowlisted. Tool params are typed in the
// registry schema and mapped per resource; anything else fails closed.
// Mutating GETs and endpoints carrying QR/codes/PIN/tokens/secrets or bulk
// PII exports are NOT allowlisted by design.

type domainReadSpec struct {
	handler func(*Server) http.HandlerFunc
	// gates replays the exact middleware chain the route applies in
	// server.go (feature flag, permission/section/role gates) before the
	// handler runs, so tool access can never exceed HTTP route access.
	gates     func(*Server) []func(http.Handler) http.Handler
	urlParam  string            // chi URLParam fed from input id ("" = resource takes no id)
	urlParam2 string            // second chi URLParam fed from input section_id ("" = none)
	query     map[string]string // typed input field -> handler query param
	desc      string
}

type domainReadInput struct {
	Resource   string `json:"resource"`
	ID         string `json:"id,omitempty"`
	Date       string `json:"date,omitempty"`
	DateFrom   string `json:"date_from,omitempty"`
	DateTo     string `json:"date_to,omitempty"`
	ShiftID    string `json:"shift_id,omitempty"`
	StationID  string `json:"station_id,omitempty"`
	Q          string `json:"q,omitempty"`
	Days       string `json:"days,omitempty"`
	Scenario   string `json:"scenario,omitempty"`
	CategoryID string `json:"category_id,omitempty"`
	FoodType   string `json:"food_type,omitempty"`
	ScopeKey   string `json:"scope_key,omitempty"`
	ScopeKind  string `json:"scope_kind,omitempty"`
	Year       string `json:"year,omitempty"`
	View       string `json:"view,omitempty"`
	Name       string `json:"name,omitempty"`
	PartySize  string `json:"party_size,omitempty"`
	Phone      string `json:"phone,omitempty"`
	Time       string `json:"time,omitempty"`
	Status     string `json:"status,omitempty"`
	Drafts     string `json:"include_drafts,omitempty"`
	SectionID  string `json:"section_id,omitempty"`
}

func (in domainReadInput) fields() map[string]string {
	m := map[string]string{}
	if in.ID != "" {
		m["id"] = in.ID
	}
	if in.Date != "" {
		m["date"] = in.Date
	}
	if in.DateFrom != "" {
		m["date_from"] = in.DateFrom
	}
	if in.DateTo != "" {
		m["date_to"] = in.DateTo
	}
	if in.ShiftID != "" {
		m["shift_id"] = in.ShiftID
	}
	if in.StationID != "" {
		m["station_id"] = in.StationID
	}
	if in.Q != "" {
		m["q"] = in.Q
	}
	if in.Days != "" {
		m["days"] = in.Days
	}
	if in.Scenario != "" {
		m["scenario"] = in.Scenario
	}
	if in.CategoryID != "" {
		m["category_id"] = in.CategoryID
	}
	if in.FoodType != "" {
		m["food_type"] = in.FoodType
	}
	if in.ScopeKey != "" {
		m["scope_key"] = in.ScopeKey
	}
	if in.ScopeKind != "" {
		m["scope_kind"] = in.ScopeKind
	}
	if in.Year != "" {
		m["year"] = in.Year
	}
	if in.View != "" {
		m["view"] = in.View
	}
	if in.Name != "" {
		m["name"] = in.Name
	}
	if in.PartySize != "" {
		m["party_size"] = in.PartySize
	}
	if in.Phone != "" {
		m["phone"] = in.Phone
	}
	if in.Time != "" {
		m["time"] = in.Time
	}
	if in.Status != "" {
		m["status"] = in.Status
	}
	if in.Drafts != "" {
		m["include_drafts"] = in.Drafts
	}
	if in.SectionID != "" {
		m["section_id"] = in.SectionID
	}
	return m
}

func (s *Server) assistantDomainRead(ctx context.Context, tool string, resources map[string]domainReadSpec, raw json.RawMessage) (string, error) {
	var in domainReadInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", err
	}
	spec, ok := resources[in.Resource]
	if !ok {
		names := make([]string, 0, len(resources))
		for k := range resources {
			names = append(names, k)
		}
		sort.Strings(names)
		return botJSON(map[string]any{"error": "recurso no permitido", "allowed": names}), nil
	}
	query := map[string]string{}
	for field, value := range in.fields() {
		if field == "id" {
			if spec.urlParam == "" {
				return botJSON(map[string]any{"error": fmt.Sprintf("el recurso %s no admite id", in.Resource)}), nil
			}
			continue
		}
		if field == "section_id" {
			if spec.urlParam2 == "" {
				return botJSON(map[string]any{"error": fmt.Sprintf("el recurso %s no admite section_id", in.Resource)}), nil
			}
			continue
		}
		q, ok := spec.query[field]
		if !ok {
			return botJSON(map[string]any{"error": fmt.Sprintf("el recurso %s no admite el parámetro %s", in.Resource, field)}), nil
		}
		query[q] = value
	}
	call := assistantHandlerInput{Method: http.MethodGet, Query: query}
	if spec.urlParam != "" {
		if in.ID == "" {
			return botJSON(map[string]any{"error": fmt.Sprintf("el recurso %s requiere id", in.Resource)}), nil
		}
		call.URLParam = map[string]string{spec.urlParam: in.ID}
	}
	if spec.urlParam2 != "" {
		if in.SectionID == "" {
			return botJSON(map[string]any{"error": fmt.Sprintf("el recurso %s requiere section_id", in.Resource)}), nil
		}
		if call.URLParam == nil {
			call.URLParam = map[string]string{}
		}
		call.URLParam[spec.urlParam2] = in.SectionID
	}
	body, code, err := s.assistantCallHandler(ctx, gatedDomainHandler(s, spec), call)
	if err != nil {
		return "", err
	}
	if code < 200 || code >= 300 {
		return "", assistantHandlerError(tool+" "+in.Resource, body, code)
	}
	return botHandlerResponse(tool, body)
}

// POS reads. Every handler verified: pure GET (no Exec/DML), tenant from the
// session boAuth, output = own-tenant aggregates/catalog, no PII/secrets.
var posReadResources = map[string]domainReadSpec{
	"shift_current":    {gates: posReadGates(posPermissionView), handler: func(s *Server) http.HandlerFunc { return s.handleBOPOSShiftCurrent }, desc: "turno POS abierto ahora"},
	"cash_day_current": {gates: posReadGates(posPermissionView), handler: func(s *Server) http.HandlerFunc { return s.handleBOPOSCashDayCurrent }, query: map[string]string{"date": "date"}, desc: "caja del día (por defecto hoy)"},
	"cash_days_range":  {gates: posReadGates(posPermissionView), handler: func(s *Server) http.HandlerFunc { return s.handleBOPOSCashDaysRange }, query: map[string]string{"date_from": "from", "date_to": "to"}, desc: "resumen de cajas por rango"},
	"cash_movements":   {gates: posReadGates(posPermissionReports), handler: func(s *Server) http.HandlerFunc { return s.handleBOPOSCashMovements }, query: map[string]string{"shift_id": "shiftId"}, desc: "movimientos de caja de un turno"},
	"cash_closure_get": {gates: posReadGates(posPermissionReports), handler: func(s *Server) http.HandlerFunc { return s.handleBOPOSCashClosureGet }, urlParam: "id", desc: "detalle de un cierre de caja"},
	"tickets_series":   {gates: posViewOrFichajeReadGates(), handler: func(s *Server) http.HandlerFunc { return s.handleBOPOSTicketsSeries }, query: map[string]string{"date": "date"}, desc: "series/estado de tickets de un día"},
	"tickets_hourly":   {gates: posViewOrFichajeReadGates(), handler: func(s *Server) http.HandlerFunc { return s.handleBOPOSTicketsHourly }, query: map[string]string{"date": "date"}, desc: "tickets por hora de un día"},
	"sales_report":     {gates: posReadGates(posPermissionReports), handler: func(s *Server) http.HandlerFunc { return s.handleBOPOSSalesReport }, query: map[string]string{"date_from": "from", "date_to": "to"}, desc: "ventas por día y servicio (por defecto últimos 30 días)"},
	"covers_report":    {gates: posReadGates(posPermissionReports), handler: func(s *Server) http.HandlerFunc { return s.handleBOPOSCoversReport }, query: map[string]string{"date_from": "from", "date_to": "to"}, desc: "comensales por rango"},
	"products_list":    {gates: posReadGates(posPermissionView), handler: func(s *Server) http.HandlerFunc { return s.handleBOPOSProductsList }, desc: "catálogo POS de productos"},
	"kitchen_queue":    {gates: posReadGates(posPermissionView), handler: func(s *Server) http.HandlerFunc { return s.handleBOPOSKitchenQueue }, query: map[string]string{"station_id": "stationId"}, desc: "cola de cocina"},
	"service_periods":  {gates: posReadGates(posPermissionView), handler: func(s *Server) http.HandlerFunc { return s.handleBOPOSServicePeriodsList }, desc: "servicios de comedor configurados"},
}

// gatedDomainHandler wraps a domain-read handler with the same gate
// middleware the original HTTP route uses, evaluated against the real
// backoffice session in ctx (fail closed, same as the route).
func gatedDomainHandler(s *Server, spec domainReadSpec) http.HandlerFunc {
	h := http.Handler(http.HandlerFunc(spec.handler(s)))
	if spec.gates != nil {
		for _, g := range spec.gates(s) {
			h = g(h)
		}
	}
	return h.ServeHTTP
}

func posReadGates(perms ...string) func(*Server) []func(http.Handler) http.Handler {
	return func(s *Server) []func(http.Handler) http.Handler {
		gs := []func(http.Handler) http.Handler{s.requireBOPOSFeature}
		for _, p := range perms {
			gs = append(gs, s.requireBOPOSPermission(p))
		}
		return gs
	}
}

func posViewOrFichajeReadGates() func(*Server) []func(http.Handler) http.Handler {
	return func(s *Server) []func(http.Handler) http.Handler {
		return []func(http.Handler) http.Handler{s.requireBOPOSFeature, s.requireBOPOSViewOrFichajeAdmin}
	}
}

func stockReadGates(perms ...string) func(*Server) []func(http.Handler) http.Handler {
	return func(s *Server) []func(http.Handler) http.Handler {
		gs := make([]func(http.Handler) http.Handler, 0, len(perms))
		for _, p := range perms {
			gs = append(gs, s.requireBOStockPermission(p))
		}
		return gs
	}
}

// stockAdminReadGates mirrors routes gated by a stock permission AND the
// backoffice admin-role gate (requireBORoleImportanceAtLeast(90)).
func stockAdminReadGates(perms ...string) func(*Server) []func(http.Handler) http.Handler {
	return func(s *Server) []func(http.Handler) http.Handler {
		gs := make([]func(http.Handler) http.Handler, 0, len(perms)+1)
		for _, p := range perms {
			gs = append(gs, s.requireBOStockPermission(p))
		}
		return append(gs, s.requireBORoleImportanceAtLeast(90))
	}
}

// sectionReadGates replays routes gated purely by backoffice section(s).
func sectionReadGates(sections ...string) func(*Server) []func(http.Handler) http.Handler {
	return func(s *Server) []func(http.Handler) http.Handler {
		gs := make([]func(http.Handler) http.Handler, 0, len(sections))
		for _, sec := range sections {
			gs = append(gs, s.requireBOSection(sec))
		}
		return gs
	}
}

// sectionAdminGates mirrors routes gated by a backoffice section AND the
// admin-role gate (members, fichaje labour data).
func sectionAdminGates(section string) func(*Server) []func(http.Handler) http.Handler {
	return func(s *Server) []func(http.Handler) http.Handler {
		return []func(http.Handler) http.Handler{s.requireBOSection(section), s.requireBORoleImportanceAtLeast(90)}
	}
}

// Stock reads, same audit: pure GETs, session tenant, own-tenant data, no
// secrets/PII. stock role-permissions config stays out (authorization config).
var stockReadResources = map[string]domainReadSpec{
	"valuation":                 {gates: stockReadGates(stockPermissionView), handler: func(s *Server) http.HandlerFunc { return s.handleBOStockValuation }, desc: "valoración del inventario"},
	"vat_list":                  {gates: stockReadGates(stockPermissionCostsView), handler: func(s *Server) http.HandlerFunc { return s.handleBOStockVATList }, desc: "tipos de IVA de stock"},
	"suppliers_list":            {gates: stockReadGates(stockPermissionView), handler: func(s *Server) http.HandlerFunc { return s.handleBOStockSuppliersList }, desc: "proveedores (id, nombre, notas)"},
	"supplier_prices":           {gates: stockReadGates(stockPermissionView), handler: func(s *Server) http.HandlerFunc { return s.handleBOStockSupplierPricesGet }, urlParam: "id", query: map[string]string{"days": "days"}, desc: "precios de un proveedor"},
	"supplier_aliases":          {gates: stockReadGates(stockPermissionView), handler: func(s *Server) http.HandlerFunc { return s.handleBOStockSupplierAliasesGet }, urlParam: "id", desc: "alias de producto de un proveedor"},
	"settings":                  {gates: stockReadGates(stockPermissionView), handler: func(s *Server) http.HandlerFunc { return s.handleBOStockSettingsGet }, desc: "configuración operativa de stock"},
	"reconciliation":            {gates: stockReadGates(stockPermissionView), handler: func(s *Server) http.HandlerFunc { return s.handleBOStockReconciliationGet }, desc: "diferencias niveles vs movimientos"},
	"recipe_get":                {gates: stockReadGates(stockPermissionRecipesView), handler: func(s *Server) http.HandlerFunc { return s.handleBOStockRecipeGet }, urlParam: "id", desc: "detalle de una receta"},
	"margin_targets":            {gates: stockReadGates(stockPermissionCostsView), handler: func(s *Server) http.HandlerFunc { return s.handleBOStockMarginTargets }, desc: "objetivos de margen"},
	"margin_scopes":             {gates: stockReadGates(stockPermissionCostsView), handler: func(s *Server) http.HandlerFunc { return s.handleBOStockMarginScopesList }, desc: "ámbitos de margen"},
	"margin_scope_resolve":      {gates: stockReadGates(stockPermissionCostsView), handler: func(s *Server) http.HandlerFunc { return s.handleBOStockMarginScopeResolve }, query: map[string]string{"category_id": "categoryId", "food_type": "foodType", "scope_key": "scopeKey", "scope_kind": "scopeKind"}, desc: "resuelve el margen aplicable"},
	"margin_scope_defaults":     {gates: stockReadGates(stockPermissionCostsView), handler: func(s *Server) http.HandlerFunc { return s.handleBOStockMarginScopeDefaults }, desc: "bandas de margen por defecto"},
	"labour_members":            {gates: stockReadGates(stockPermissionCostsManage), handler: func(s *Server) http.HandlerFunc { return s.handleBOStockLabourMembers }, desc: "miembros con coste laboral"},
	"item_units":                {gates: stockReadGates(stockPermissionView), handler: func(s *Server) http.HandlerFunc { return s.handleBOStockItemUnitsList }, urlParam: "id", desc: "unidades de un artículo"},
	"item_options":              {gates: stockReadGates(stockPermissionView), handler: func(s *Server) http.HandlerFunc { return s.handleBOStockItemOptions }, query: map[string]string{"q": "q"}, desc: "opciones de artículos (búsqueda q)"},
	"forecast":                  {gates: stockReadGates(stockPermissionForecastView), handler: func(s *Server) http.HandlerFunc { return s.handleBOStockForecast }, query: map[string]string{"scenario": "scenario"}, desc: "previsión de consumo"},
	"expiring":                  {gates: stockReadGates(stockPermissionView), handler: func(s *Server) http.HandlerFunc { return s.handleBOStockExpiring }, query: map[string]string{"days": "days"}, desc: "stock por caducar (days)"},
	"documents_list":            {gates: stockReadGates(stockPermissionOCRUpload), handler: func(s *Server) http.HandlerFunc { return s.handleBOStockDocumentsList }, desc: "documentos escaneados (metadatos)"},
	"count_get":                 {gates: stockReadGates(stockPermissionView), handler: func(s *Server) http.HandlerFunc { return s.handleBOStockCountGet }, urlParam: "id", desc: "detalle de un conteo"},
	"costing":                   {gates: stockReadGates(stockPermissionCostsView), handler: func(s *Server) http.HandlerFunc { return s.handleBOStockCosting }, desc: "costes por receta/plato"},
	"permissions_mine":          {handler: func(s *Server) http.HandlerFunc { return s.handleBOStockPermissionsMine }, desc: "mis permisos de stock"},
	"production_labour_list":    {gates: stockAdminReadGates(stockPermissionCostsView), handler: func(s *Server) http.HandlerFunc { return s.handleBOProductionLabourList }, urlParam: "id", desc: "costes laborales de una orden de producción"},
	"production_labour_entries": {gates: stockAdminReadGates(stockPermissionCostsView), handler: func(s *Server) http.HandlerFunc { return s.handleBOProductionLabourEntries }, desc: "entradas laborales de producción"},
}

// Member reads (club de miembros): asistencia/estadísticas propias del tenant.
var membersReadResources = map[string]domainReadSpec{
	"stats":              {urlParam: "id", gates: sectionAdminGates(boSectionMiembros), handler: func(s *Server) http.HandlerFunc { return s.handleBOMemberStats }, query: map[string]string{"date": "date", "view": "view"}, desc: "estadísticas de miembros (date, view)"},
	"stats_year":         {urlParam: "id", gates: sectionAdminGates(boSectionMiembros), handler: func(s *Server) http.HandlerFunc { return s.handleBOMemberStatsYear }, query: map[string]string{"year": "year"}, desc: "estadísticas de miembros por año"},
	"stats_range":        {urlParam: "id", gates: sectionAdminGates(boSectionMiembros), handler: func(s *Server) http.HandlerFunc { return s.handleBOMemberStatsRange }, query: map[string]string{"date_from": "from", "date_to": "to"}, desc: "estadísticas de miembros por rango"},
	"quarter_balance":    {urlParam: "id", gates: sectionAdminGates(boSectionMiembros), handler: func(s *Server) http.HandlerFunc { return s.handleBOMemberQuarterBalance }, query: map[string]string{"date": "date"}, desc: "balance trimestral de miembros"},
	"table_data":         {urlParam: "id", gates: sectionAdminGates(boSectionMiembros), handler: func(s *Server) http.HandlerFunc { return s.handleBOMemberTableData }, query: map[string]string{"view": "view", "year": "year"}, desc: "tabla de datos de miembros (view, year)"},
	"compensations_list": {urlParam: "id", gates: sectionAdminGates(boSectionMiembros), handler: func(s *Server) http.HandlerFunc { return s.handleBOMemberCompensationsList }, desc: "compensaciones de miembros"},
}

// Fichaje reads: costes laborales del tenant.
var fichajeReadResources = map[string]domainReadSpec{
	"labour_cost":  {gates: sectionAdminGates(boSectionFichaje), handler: func(s *Server) http.HandlerFunc { return s.handleBOFichajeLabourCost }, query: map[string]string{"date_from": "from", "date_to": "to"}, desc: "coste laboral por rango"},
	"hourly_costs": {gates: sectionAdminGates(boSectionFichaje), handler: func(s *Server) http.HandlerFunc { return s.handleBOFichajeHourlyCosts }, query: map[string]string{"date": "date"}, desc: "costes por hora de un día"},
}

// Technical-sheet reads (fichas técnicas): recetas/alérgenos del tenant.
var sheetsReadResources = map[string]domainReadSpec{
	"list":           {gates: stockReadGates(stockPermissionSheetsView), handler: func(s *Server) http.HandlerFunc { return s.handleBOTechnicalSheetList }, query: map[string]string{"category_id": "categoryId", "q": "q", "status": "status"}, desc: "fichas técnicas (filtros category_id, q, status)"},
	"get":            {gates: stockReadGates(stockPermissionSheetsView), handler: func(s *Server) http.HandlerFunc { return s.handleBOTechnicalSheetGet }, urlParam: "id", desc: "detalle de una ficha técnica"},
	"cost":           {gates: stockReadGates(stockPermissionSheetsView), handler: func(s *Server) http.HandlerFunc { return s.handleBOTechnicalSheetCost }, urlParam: "id", desc: "coste de una ficha técnica"},
	"components":     {gates: stockReadGates(stockPermissionSheetsView), handler: func(s *Server) http.HandlerFunc { return s.handleBOTechnicalSheetComponentsList }, urlParam: "id", desc: "componentes de una ficha"},
	"allergens":      {gates: stockReadGates(stockPermissionSheetsView), handler: func(s *Server) http.HandlerFunc { return s.handleBOTechnicalSheetAllergensGet }, urlParam: "id", desc: "alérgenos de una ficha"},
	"usage":          {gates: stockReadGates(stockPermissionSheetsView), handler: func(s *Server) http.HandlerFunc { return s.handleBOTechnicalSheetUsage }, urlParam: "id", desc: "dónde se usa una ficha"},
	"steps":          {gates: stockReadGates(stockPermissionSheetsView), handler: func(s *Server) http.HandlerFunc { return s.handleBOTechnicalSheetStepsList }, urlParam: "id", desc: "pasos de elaboración"},
	"step_image_job": {gates: stockReadGates(stockPermissionSheetsView), handler: func(s *Server) http.HandlerFunc { return s.handleBOTechnicalSheetStepImageJobGet }, urlParam: "stepId", desc: "estado del job de imagen de un paso (id = stepId del paso)"},
}

// Menu reads: conteos, config de carta y menús de grupo del tenant. Los GETs
// de categorías y el GET de menú v2 siembran filas (ensure*) y NO se
// allowlistan; las categorías se leen vía catalog comida.
var menusReadResources = map[string]domainReadSpec{
	"comida_counts":                 {gates: sectionReadGates(boSectionMenus), handler: func(s *Server) http.HandlerFunc { return s.handleBOComidaCounts }, desc: "conteos de la carta"},
	"arroz_types":                   {gates: sectionReadGates(boSectionReservas), handler: func(s *Server) http.HandlerFunc { return s.handleBOArrozTypes }, desc: "tipos de arroz"},
	"beverage_options":              {gates: sectionReadGates(boSectionMenus), handler: func(s *Server) http.HandlerFunc { return s.handleBOBeverageOptionsList }, urlParam: "id", desc: "opciones de bebida de un menú"},
	"mandatory_menus":               {gates: sectionReadGates(boSectionReservas), handler: func(s *Server) http.HandlerFunc { return s.handleBOMandatoryMenusGet }, query: map[string]string{"date": "date"}, desc: "menús obligatorios por fecha"},
	"menu_selector":                 {gates: sectionReadGates(boSectionMenus), handler: func(s *Server) http.HandlerFunc { return s.handleBOMenuSelectorGet }, desc: "selector de menú actual"},
	"group_menus_list":              {gates: sectionReadGates(boSectionMenus), handler: func(s *Server) http.HandlerFunc { return s.handleBOGroupMenusList }, query: map[string]string{"status": "status"}, desc: "menús de grupo legacy"},
	"group_menu_get":                {gates: sectionReadGates(boSectionMenus), handler: func(s *Server) http.HandlerFunc { return s.handleBOGroupMenuGet }, urlParam: "id", desc: "menú de grupo legacy por id"},
	"group_menus_v2_list":           {gates: sectionReadGates(boSectionMenus), handler: func(s *Server) http.HandlerFunc { return s.handleBOGroupMenusV2List }, query: map[string]string{"include_drafts": "includeDrafts"}, desc: "menús de grupo v2"},
	"group_menus_v2_special":        {gates: sectionReadGates(boSectionMenus), handler: func(s *Server) http.HandlerFunc { return s.handleBOGroupMenusV2ListSpecialSections }, urlParam: "id", desc: "secciones especiales de un menú v2"},
	"group_menus_v2_slider":         {gates: sectionReadGates(boSectionMenus), handler: func(s *Server) http.HandlerFunc { return s.handleBOGroupMenusV2GetSlider }, urlParam: "id", desc: "slider de un menú v2"},
	"group_menus_v2_section_dishes": {gates: sectionReadGates(boSectionMenus), handler: func(s *Server) http.HandlerFunc { return s.handleBOGroupMenusV2GetSectionDishes }, urlParam: "id", urlParam2: "sectionId", desc: "platos de una sección de menú v2 (id + section_id)"},
}

// Reservas reads: cambios y config del tenant.
var reservasReadResources = map[string]domainReadSpec{
	"bookings_modified":     {gates: sectionReadGates(boSectionReservas), handler: func(s *Server) http.HandlerFunc { return s.handleBOBookingsModifiedByDate }, query: map[string]string{"date": "date"}, desc: "reservas modificadas por fecha"},
	"booking_notifications": {gates: sectionAdminGates(boSectionMiembros), handler: func(s *Server) http.HandlerFunc { return s.handleBOBookingNotificationsGet }, desc: "config de notificaciones de reserva"},
	"special_date_stats":    {gates: sectionReadGates(boSectionReservas), handler: func(s *Server) http.HandlerFunc { return s.handleBOSpecialDateStats }, query: map[string]string{"date": "date"}, desc: "estadísticas de una fecha especial"},
}

// Website reads: config pública del tenant (anuncios, widget, plantillas).
var websiteReadResources = map[string]domainReadSpec{
	"widget_settings":   {gates: sectionReadGates(boSectionReservas), handler: func(s *Server) http.HandlerFunc { return s.handleBOWidgetSettingsGet }, desc: "config del widget de reservas"},
	"ads_list":          {gates: sectionReadGates(boSectionAnuncios), handler: func(s *Server) http.HandlerFunc { return s.handleBOAdsList }, desc: "anuncios/popups configurados"},
	"premium_website":   {gates: sectionReadGates(boSectionAjustes), handler: func(s *Server) http.HandlerFunc { return s.handleBOPremiumWebsiteGet }, desc: "web premium del tenant"},
	"website_templates": {gates: sectionReadGates(boSectionAjustes), handler: func(s *Server) http.HandlerFunc { return s.handleBOPremiumWebsiteTemplates }, desc: "catálogo de plantillas web"},
	"menu_templates":    {gates: sectionReadGates(boSectionAjustes), handler: func(s *Server) http.HandlerFunc { return s.handleBOPremiumWebsiteMenuTemplatesGet }, desc: "plantillas de carta web"},
}

// Campaigns reads: contenido de plantilla (la audiencia se excluye: muestra PII).
var campaignsReadResources = map[string]domainReadSpec{
	"template": {gates: func(s *Server) []func(http.Handler) http.Handler {
		return []func(http.Handler) http.Handler{s.requireBOSection(boSectionReservas), s.requireBOCapability(boCapabilityCampanas)}
	}, handler: func(s *Server) http.HandlerFunc { return s.handleBOCampaignTemplate }, desc: "plantilla actual de campaña"},
}

// Invoices reads: búsqueda de reserva para facturar (datos del propio tenant).
var invoicesReadResources = map[string]domainReadSpec{
	"search_reservation": {gates: sectionReadGates(boSectionFacturas), handler: func(s *Server) http.HandlerFunc { return s.handleBOInvoicesSearchReservation }, query: map[string]string{"date_from": "date_from", "date_to": "date_to", "name": "name", "party_size": "party_size", "phone": "phone", "time": "time"}, desc: "buscar reserva para facturar (filtros fecha/nombre/comensales/teléfono/hora)"},
}
