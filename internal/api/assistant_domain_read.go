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
	gates    func(*Server) []func(http.Handler) http.Handler
	urlParam string            // chi URLParam fed from input id ("" = resource takes no id)
	query    map[string]string // typed input field -> handler query param
	desc     string
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
