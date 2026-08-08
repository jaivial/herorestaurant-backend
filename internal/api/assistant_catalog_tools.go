package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

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
	if strings.HasSuffix(name, "_list") && name != "catalog_list" {
		return s.assistantTypedDomainList(ctx, rid, name, raw)
	}
	if name == "restaurant_settings_get" || name == "whatsapp_bot_config_get" || name == "site_published_content_get" {
		return s.assistantSafeRead(ctx, rid, name)
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
			return s.assistantRequireConfirmation(rid, name, raw)
		}
		if err := s.assistantConsumeConfirmation(in.ConfirmationToken, rid, name, raw); err != nil {
			return "", err
		}
		res, e := s.db.ExecContext(ctx, fmt.Sprintf("INSERT INTO %s (restaurant_id,%s,%s,%s) VALUES (?,?,?,?)", spec.table, spec.name, spec.desc, spec.price), rid, in.Name, in.Description, in.Price)
		if e != nil {
			return "", e
		}
		id, _ := res.LastInsertId()
		return botJSON(map[string]any{"created": true, "id": id}), nil
	case "catalog_update":
		if !in.Confirmed {
			return s.assistantRequireConfirmation(rid, name, raw)
		}
		if err := s.assistantConsumeConfirmation(in.ConfirmationToken, rid, name, raw); err != nil {
			return "", err
		}
		res, e := s.db.ExecContext(ctx, fmt.Sprintf("UPDATE %s SET %s=COALESCE(NULLIF(?,''),%s),%s=COALESCE(NULLIF(?,''),%s),%s=? WHERE restaurant_id=? AND id=?", spec.table, spec.name, spec.name, spec.desc, spec.desc, spec.price), in.Name, in.Description, in.Price, rid, in.ID)
		if e != nil {
			return "", e
		}
		n, _ := res.RowsAffected()
		return botJSON(map[string]any{"updated": n == 1}), nil
	case "catalog_delete":
		if !in.Confirmed {
			return s.assistantRequireConfirmation(rid, name, raw)
		}
		if err := s.assistantConsumeConfirmation(in.ConfirmationToken, rid, name, raw); err != nil {
			return "", err
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

// assistantTypedDomainList provides read-only, tenant-scoped domain queries.
// Tables and columns are fixed here (never supplied by the model).
func (s *Server) assistantTypedDomainList(ctx context.Context, rid int, name string, raw json.RawMessage) (string, error) {
	var in struct {
		Search, Date, Status string
		Limit                int
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", err
	}
	if in.Limit < 1 || in.Limit > 100 {
		in.Limit = 50
	}
	var q string
	args := []any{rid}
	switch name {
	case "schedules_list":
		q = `SELECT id,work_date,start_time,end_time,notes FROM member_work_schedules WHERE restaurant_id=?`
		if in.Date != "" {
			q += ` AND work_date=?`
			args = append(args, in.Date)
		}
	case "customers_list":
		q = `SELECT id, COALESCE(NULLIF(display_name, ''), email), email, phone, COALESCE(tax_id, '') FROM analytics_customers WHERE restaurant_id=?`
		if in.Search != "" {
			q += ` AND (display_name LIKE ? OR email LIKE ? OR phone LIKE ?)`
			v := "%" + in.Search + "%"
			args = append(args, v, v, v)
		}
	case "stock_items_list":
		q = `SELECT id,name,base_unit,kind,is_active FROM stock_items WHERE restaurant_id=?`
		if in.Search != "" {
			q += ` AND name LIKE ?`
			args = append(args, "%"+in.Search+"%")
		}
	case "pos_visits_list":
		q = `SELECT id,channel,service_date,status,covers FROM pos_visits WHERE restaurant_id=?`
		if in.Date != "" {
			q += ` AND service_date=?`
			args = append(args, in.Date)
		}
		if in.Status != "" {
			q += ` AND status=?`
			args = append(args, in.Status)
		}
	case "invoices_list":
		q = `SELECT id, COALESCE(concept, ''), COALESCE(amount, 0), is_active, created_at FROM recurring_invoices WHERE restaurant_id=?`
	case "recipes_list":
		q = `SELECT id,name,portions,prep_time_min,waste_pct FROM stock_recipes WHERE restaurant_id=?`
	case "production_list":
		q = `SELECT id, recipe_id, qty_produced_base, status, produced_at FROM stock_production_orders WHERE restaurant_id=?`
	case "waste_costs_list":
		q = `SELECT id, stock_item_id, entered_qty, unit_cost, occurred_at FROM stock_movements WHERE restaurant_id=? AND type='WASTE'`
	default:
		return "", fmt.Errorf("herramienta desconocida: %s", name)
	}
	q += fmt.Sprintf(" ORDER BY id DESC LIMIT %d", in.Limit)
	rows, e := s.db.QueryContext(ctx, q, args...)
	if e != nil {
		return "", e
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int
		var vals [4]any
		if e = rows.Scan(&id, &vals[0], &vals[1], &vals[2], &vals[3]); e != nil {
			return "", e
		}
		item := map[string]any{"id": id}
		for i, v := range vals {
			// Driver returns []byte for binary/binary-collation columns; keep
			// them as readable strings so the model never sees base64.
			if b, ok := v.([]byte); ok {
				v = string(b)
			}
			item[fmt.Sprintf("value_%d", i+1)] = v
		}
		out = append(out, item)
	}
	return botJSON(map[string]any{"items": out, "tool": name}), rows.Err()
}

func (s *Server) assistantSafeRead(ctx context.Context, rid int, name string) (string, error) {
	var q string
	switch name {
	case "restaurant_settings_get":
		cols := []string{"id", "name"}
		for _, c := range []string{"slug", "contact_phone", "contact_email", "location", "website_url", "avatar", "cif", "menu_url"} {
			if s.assistantColumnExists(ctx, "restaurants", c) {
				cols = append(cols, c)
			}
		}
		q = `SELECT ` + strings.Join(cols, ",") + ` FROM restaurants WHERE id=? LIMIT 1`
	case "whatsapp_bot_config_get":
		q = `SELECT restaurant_id, config_json FROM whatsapp_bot_config WHERE restaurant_id=? LIMIT 1`
	case "site_published_content_get":
		q = `SELECT id, name, published_version_id, status FROM site_builder_sites WHERE restaurant_id=? AND status='published' ORDER BY id DESC LIMIT 1`
	default:
		return "", fmt.Errorf("herramienta desconocida: %s", name)
	}
	rows, e := s.db.QueryContext(ctx, q, rid)
	if e != nil {
		return "", e
	}
	defer rows.Close()
	cols, e := rows.Columns()
	if e != nil {
		return "", e
	}
	if !rows.Next() {
		return botJSON(map[string]any{"found": false, "tool": name}), rows.Err()
	}
	vals := make([]any, len(cols))
	ptr := make([]any, len(cols))
	for i := range vals {
		ptr[i] = &vals[i]
	}
	if e = rows.Scan(ptr...); e != nil {
		return "", e
	}
	out := map[string]any{"tool": name}
	for i, c := range cols {
		if b, ok := vals[i].([]byte); ok {
			out[c] = string(b)
		} else {
			out[c] = vals[i]
		}
	}
	return botJSON(out), rows.Err()
}
