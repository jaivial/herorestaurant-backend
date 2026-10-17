package api

import (
	"context"
)

// group_menu_wines_v2 is the wine catalogue; the legacy VINOS table is left
// behind as a snapshot of the rows it held when the switch happened.
//
// Everything outside the catalogue - URL params, JSON bodies, WebSocket
// payloads and the translations table - still names a wine by its legacy
// VINOS.num, so every statement below translates that num through
// legacy_wine_map instead of leaking the v2 primary key into the API.
const (
	// vinoV2From joins the catalogue to its legacy-id map. The LEFT JOIN keeps a
	// wine listed even when its map row is missing, so a hole in the map
	// degrades the reported id rather than hiding the wine from the backoffice.
	vinoV2From = `FROM group_menu_wines_v2 v LEFT JOIN legacy_wine_map m ON m.restaurant_id = v.restaurant_id AND m.v2_wine_id = v.id`

	// vinoV2NumExpr renders the legacy num of a catalogue row for a SELECT list.
	// It only makes sense together with vinoV2From.
	//
	// v.id is BIGINT UNSIGNED while legacy_num is a signed INT; the COALESCE
	// would promote the result to unsigned, which database/sql cannot scan into
	// the plain int the handlers use. The CAST keeps the value signed so every
	// existing Scan target keeps working unchanged.
	vinoV2NumExpr = `CAST(COALESCE(m.legacy_num, v.id) AS SIGNED)`

	// vinoV2WhereLegacyNum pins one catalogue row from its legacy num. The
	// subquery only reads legacy_wine_map, which MySQL permits inside the
	// UPDATE/DELETE of another table (the restriction is on selecting from the
	// table being updated). The restaurant id appears twice - once for the row,
	// once for the map lookup - so a num can never resolve across tenants.
	//
	// Argument order is restaurantID, restaurantID, legacyNum; use
	// vinoV2LegacyNumArgs to build it.
	vinoV2WhereLegacyNum = `restaurant_id = ? AND id = (SELECT w.v2_wine_id FROM legacy_wine_map w WHERE w.restaurant_id = ? AND w.legacy_num = ?)`

	// vinoV2WhereLegacyNumJoined is the same predicate for statements that also
	// carry vinoV2From: once the map is joined into scope, restaurant_id has to
	// say which of the two tables it belongs to. Same argument order.
	vinoV2WhereLegacyNumJoined = `v.restaurant_id = ? AND v.id = (SELECT w.v2_wine_id FROM legacy_wine_map w WHERE w.restaurant_id = ? AND w.legacy_num = ?)`
)

// vinoV2LegacyNumArgs fills vinoV2WhereLegacyNum.
func vinoV2LegacyNumArgs(restaurantID int, legacyNum int) []any {
	return []any{restaurantID, restaurantID, legacyNum}
}

// vinoV2Insert carries the columns the legacy INSERT used to write. There is no
// foto: the blob column was dropped in favour of foto_path pointing at the CDN.
type vinoV2Insert struct {
	Tipo               string
	Nombre             string
	Precio             float64
	Descripcion        string
	Bodega             string
	DenominacionOrigen string
	Graduacion         float64
	Anyo               string
	Active             int
}

// insertVinoV2 writes a wine to the catalogue and allocates the legacy num the
// rest of the system still uses to address it, returning both.
//
// Both statements share one transaction: a catalogue row without a map row is
// unreachable from every API that still speaks "num", and a map row without a
// catalogue row is a dangling pointer. The legacy num is MAX(legacy_num)+1 per
// restaurant, which is always free because the map's primary key is
// (restaurant_id, legacy_num) and nums are never reused after a delete.
func (s *Server) insertVinoV2(ctx context.Context, restaurantID int, in vinoV2Insert) (int64, int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `
		INSERT INTO group_menu_wines_v2
			(restaurant_id, tipo, nombre, precio, descripcion, bodega, denominacion_origen, graduacion, anyo, active, foto_path, position)
		SELECT ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, COALESCE(MAX(position), 0) + 1
		  FROM group_menu_wines_v2 WHERE restaurant_id = ?`,
		restaurantID, in.Tipo, in.Nombre, in.Precio, in.Descripcion, in.Bodega,
		in.DenominacionOrigen, in.Graduacion, in.Anyo, in.Active, restaurantID,
	)
	if err != nil {
		return 0, 0, err
	}
	v2ID, err := res.LastInsertId()
	if err != nil {
		return 0, 0, err
	}

	var legacyNum int64
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(legacy_num), 0) + 1 FROM legacy_wine_map WHERE restaurant_id = ?`,
		restaurantID,
	).Scan(&legacyNum); err != nil {
		return 0, 0, err
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO legacy_wine_map (restaurant_id, legacy_num, v2_wine_id) VALUES (?, ?, ?)`,
		restaurantID, legacyNum, v2ID,
	); err != nil {
		return 0, 0, err
	}

	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	return v2ID, legacyNum, nil
}

// deleteVinoV2 removes a wine and its map row in one transaction, reporting
// whether the catalogue row existed so callers keep their "not found" answer.
func (s *Server) deleteVinoV2(ctx context.Context, restaurantID int, legacyNum int) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx,
		`DELETE FROM group_menu_wines_v2 WHERE `+vinoV2WhereLegacyNum,
		vinoV2LegacyNumArgs(restaurantID, legacyNum)...,
	)
	if err != nil {
		return false, err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return false, nil
	}

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM legacy_wine_map WHERE restaurant_id = ? AND legacy_num = ?`,
		restaurantID, legacyNum,
	); err != nil {
		return false, err
	}

	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
