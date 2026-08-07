package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

func assistantToolDefs() []assistantToolDef {
	return []assistantToolDef{
		{Name: "restaurant_info", Description: "Lee datos básicos del restaurante activo.", InputSchema: json.RawMessage(`{"type":"object","properties":{}}`)},
		{Name: "bookings_summary", Description: "Devuelve resumen de reservas del restaurante activo para una fecha opcional.", InputSchema: json.RawMessage(`{"type":"object","properties":{"date":{"type":"string"}}}`)},
		{Name: "restaurant_query", Description: "Consulta datos agregados seguros del restaurante activo. resource: bookings, menus o wines.", InputSchema: json.RawMessage(`{"type":"object","properties":{"resource":{"type":"string","enum":["bookings","menus","wines"]},"date_from":{"type":"string"},"date_to":{"type":"string"}},"required":["resource"]}`)},
	}
}

func (s *Server) assistantExecuteTool(ctx context.Context, restaurantID int, name string, input json.RawMessage) (string, error) {
	if restaurantID <= 0 {
		return "", fmt.Errorf("restaurante activo no disponible")
	}
	var in struct{ Resource, Date, DateFrom, DateTo string }
	_ = json.Unmarshal(input, &in)
	switch name {
	case "restaurant_info":
		var name, phone string
		err := s.db.QueryRowContext(ctx, "SELECT name, phone FROM restaurants WHERE id=?", restaurantID).Scan(&name, &phone)
		if err != nil {
			return "", err
		}
		return botJSON(map[string]any{"restaurant_id": restaurantID, "name": name, "phone": phone}), nil
	case "bookings_summary":
		var total, people int
		q := "SELECT COUNT(*), COALESCE(SUM(party_size),0) FROM bookings WHERE restaurant_id=? AND status NOT IN ('cancelled','canceled')"
		args := []any{restaurantID}
		if strings.TrimSpace(in.Date) != "" {
			q += " AND reservation_date=?"
			args = append(args, in.Date)
		}
		err := s.db.QueryRowContext(ctx, q, args...).Scan(&total, &people)
		if err != nil {
			return "", err
		}
		return botJSON(map[string]any{"total": total, "people": people, "date": in.Date}), nil
	case "restaurant_query":
		switch in.Resource {
		case "bookings":
			return s.assistantBookingSeries(ctx, restaurantID, in.DateFrom, in.DateTo)
		case "menus":
			return s.assistantCount(ctx, restaurantID, "group_menus", "menus")
		case "wines":
			return s.assistantCount(ctx, restaurantID, "wines", "wines")
		}
	}
	return "", fmt.Errorf("herramienta desconocida: %s", name)
}
func (s *Server) assistantCount(ctx context.Context, rid int, table, key string) (string, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+" WHERE restaurant_id=?", rid).Scan(&n); err != nil {
		return "", err
	}
	return botJSON(map[string]any{key: n}), nil
}
func (s *Server) assistantBookingSeries(ctx context.Context, rid int, from, to string) (string, error) {
	q := "SELECT reservation_date, COUNT(*) FROM bookings WHERE restaurant_id=?"
	args := []any{rid}
	if from != "" {
		q += " AND reservation_date>=?"
		args = append(args, from)
	}
	if to != "" {
		q += " AND reservation_date<=?"
		args = append(args, to)
	}
	q += " GROUP BY reservation_date ORDER BY reservation_date"
	rows, e := s.db.QueryContext(ctx, q, args...)
	if e != nil {
		return "", e
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var d string
		var n int
		if e = rows.Scan(&d, &n); e != nil {
			return "", e
		}
		out = append(out, map[string]any{"date": d, "count": n})
	}
	return botJSON(map[string]any{"series": out}), rows.Err()
}
