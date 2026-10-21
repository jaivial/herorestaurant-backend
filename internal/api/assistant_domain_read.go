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
	handler  func(*Server) http.HandlerFunc
	urlParam string            // chi URLParam fed from input id ("" = resource takes no id)
	query    map[string]string // typed input field -> handler query param
	desc     string
}

type domainReadInput struct {
	Resource  string `json:"resource"`
	ID        string `json:"id,omitempty"`
	Date      string `json:"date,omitempty"`
	DateFrom  string `json:"date_from,omitempty"`
	DateTo    string `json:"date_to,omitempty"`
	ShiftID   string `json:"shift_id,omitempty"`
	StationID string `json:"station_id,omitempty"`
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
	body, code, err := s.assistantCallHandler(ctx, spec.handler(s), call)
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
	"shift_current":    {handler: func(s *Server) http.HandlerFunc { return s.handleBOPOSShiftCurrent }, desc: "turno POS abierto ahora"},
	"cash_day_current": {handler: func(s *Server) http.HandlerFunc { return s.handleBOPOSCashDayCurrent }, query: map[string]string{"date": "date"}, desc: "caja del día (por defecto hoy)"},
	"cash_days_range":  {handler: func(s *Server) http.HandlerFunc { return s.handleBOPOSCashDaysRange }, query: map[string]string{"date_from": "from", "date_to": "to"}, desc: "resumen de cajas por rango"},
	"cash_movements":   {handler: func(s *Server) http.HandlerFunc { return s.handleBOPOSCashMovements }, query: map[string]string{"shift_id": "shiftId"}, desc: "movimientos de caja de un turno"},
	"cash_closure_get": {handler: func(s *Server) http.HandlerFunc { return s.handleBOPOSCashClosureGet }, urlParam: "id", desc: "detalle de un cierre de caja"},
	"tickets_series":   {handler: func(s *Server) http.HandlerFunc { return s.handleBOPOSTicketsSeries }, query: map[string]string{"date": "date"}, desc: "series/estado de tickets de un día"},
	"tickets_hourly":   {handler: func(s *Server) http.HandlerFunc { return s.handleBOPOSTicketsHourly }, query: map[string]string{"date": "date"}, desc: "tickets por hora de un día"},
	"sales_report":     {handler: func(s *Server) http.HandlerFunc { return s.handleBOPOSSalesReport }, query: map[string]string{"date_from": "from", "date_to": "to"}, desc: "ventas por día y servicio (por defecto últimos 30 días)"},
	"covers_report":    {handler: func(s *Server) http.HandlerFunc { return s.handleBOPOSCoversReport }, query: map[string]string{"date_from": "from", "date_to": "to"}, desc: "comensales por rango"},
	"products_list":    {handler: func(s *Server) http.HandlerFunc { return s.handleBOPOSProductsList }, desc: "catálogo POS de productos"},
	"kitchen_queue":    {handler: func(s *Server) http.HandlerFunc { return s.handleBOPOSKitchenQueue }, query: map[string]string{"station_id": "stationId"}, desc: "cola de cocina"},
	"service_periods":  {handler: func(s *Server) http.HandlerFunc { return s.handleBOPOSServicePeriodsList }, desc: "servicios de comedor configurados"},
}
