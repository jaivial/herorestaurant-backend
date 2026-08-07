package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// assistantCatalogToolDefs exposes the safe, common CRUD surface used by
// Forky. All names resolve through allowlisted resources; no SQL is accepted.
func assistantCatalogToolDefs() []assistantToolDef {
	return []assistantToolDef{
		{Name: "catalog_list", Description: "Lista recursos del restaurante activo: comida, bebidas, cafés, vinos, menús, productos POS, stock o miembros.", InputSchema: json.RawMessage(`{"type":"object","properties":{"resource":{"type":"string","enum":["comida","bebidas","cafes","vinos","menus","pos_products","stock_items","members"]},"search":{"type":"string"},"limit":{"type":"integer"}},"required":["resource"]}`)},
		{Name: "catalog_get", Description: "Obtiene recurso por ID dentro del restaurante activo.", InputSchema: json.RawMessage(`{"type":"object","properties":{"resource":{"type":"string"},"id":{"type":"integer"}},"required":["resource","id"]}`)},
		{Name: "catalog_create", Description: "Crea recurso del restaurante activo. Requiere confirmed=true.", InputSchema: json.RawMessage(`{"type":"object","properties":{"resource":{"type":"string"},"name":{"type":"string"},"description":{"type":"string"},"price":{"type":"number"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["resource","name","confirmed"]}`)},
		{Name: "catalog_update", Description: "Actualiza recurso del restaurante activo. Requiere confirmed=true.", InputSchema: json.RawMessage(`{"type":"object","properties":{"resource":{"type":"string"},"id":{"type":"integer"},"name":{"type":"string"},"description":{"type":"string"},"price":{"type":"number"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["resource","id","confirmed"]}`)},
		{Name: "catalog_delete", Description: "Elimina/desactiva recurso del restaurante activo. Requiere confirmed=true.", InputSchema: json.RawMessage(`{"type":"object","properties":{"resource":{"type":"string"},"id":{"type":"integer"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["resource","id","confirmed"]}`)},
		{Name: "analytics_report", Description: "Devuelve métricas y series del restaurante activo para gráficos.", InputSchema: json.RawMessage(`{"type":"object","properties":{"metric":{"type":"string","enum":["bookings","revenue","products","stock"]},"date_from":{"type":"string"},"date_to":{"type":"string"}},"required":["metric"]}`)},
	}
}

func assistantCatalogResources() map[string]struct{ table, name, desc, price, softDelete string } {
	return map[string]struct{ table, name, desc, price, softDelete string }{
		"comida": {"comida", "nombre", "descripcion", "precio", "activo"}, "bebidas": {"bebidas", "nombre", "descripcion", "precio", "activo"}, "cafes": {"cafes", "nombre", "descripcion", "precio", "activo"}, "vinos": {"vinos", "nombre", "descripcion", "precio", "activo"}, "menus": {"group_menus", "name", "description", "price", "active"}, "pos_products": {"pos_products", "name", "description", "price_cents", "status"}, "stock_items": {"stock_items", "name", "description", "unit_cost", "status"}, "members": {"members", "name", "email", "phone", "active"},
	}
}

func (s *Server) assistantCatalogTool(ctx context.Context, rid int, name string, raw json.RawMessage) (string, error) {
	if rid <= 0 || s.db == nil {
		return "", fmt.Errorf("restaurante activo no disponible")
	}
	var in struct {
		Resource, Search, Name, Description string
		ID, Limit                           int
		Price                               float64
		Confirmed                           bool
		ConfirmationToken                   string `json:"confirmation_token"`
		Metric, DateFrom, DateTo            string
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", err
	}
	spec, ok := assistantCatalogResources()[strings.ToLower(in.Resource)]
	if !ok {
		return "", fmt.Errorf("recurso no permitido: %s", in.Resource)
	}
	switch name {
	case "catalog_list":
		limit := in.Limit
		if limit < 1 || limit > 100 {
			limit = 50
		}
		q := fmt.Sprintf("SELECT id,%s,%s,%s FROM %s WHERE restaurant_id=?", spec.name, spec.desc, spec.price, spec.table)
		args := []any{rid}
		if in.Search != "" {
			q += fmt.Sprintf(" AND %s LIKE ?", spec.name)
			args = append(args, "%"+in.Search+"%")
		}
		q += fmt.Sprintf(" ORDER BY id DESC LIMIT %d", limit)
		rows, e := s.db.QueryContext(ctx, q, args...)
		if e != nil {
			return "", e
		}
		defer rows.Close()
		out := []map[string]any{}
		for rows.Next() {
			var id int
			var n, d string
			var p any
			if e = rows.Scan(&id, &n, &d, &p); e != nil {
				return "", e
			}
			out = append(out, map[string]any{"id": id, "name": n, "description": d, "price": p})
		}
		return botJSON(map[string]any{"resource": in.Resource, "items": out}), rows.Err()
	case "catalog_get":
		var id int
		var n, d string
		var p any
		e := s.db.QueryRowContext(ctx, fmt.Sprintf("SELECT id,%s,%s,%s FROM %s WHERE restaurant_id=? AND id=?", spec.name, spec.desc, spec.price, spec.table), rid, in.ID).Scan(&id, &n, &d, &p)
		if e != nil {
			return "", e
		}
		return botJSON(map[string]any{"id": id, "name": n, "description": d, "price": p}), nil
	case "catalog_create":
		if !in.Confirmed {
			if s.confirmationStore == nil {
				return botJSON(map[string]any{"requires_confirmation": true}), nil
			}
			tok, e := s.confirmationStore.Issue("", fmt.Sprint(rid), name, "", "", 2*time.Minute)
			if e != nil {
				return "", e
			}
			return botJSON(map[string]any{"requires_confirmation": true, "confirmation_token": tok, "expires_in_seconds": 120}), nil
		}
		if s.confirmationStore != nil {
			if e := s.confirmationStore.Consume(in.ConfirmationToken, "", fmt.Sprint(rid), name, "", ""); e != nil {
				return "", e
			}
		}
		res, e := s.db.ExecContext(ctx, fmt.Sprintf("INSERT INTO %s (restaurant_id,%s,%s,%s) VALUES (?,?,?,?)", spec.table, spec.name, spec.desc, spec.price), rid, in.Name, in.Description, in.Price)
		if e != nil {
			return "", e
		}
		id, _ := res.LastInsertId()
		return botJSON(map[string]any{"created": true, "id": id}), nil
	case "catalog_update":
		if !in.Confirmed {
			if s.confirmationStore == nil {
				return botJSON(map[string]any{"requires_confirmation": true}), nil
			}
			tok, e := s.confirmationStore.Issue("", fmt.Sprint(rid), name, "", "", 2*time.Minute)
			if e != nil {
				return "", e
			}
			return botJSON(map[string]any{"requires_confirmation": true, "confirmation_token": tok, "expires_in_seconds": 120}), nil
		}
		if s.confirmationStore != nil {
			if e := s.confirmationStore.Consume(in.ConfirmationToken, "", fmt.Sprint(rid), name, "", ""); e != nil {
				return "", e
			}
		}
		res, e := s.db.ExecContext(ctx, fmt.Sprintf("UPDATE %s SET %s=COALESCE(NULLIF(?,''),%s),%s=COALESCE(NULLIF(?,''),%s),%s=? WHERE restaurant_id=? AND id=?", spec.table, spec.name, spec.name, spec.desc, spec.desc, spec.price), in.Name, in.Description, in.Price, rid, in.ID)
		if e != nil {
			return "", e
		}
		n, _ := res.RowsAffected()
		return botJSON(map[string]any{"updated": n == 1}), nil
	case "catalog_delete":
		if !in.Confirmed {
			if s.confirmationStore == nil {
				return botJSON(map[string]any{"requires_confirmation": true}), nil
			}
			tok, e := s.confirmationStore.Issue("", fmt.Sprint(rid), name, "", "", 2*time.Minute)
			if e != nil {
				return "", e
			}
			return botJSON(map[string]any{"requires_confirmation": true, "confirmation_token": tok, "expires_in_seconds": 120}), nil
		}
		if s.confirmationStore != nil {
			if e := s.confirmationStore.Consume(in.ConfirmationToken, "", fmt.Sprint(rid), name, "", ""); e != nil {
				return "", e
			}
		}
		res, e := s.db.ExecContext(ctx, fmt.Sprintf("UPDATE %s SET %s=? WHERE restaurant_id=? AND id=?", spec.table, spec.softDelete), 0, rid, in.ID)
		if e != nil {
			return "", e
		}
		n, _ := res.RowsAffected()
		return botJSON(map[string]any{"deleted": n == 1}), nil
	}
	return "", fmt.Errorf("tool no soportada")
}
