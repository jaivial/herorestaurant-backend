package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

func assistantCatalogResources() map[string]struct{ table, idCol, name, desc, price, softDelete, foto string } {
	type spec struct{ table, idCol, name, desc, price, softDelete, foto string }
	m := map[string]spec{
		// Real schema (dev MySQL): BEBIDAS/CAFES/VINOS use num as PK, comida
		// lives in comida_items, menus has menu_title, POS prices are cents.
		"comida":       {"comida_items", "id", "nombre", "descripcion", "precio", "active", "foto_url"},
		"bebidas":      {"BEBIDAS", "num", "nombre", "descripcion", "precio", "active", "foto_path"},
		"cafes":        {"CAFES", "num", "nombre", "descripcion", "precio", "active", "foto_path"},
		"menus":        {"menus", "id", "menu_title", "menu_subtitle", "price", "active", "menu_preview_image_path"},
		"pos_products": {"pos_products", "id", "name", "description", "price_gross_cents", "is_active", ""},
		"stock_items":  {"stock_items", "id", "name", "description", "", "is_active", "image_url"},
		"members":      {"restaurant_members", "id", "first_name", "email", "", "is_active", "photo_url"},
		// POSTRES has no description/price columns of its own: DESCRIPCION is
		// the name and doubles as the description slot.
		"postres": {"POSTRES", "NUM", "DESCRIPCION", "DESCRIPCION", "", "active", "foto_url"},
	}
	out := map[string]struct{ table, idCol, name, desc, price, softDelete, foto string }{}
	for k, v := range m {
		out[k] = struct{ table, idCol, name, desc, price, softDelete, foto string }{v.table, v.idCol, v.name, v.desc, v.price, v.softDelete, v.foto}
	}
	return out
}

// catalogPriceExpr returns the price column or NULL when the resource has none.
func catalogPriceExpr(sp struct{ table, idCol, name, desc, price, softDelete, foto string }) string {
	if sp.price == "" {
		return "NULL"
	}
	return sp.price
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
	// Wine is the one catalogue that is no longer a plain table with a plain
	// numeric id: its rows live in group_menu_wines_v2 and are still addressed
	// by the legacy num, so it cannot go through the generic statements below.
	if strings.ToLower(in.Resource) == "vinos" {
		return s.assistantVinoCatalogTool(ctx, rid, name, raw)
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
		fotoExpr := "NULL"
		if spec.foto != "" {
			fotoExpr = spec.foto
		}
		q := fmt.Sprintf("SELECT %s,%s,%s,%s,%s FROM %s WHERE restaurant_id=?", spec.idCol, spec.name, spec.desc, catalogPriceExpr(spec), fotoExpr, spec.table)
		args := []any{rid}
		if in.Search != "" {
			q += fmt.Sprintf(" AND %s LIKE ?", spec.name)
			args = append(args, "%"+in.Search+"%")
		}
		q += fmt.Sprintf(" ORDER BY %s DESC LIMIT %d", spec.idCol, limit)
		rows, e := s.db.QueryContext(ctx, q, args...)
		if e != nil {
			return "", e
		}
		defer rows.Close()
		out := []map[string]any{}
		for rows.Next() {
			var id int
			var n, d string
			// TEXT/DECIMAL scanned into any arrive as []byte and JSON-marshal as
			// base64; scan into NullString so the model sees real values.
			var p, f sql.NullString
			if e = rows.Scan(&id, &n, &d, &p, &f); e != nil {
				return "", e
			}
			item := map[string]any{"id": id, "name": n, "description": d, "price": nil}
			if p.Valid {
				item["price"] = p.String
			}
			if spec.foto != "" {
				var fv any
				if f.Valid {
					fv = f.String
				}
				item["foto"] = fv
			}
			out = append(out, item)
		}
		return botJSON(map[string]any{"resource": in.Resource, "items": out}), rows.Err()
	case "catalog_get":
		var id int
		var n, d string
		var p sql.NullString
		e := s.db.QueryRowContext(ctx, fmt.Sprintf("SELECT %s,%s,%s,%s FROM %s WHERE restaurant_id=? AND %s=?", spec.idCol, spec.name, spec.desc, catalogPriceExpr(spec), spec.table, spec.idCol), rid, in.ID).Scan(&id, &n, &d, &p)
		if e != nil {
			return "", e
		}
		var pv any
		if p.Valid {
			pv = p.String
		}
		return botJSON(map[string]any{"id": id, "name": n, "description": d, "price": pv}), nil
	case "catalog_create":
		if !in.Confirmed {
			return s.assistantRequireConfirmation(rid, name, raw)
		}
		if err := s.assistantConsumeConfirmation(in.ConfirmationToken, rid, name, raw); err != nil {
			return "", err
		}
		res, e := func() (sql.Result, error) {
			if spec.price == "" {
				return s.db.ExecContext(ctx, fmt.Sprintf("INSERT INTO %s (restaurant_id,%s,%s) VALUES (?,?,?)", spec.table, spec.name, spec.desc), rid, in.Name, in.Description)
			}
			return s.db.ExecContext(ctx, fmt.Sprintf("INSERT INTO %s (restaurant_id,%s,%s,%s) VALUES (?,?,?,?)", spec.table, spec.name, spec.desc, spec.price), rid, in.Name, in.Description, in.Price)
		}()
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
		res, e := func() (sql.Result, error) {
			if spec.price == "" {
				return s.db.ExecContext(ctx, fmt.Sprintf("UPDATE %s SET %s=COALESCE(NULLIF(?,''),%s),%s=COALESCE(NULLIF(?,''),%s) WHERE restaurant_id=? AND %s=?", spec.table, spec.name, spec.name, spec.desc, spec.desc, spec.idCol), in.Name, in.Description, rid, in.ID)
			}
			return s.db.ExecContext(ctx, fmt.Sprintf("UPDATE %s SET %s=COALESCE(NULLIF(?,''),%s),%s=COALESCE(NULLIF(?,''),%s),%s=? WHERE restaurant_id=? AND %s=?", spec.table, spec.name, spec.name, spec.desc, spec.desc, spec.price, spec.idCol), in.Name, in.Description, in.Price, rid, in.ID)
		}()
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
		res, e := s.db.ExecContext(ctx, fmt.Sprintf("UPDATE %s SET %s=? WHERE restaurant_id=? AND %s=?", spec.table, spec.softDelete, spec.idCol), 0, rid, in.ID)
		if e != nil {
			return "", e
		}
		n, _ := res.RowsAffected()
		return botJSON(map[string]any{"deleted": n == 1}), nil
	}
	return "", fmt.Errorf("tool no soportada")
}

// assistantVinoCatalogTool serves the "vinos" resource of the catalog tools.
//
// The generic machinery above builds its statements from a table name and a
// plain numeric id column. Wine no longer has either: its rows live in
// group_menu_wines_v2 and are still addressed by the legacy num the assistant
// was handed, so the id is resolved through legacy_wine_map here instead of
// being contorted into the generic templates.
func (s *Server) assistantVinoCatalogTool(ctx context.Context, rid int, name string, raw json.RawMessage) (string, error) {
	if rid <= 0 || s.db == nil {
		return "", fmt.Errorf("restaurante activo no disponible")
	}
	var in struct {
		Search, Name, Description string
		ID, Limit                 int
		Price                     float64
		Confirmed                 bool
		ConfirmationToken         string `json:"confirmation_token"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", err
	}

	const numExpr = vinoV2NumExpr
	switch name {
	case "catalog_list":
		limit := in.Limit
		if limit < 1 || limit > 100 {
			limit = 50
		}
		q := "SELECT " + numExpr + ",v.nombre,v.descripcion,v.precio,v.foto_path " + vinoV2From + " WHERE v.restaurant_id=?"
		args := []any{rid}
		if in.Search != "" {
			q += " AND v.nombre LIKE ?"
			args = append(args, "%"+in.Search+"%")
		}
		q += " ORDER BY " + numExpr + " DESC LIMIT " + strconv.Itoa(limit)
		rows, e := s.db.QueryContext(ctx, q, args...)
		if e != nil {
			return "", e
		}
		defer rows.Close()
		out := []map[string]any{}
		for rows.Next() {
			var id int
			var n, d string
			// DECIMAL/varchar scan into any arrive as []byte and JSON-marshal
			// as base64; scan into NullString so the model sees real values.
			var price, foto sql.NullString
			if e = rows.Scan(&id, &n, &d, &price, &foto); e != nil {
				return "", e
			}
			item := map[string]any{"id": id, "name": n, "description": d, "price": nil, "foto": nil}
			if price.Valid {
				item["price"] = price.String
			}
			if foto.Valid {
				item["foto"] = foto.String
			}
			out = append(out, item)
		}
		return botJSON(map[string]any{"resource": "vinos", "items": out}), rows.Err()
	case "catalog_get":
		var id int
		var n, d string
		var p sql.NullString
		e := s.db.QueryRowContext(ctx, "SELECT "+numExpr+",v.nombre,v.descripcion,v.precio "+vinoV2From+" WHERE "+vinoV2WhereLegacyNumJoined, vinoV2LegacyNumArgs(rid, in.ID)...).Scan(&id, &n, &d, &p)
		if e != nil {
			return "", e
		}
		var pv any
		if p.Valid {
			pv = p.String
		}
		return botJSON(map[string]any{"id": id, "name": n, "description": d, "price": pv}), nil
	case "catalog_create":
		if !in.Confirmed {
			return s.assistantRequireConfirmation(rid, name, raw)
		}
		if err := s.assistantConsumeConfirmation(in.ConfirmationToken, rid, name, raw); err != nil {
			return "", err
		}
		// tipo is NOT NULL in the catalogue while the tool has no notion of wine
		// type, so an empty string is stored rather than inventing a category.
		_, legacyNum, e := s.insertVinoV2(ctx, rid, vinoV2Insert{
			Tipo:        "",
			Nombre:      strings.TrimSpace(in.Name),
			Descripcion: strings.TrimSpace(in.Description),
			Precio:      in.Price,
			Active:      1,
		})
		if e != nil {
			return "", e
		}
		return botJSON(map[string]any{"created": true, "id": legacyNum}), nil
	case "catalog_update":
		if !in.Confirmed {
			return s.assistantRequireConfirmation(rid, name, raw)
		}
		if err := s.assistantConsumeConfirmation(in.ConfirmationToken, rid, name, raw); err != nil {
			return "", err
		}
		res, e := s.db.ExecContext(ctx, "UPDATE group_menu_wines_v2 SET nombre=COALESCE(NULLIF(?,''),nombre),descripcion=COALESCE(NULLIF(?,''),descripcion),precio=? WHERE "+vinoV2WhereLegacyNum,
			append([]any{in.Name, in.Description, in.Price}, vinoV2LegacyNumArgs(rid, in.ID)...)...)
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
		res, e := s.db.ExecContext(ctx, "UPDATE group_menu_wines_v2 SET active=0 WHERE "+vinoV2WhereLegacyNum, vinoV2LegacyNumArgs(rid, in.ID)...)
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
