package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"preactvillacarmen/internal/sqldb"
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
	// Postres cannot use the generic statements either: it has no price
	// column and its name doubles as its description, and the shared
	// resource map is also the dispatch for catalog_create/update/delete, so
	// registering it there would enable writes with no granted scope. It
	// goes through a read-only typed path instead.
	if strings.ToLower(in.Resource) == "postres" {
		return s.assistantPostresCatalogTool(ctx, rid, name, raw)
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
			fotoExpr = assistantCatIdent(spec.foto)
		}
		q := fmt.Sprintf("SELECT %s,%s,%s,%s,%s FROM %s WHERE restaurant_id=?", assistantCatIdent(spec.idCol), assistantCatIdent(spec.name), assistantCatIdent(spec.desc), assistantCatalogPriceSelect(spec), fotoExpr, assistantCatIdent(spec.table))
		args := []any{rid}
		if in.Search != "" {
			q += " AND " + assistantLikeClause(assistantCatIdent(spec.name))
			args = append(args, assistantLikeArg(in.Search))
		}
		q += fmt.Sprintf(" ORDER BY %s DESC LIMIT %d", assistantCatIdent(spec.idCol), limit)
		rows, e := s.db.QueryContext(ctx, portableSQL(q), args...)
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
		e := s.db.QueryRowContext(ctx, portableSQL(fmt.Sprintf("SELECT %s,%s,%s,%s FROM %s WHERE restaurant_id=? AND %s=?", assistantCatIdent(spec.idCol), assistantCatIdent(spec.name), assistantCatIdent(spec.desc), assistantCatalogPriceSelect(spec), assistantCatIdent(spec.table), assistantCatIdent(spec.idCol))), rid, in.ID).Scan(&id, &n, &d, &p)
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
		q := fmt.Sprintf("INSERT INTO %s (restaurant_id,%s,%s) VALUES (?,?,?)", assistantCatIdent(spec.table), assistantCatIdent(spec.name), assistantCatIdent(spec.desc))
		args := []any{rid, in.Name, in.Description}
		if spec.price != "" {
			q = fmt.Sprintf("INSERT INTO %s (restaurant_id,%s,%s,%s) VALUES (?,?,?,%s)", assistantCatIdent(spec.table), assistantCatIdent(spec.name), assistantCatIdent(spec.desc), assistantCatIdent(spec.price), assistantCatalogPriceArg(spec.table))
			args = append(args, in.Price)
		}
		var id int64
		if sqlDialect == sqldb.Postgres {
			// RETURNING id: an execution error is returned exactly like the
			// source ExecContext error (pgx has no LastInsertId).
			var e error
			if id, e = sqldb.InsertID(ctx, sqlDialect, s.db, portableSQL(q), assistantCatIdent(spec.idCol), args...); e != nil {
				return "", e
			}
		} else {
			res, e := s.db.ExecContext(ctx, portableSQL(q), args...)
			if e != nil {
				return "", e
			}
			id, _ = res.LastInsertId() // source ignores the id retrieval error
		}
		return botJSON(map[string]any{"created": true, "id": id}), nil
	case "catalog_update":
		if !in.Confirmed {
			return s.assistantRequireConfirmation(rid, name, raw)
		}
		if err := s.assistantConsumeConfirmation(in.ConfirmationToken, rid, name, raw); err != nil {
			return "", err
		}
		res, e := func() (sql.Result, error) {
			if sqlDialect == sqldb.Postgres {
				// MySQL RowsAffected counts CHANGED rows ("updated": n == 1):
				// the PG twin only touches rows whose stored tuple differs.
				nm, ds := assistantCatIdent(spec.name), assistantCatIdent(spec.desc)
				nameV := fmt.Sprintf("COALESCE(NULLIF(CAST(? AS text),''),%s)", nm)
				descV := fmt.Sprintf("COALESCE(NULLIF(CAST(? AS text),''),%s)", ds)
				sets := fmt.Sprintf("%s=%s,%s=%s", nm, nameV, ds, descV)
				cols := fmt.Sprintf(`%s COLLATE "C",%s COLLATE "C"`, nm, ds)
				vals := fmt.Sprintf(`%s COLLATE "C",%s COLLATE "C"`, nameV, descV)
				setArgs := []any{padSpaceNullIfArg(in.Name), padSpaceNullIfArg(in.Description)}
				if spec.price != "" {
					pc, pv := assistantCatIdent(spec.price), assistantCatalogPriceArg(spec.table)
					sets += fmt.Sprintf(",%s=%s", pc, pv)
					cols += "," + pc
					vals += "," + pv
					setArgs = append(setArgs, in.Price)
				}
				q := fmt.Sprintf("UPDATE %s SET %s WHERE restaurant_id=? AND %s=? AND ROW(%s) IS DISTINCT FROM ROW(%s)", assistantCatIdent(spec.table), sets, assistantCatIdent(spec.idCol), cols, vals)
				args := append(append(append([]any{}, setArgs...), rid, in.ID), setArgs...)
				return s.db.ExecContext(ctx, pgOnlySQL(q), args...)
			}
			if spec.price == "" {
				return s.db.ExecContext(ctx, mysqlOnlySQL(fmt.Sprintf("UPDATE %s SET %s=COALESCE(NULLIF(?,''),%s),%s=COALESCE(NULLIF(?,''),%s) WHERE restaurant_id=? AND %s=?", spec.table, spec.name, spec.name, spec.desc, spec.desc, spec.idCol)), in.Name, in.Description, rid, in.ID)
			}
			return s.db.ExecContext(ctx, mysqlOnlySQL(fmt.Sprintf("UPDATE %s SET %s=COALESCE(NULLIF(?,''),%s),%s=COALESCE(NULLIF(?,''),%s),%s=? WHERE restaurant_id=? AND %s=?", spec.table, spec.name, spec.name, spec.desc, spec.desc, spec.price, spec.idCol)), in.Name, in.Description, in.Price, rid, in.ID)
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
		// PG: changed-rows predicate keeps "deleted": n == 1 false for an
		// already-inactive row, as MySQL's changed-rows count does.
		res, e := s.db.ExecContext(ctx, portableSQL(fmt.Sprintf("UPDATE %s SET %s=? WHERE restaurant_id=? AND %s=?", assistantCatIdent(spec.table), assistantCatIdent(spec.softDelete), assistantCatIdent(spec.idCol))+sqlFragment("", " AND "+assistantCatIdent(spec.softDelete)+" IS DISTINCT FROM CAST(? AS smallint)")), assistantSoftDeleteArgs(rid, in.ID)...)
		if e != nil {
			return "", e
		}
		n, _ := res.RowsAffected()
		return botJSON(map[string]any{"deleted": n == 1}), nil
	}
	return "", fmt.Errorf("tool no soportada")
}

// assistantPostresCatalogTool serves the "postres" resource of the catalog
// tools as READ-ONLY (list/get only). POSTRES is a plain table with a numeric
// id, but it has no price column and DESCRIPCION doubles as name and
// description, so the generic CRUD statements do not fit it (the INSERT would
// name the same column twice). More importantly, the generic resource map is
// shared by catalog_create/update/delete: registering postres there would
// have enabled writes with no granted write scope. Unknown tool names fail
// closed.
func (s *Server) assistantPostresCatalogTool(ctx context.Context, rid int, name string, raw json.RawMessage) (string, error) {
	if rid <= 0 || s.db == nil {
		return "", fmt.Errorf("restaurante activo no disponible")
	}
	var in struct {
		Search string
		ID     int
		Limit  int
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", err
	}
	switch name {
	case "catalog_list":
		limit := in.Limit
		if limit < 1 || limit > 100 {
			limit = 50
		}
		q := sqlFragment("SELECT NUM,DESCRIPCION,foto_url FROM POSTRES WHERE restaurant_id=?", `SELECT "NUM","DESCRIPCION",foto_url FROM "POSTRES" WHERE restaurant_id=?`)
		args := []any{rid}
		if in.Search != "" {
			q += " AND " + assistantLikeClause(sqlFragment("DESCRIPCION", `"DESCRIPCION"`))
			args = append(args, assistantLikeArg(in.Search))
		}
		q += sqlFragment(" ORDER BY NUM DESC LIMIT ", ` ORDER BY "NUM" DESC LIMIT `) + strconv.Itoa(limit)
		rows, e := s.db.QueryContext(ctx, portableSQL(q), args...)
		if e != nil {
			return "", e
		}
		defer rows.Close()
		out := []map[string]any{}
		for rows.Next() {
			var id int
			var n string // DESCRIPCION is NOT NULL in the POSTRES schema
			var f sql.NullString
			if e = rows.Scan(&id, &n, &f); e != nil {
				return "", e
			}
			var fv any
			if f.Valid {
				fv = f.String
			}
			out = append(out, map[string]any{"id": id, "name": n, "description": n, "price": nil, "foto": fv})
		}
		return botJSON(map[string]any{"resource": "postres", "items": out}), rows.Err()
	case "catalog_get":
		var id int
		var n string
		e := s.db.QueryRowContext(ctx, nativeSQL("SELECT NUM,DESCRIPCION FROM POSTRES WHERE restaurant_id=? AND NUM=?", `SELECT "NUM","DESCRIPCION" FROM "POSTRES" WHERE restaurant_id=? AND "NUM"=?`), rid, in.ID).Scan(&id, &n)
		if e != nil {
			return "", e
		}
		return botJSON(map[string]any{"id": id, "name": n, "description": n, "price": nil}), nil
	case "catalog_create", "catalog_update", "catalog_delete":
		// Explicit read-only enforcement: postres has no granted write scope.
		return "", fmt.Errorf("postres es un recurso de solo lectura")
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

	numExpr := vinoV2NumSQL()
	// precio is FLOAT(10,2) (PG real): scanned as a value and rendered by
	// float32SQLText exactly as database/sql renders the MySQL driver's float32.
	precioSel := "v.precio"
	switch name {
	case "catalog_list":
		limit := in.Limit
		if limit < 1 || limit > 100 {
			limit = 50
		}
		q := "SELECT " + numExpr + ",v.nombre,v.descripcion," + precioSel + ",v.foto_path " + vinoV2From + " WHERE v.restaurant_id=?"
		args := []any{rid}
		if in.Search != "" {
			q += " AND " + assistantLikeClause("v.nombre")
			args = append(args, assistantLikeArg(in.Search))
		}
		q += " ORDER BY " + numExpr + " DESC LIMIT " + strconv.Itoa(limit)
		rows, e := s.db.QueryContext(ctx, portableSQL(q), args...)
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
			var price any
			var foto sql.NullString
			if e = rows.Scan(&id, &n, &d, &price, &foto); e != nil {
				return "", e
			}
			item := map[string]any{"id": id, "name": n, "description": d, "price": nil, "foto": nil}
			if ps, ok := float32SQLText(price); ok {
				item["price"] = ps
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
		var p any
		e := s.db.QueryRowContext(ctx, portableSQL("SELECT "+numExpr+",v.nombre,v.descripcion,"+precioSel+" "+vinoV2From+" WHERE "+vinoV2WhereLegacyNumJoined), vinoV2LegacyNumArgs(rid, in.ID)...).Scan(&id, &n, &d, &p)
		if e != nil {
			return "", e
		}
		var pv any
		if ps, ok := float32SQLText(p); ok {
			pv = ps
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
		setArgs := []any{padSpaceNullIfArg(in.Name), padSpaceNullIfArg(in.Description), in.Price}
		vinoArgs := append(append([]any{}, setArgs...), vinoV2LegacyNumArgs(rid, in.ID)...)
		if sqlDialect == sqldb.Postgres {
			vinoArgs = append(vinoArgs, setArgs...) // changed-rows predicate operands
		}
		res, e := s.db.ExecContext(ctx, portableSQL(sqlFragment("UPDATE group_menu_wines_v2 SET nombre=COALESCE(NULLIF(?,''),nombre),descripcion=COALESCE(NULLIF(?,''),descripcion),precio=? WHERE ", "UPDATE group_menu_wines_v2 SET nombre=COALESCE(NULLIF(CAST(? AS text),''),nombre),descripcion=COALESCE(NULLIF(CAST(? AS text),''),descripcion),precio=CAST(CAST(? AS double precision) AS real) WHERE ")+vinoV2WhereLegacyNum+sqlFragment("", ` AND ROW(nombre COLLATE "C",descripcion COLLATE "C",precio) IS DISTINCT FROM ROW(COALESCE(NULLIF(CAST(? AS text),''),nombre) COLLATE "C",COALESCE(NULLIF(CAST(? AS text),''),descripcion) COLLATE "C",CAST(CAST(? AS double precision) AS real))`)),
			vinoArgs...)
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
		res, e := s.db.ExecContext(ctx, portableSQL("UPDATE group_menu_wines_v2 SET active=0 WHERE "+vinoV2WhereLegacyNum+sqlFragment("", " AND active IS DISTINCT FROM 0")), vinoV2LegacyNumArgs(rid, in.ID)...)
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
		q = sqlFragment(`SELECT id,work_date,start_time,end_time,notes FROM member_work_schedules WHERE restaurant_id=?`, `SELECT id,work_date,CAST(start_time AS text),CAST(end_time AS text),notes FROM member_work_schedules WHERE restaurant_id=?`)
		if in.Date != "" {
			q += ` AND work_date=?`
			args = append(args, in.Date)
		}
	case "customers_list":
		// display_name is utf8mb4_unicode_ci (PAD SPACE): NULLIF(x,'') is NULL
		// for space-only values too; the PG twin rtrims spaces explicitly
		// (UCA zero-weight characters remain an open PAD/CI gap).
		q = sqlFragment(`SELECT id, COALESCE(NULLIF(display_name, ''), email), email, phone, COALESCE(tax_id, '') FROM analytics_customers WHERE restaurant_id=?`, `SELECT id, COALESCE(CASE WHEN rtrim(display_name, ' ') COLLATE "C" = '' THEN NULL ELSE display_name END, email), email, phone, COALESCE(tax_id, '') FROM analytics_customers WHERE restaurant_id=?`)
		if in.Search != "" {
			q += " AND (" + assistantLikeClause("display_name") + " OR " + assistantLikeClause("email") + " OR " + assistantLikeClause("phone") + ")"
			v := assistantLikeArg(in.Search)
			args = append(args, v, v, v)
		}
	case "stock_items_list":
		q = sqlFragment(`SELECT id,name,base_unit,kind,is_active FROM stock_items WHERE restaurant_id=?`, `SELECT id,name,CAST(base_unit AS text),CAST(kind AS text),is_active FROM stock_items WHERE restaurant_id=?`)
		if in.Search != "" {
			q += " AND " + assistantLikeClause("name")
			args = append(args, assistantLikeArg(in.Search))
		}
	case "pos_visits_list":
		q = sqlFragment(`SELECT id,channel,service_date,status,covers FROM pos_visits WHERE restaurant_id=?`, `SELECT id,CAST(channel AS text),service_date,CAST(status AS text),covers FROM pos_visits WHERE restaurant_id=?`)
		if in.Date != "" {
			q += ` AND service_date=?`
			args = append(args, in.Date)
		}
		if in.Status != "" {
			// MySQL: unknown enum text simply matches nothing; PG compares text
			// (CI via old-nondeterministic-collation; accent/PAD SPACE delta stays open).
			q += sqlFragment(` AND status=?`, ` AND app.norm(CAST(status AS text)) = app.norm(?)`)
			args = append(args, in.Status)
		}
	case "invoices_list":
		q = `SELECT id, COALESCE(concept, ''), COALESCE(amount, 0), is_active, created_at FROM recurring_invoices WHERE restaurant_id=?`
	case "recipes_list":
		q = `SELECT id,name,portions,prep_time_min,waste_pct FROM stock_recipes WHERE restaurant_id=?`
	case "production_list":
		q = sqlFragment(`SELECT id, recipe_id, qty_produced_base, status, produced_at FROM stock_production_orders WHERE restaurant_id=?`, `SELECT id, recipe_id, qty_produced_base, CAST(status AS text), produced_at FROM stock_production_orders WHERE restaurant_id=?`)
	case "waste_costs_list":
		q = `SELECT id, stock_item_id, entered_qty, unit_cost, occurred_at FROM stock_movements WHERE restaurant_id=? AND type='WASTE'`
	default:
		return "", fmt.Errorf("herramienta desconocida: %s", name)
	}
	q += fmt.Sprintf(" ORDER BY id DESC LIMIT %d", in.Limit)
	rows, e := s.db.QueryContext(ctx, portableSQL(q), args...)
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
		q = sqlFragment(`SELECT restaurant_id, config_json FROM whatsapp_bot_config WHERE restaurant_id=? LIMIT 1`, `SELECT restaurant_id, CAST(config_json AS text) AS config_json FROM whatsapp_bot_config WHERE restaurant_id=? LIMIT 1`)
	case "site_published_content_get":
		q = sqlFragment(`SELECT id, name, published_version_id, status FROM site_builder_sites WHERE restaurant_id=? AND status='published' ORDER BY id DESC LIMIT 1`, `SELECT id, name, published_version_id, CAST(status AS text) AS status FROM site_builder_sites WHERE restaurant_id=? AND status='published' ORDER BY id DESC LIMIT 1`)
	default:
		return "", fmt.Errorf("herramienta desconocida: %s", name)
	}
	rows, e := s.db.QueryContext(ctx, portableSQL(q), rid)
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

// assistantCatIdent quotes a fixed (never model-supplied) identifier on PG:
// BEBIDAS/CAFES/POSTRES keep their MySQL upper case in the imported schema.
func assistantCatIdent(c string) string { return sqlFragment(c, `"`+c+`"`) }

// assistantLikeClause / assistantLikeArg: PG17 cannot LIKE on the
// nondeterministic old-nondeterministic-collation collation, so PG matches LOWER(col) COLLATE "C"
// against a lowered pattern. Case-insensitive only: the MySQL accent folding
// and PAD SPACE remain an open search gap (no approximation accepted).
func assistantLikeClause(col string) string {
	return sqlFragment(col+" LIKE ?", "LOWER("+col+`) COLLATE "C" LIKE ?`)
}

func assistantLikeArg(search string) string {
	v := "%" + search + "%"
	if sqlDialect == sqldb.Postgres {
		v = strings.ToLower(v)
	}
	return v
}

// assistantCatalogPriceSelect renders the price as text on PG (numeric /
// bigint text equals the MySQL DECIMAL/BIGINT driver bytes).
func assistantCatalogPriceSelect(sp struct{ table, idCol, name, desc, price, softDelete, foto string }) string {
	if sp.price == "" {
		return catalogPriceExpr(sp)
	}
	return sqlFragment(sp.price, "CAST("+assistantCatIdent(sp.price)+" AS text)")
}

// assistantCatalogPriceArg binds the float64 tool price with the PG column
// type (DECIMAL(10,2) -> numeric(10,2); pos_products cents BIGINT -> bigint).
// Half-way rounding of float->decimal/bigint is an open QA gap.
func assistantCatalogPriceArg(table string) string {
	if table == "pos_products" {
		return sqlFragment("?", "CAST(CAST(? AS double precision) AS bigint)")
	}
	return sqlFragment("?", "CAST(CAST(? AS double precision) AS numeric(10,2))")
}

// assistantSoftDeleteArgs: MySQL "SET flag=0 ... id=?"; PG appends the
// changed-rows operand for "flag IS DISTINCT FROM 0".
func assistantSoftDeleteArgs(rid, id int) []any {
	if sqlDialect == sqldb.Postgres {
		return []any{0, rid, id, 0}
	}
	return []any{0, rid, id}
}

// float32SQLText renders a FLOAT column value as the source did when it was
// scanned into sql.NullString: go-sql-driver v1.9.3 yields float32 on both
// protocols and database/sql formats it with FormatFloat('g', -1, 32). pgx
// yields float64(float32) for real, narrowed back here (exact). NULL -> false.
func float32SQLText(v any) (string, bool) {
	switch t := v.(type) {
	case nil:
		return "", false
	case float32:
		return strconv.FormatFloat(float64(t), 'g', -1, 32), true
	case float64:
		return strconv.FormatFloat(float64(float32(t)), 'g', -1, 32), true
	case []byte:
		return string(t), true
	case string:
		return t, true
	}
	return fmt.Sprint(v), true
}

// padSpaceNullIfArg: the MySQL DSN (internal/db/mysql.go) sets the
// connection collation utf8mb4_unicode_ci (PAD SPACE), so NULLIF(?, ”) is
// NULL for a space-only parameter. PG compares text exactly, so the PG
// branch binds ” for space-only input. UCA zero-weight characters remain
// an open residual gap. MySQL path: unchanged value.
func padSpaceNullIfArg(v string) string {
	if sqlDialect == sqldb.Postgres && strings.TrimRight(v, " ") == "" {
		return ""
	}
	return v
}

// assistantCatalogNameCollation is the source collation of each catalog
// resource's name column (full-export/schema.json; all utf8mb4_unicode_ci PAD
// SPACE per the local collation metadata). A table missing here keeps the
// pre-flag old-nondeterministic-collation LIKE path (repair-v17).
var assistantCatalogNameCollation = map[string]string{
	"comida_items":       "utf8mb4_unicode_ci",
	"BEBIDAS":            "utf8mb4_unicode_ci",
	"CAFES":              "utf8mb4_unicode_ci",
	"menus":              "utf8mb4_unicode_ci",
	"pos_products":       "utf8mb4_unicode_ci",
	"stock_items":        "utf8mb4_unicode_ci",
	"restaurant_members": "utf8mb4_unicode_ci",
}
