package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"preactvillacarmen/internal/httpx"
)

// The bulk wizard links many products to technical sheets at once. Two
// properties make that safe to offer:
//
//   - preview and apply are separate, so nothing is written until the user has
//     seen the exact list;
//   - apply is one transaction, so a bad row can never leave half the menu in a
//     state nobody reviewed.

type bulkLinkRow struct {
	ItemID        int64 `json:"itemId"`
	StockRecipeID int64 `json:"stockRecipeId"`
}

type bulkLinkResult struct {
	ItemID        int64  `json:"itemId"`
	ItemName      string `json:"itemName,omitempty"`
	StockRecipeID int64  `json:"stockRecipeId"`
	SheetName     string `json:"sheetName,omitempty"`
	OutputItemID  int64  `json:"outputItemId,omitempty"`
	CurrentSheet  int64  `json:"currentSheetId,omitempty"`
	Valid         bool   `json:"valid"`
	Message       string `json:"message,omitempty"`
}

// validateBulkLinks resolves every row against the tenant's own data. Rows are
// validated individually and all of them are returned: telling the user "one
// row is wrong" without saying which is useless when the batch is long.
func (s *Server) validateBulkLinks(ctx context.Context, restaurantID int, links []bulkLinkRow) ([]bulkLinkResult, int) {
	results := make([]bulkLinkResult, 0, len(links))
	validCount := 0
	for _, link := range links {
		result := bulkLinkResult{ItemID: link.ItemID, StockRecipeID: link.StockRecipeID}

		var itemName string
		var currentSheet sql.NullInt64
		err := s.db.QueryRowContext(ctx,
			`SELECT COALESCE(NULLIF(titulo,''),nombre), stock_recipe_id FROM comida_items
			  WHERE restaurant_id=? AND id=?`,
			restaurantID, link.ItemID).Scan(&itemName, &currentSheet)
		if errors.Is(err, sql.ErrNoRows) {
			result.Message = "El producto no existe"
			results = append(results, result)
			continue
		}
		if err != nil {
			result.Message = "No se pudo leer el producto"
			results = append(results, result)
			continue
		}
		result.ItemName = itemName
		if currentSheet.Valid {
			result.CurrentSheet = currentSheet.Int64
		}

		var sheetName string
		var outputItemID int64
		err = s.db.QueryRowContext(ctx,
			`SELECT name, output_item_id FROM stock_recipes WHERE restaurant_id=? AND id=?`,
			restaurantID, link.StockRecipeID).Scan(&sheetName, &outputItemID)
		if errors.Is(err, sql.ErrNoRows) {
			result.Message = "La ficha tecnica no existe"
			results = append(results, result)
			continue
		}
		if err != nil {
			result.Message = "No se pudo leer la ficha tecnica"
			results = append(results, result)
			continue
		}
		result.SheetName = sheetName
		result.OutputItemID = outputItemID
		result.Valid = true
		validCount++
		results = append(results, result)
	}
	return results, validCount
}

func decodeBulkLinkBody(w http.ResponseWriter, r *http.Request) (struct {
	IdempotencyKey string        `json:"idempotencyKey"`
	Links          []bulkLinkRow `json:"links"`
}, bool) {
	var in struct {
		IdempotencyKey string        `json:"idempotencyKey"`
		Links          []bulkLinkRow `json:"links"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(&in) != nil {
		return in, false
	}
	return in, true
}

func (s *Server) handleBOComidaBulkLinkPreview(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	in, ok := decodeBulkLinkBody(w, r)
	if !ok {
		httpx.WriteError(w, http.StatusBadRequest, "Datos invalidos")
		return
	}
	rows, validCount := s.validateBulkLinks(r.Context(), a.ActiveRestaurantID, in.Links)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true, "rows": rows,
		"validCount": validCount, "invalidCount": len(rows) - validCount,
	})
}

func (s *Server) handleBOComidaBulkLinkApply(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	in, ok := decodeBulkLinkBody(w, r)
	if !ok {
		httpx.WriteError(w, http.StatusBadRequest, "Datos invalidos")
		return
	}
	if len(in.Links) == 0 {
		httpx.WriteError(w, http.StatusBadRequest, "No hay productos que vincular")
		return
	}
	key := strings.TrimSpace(in.IdempotencyKey)
	if key == "" {
		// Without a key a retry would apply the batch twice, so it is required
		// rather than generated: the client knows which submission this is.
		httpx.WriteError(w, http.StatusBadRequest, "Falta la clave de idempotencia")
		return
	}

	// A repeat of the same submission is answered from the audit record instead
	// of being applied again.
	var existingCount int
	err := s.db.QueryRowContext(r.Context(),
		`SELECT linked_count FROM comida_bulk_link_batches WHERE restaurant_id=? AND idempotency_key=?`,
		a.ActiveRestaurantID, key).Scan(&existingCount)
	if err == nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": true, "applied": existingCount, "reused": true,
		})
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		httpx.WriteError(w, http.StatusInternalServerError, "Error comprobando el lote")
		return
	}

	rows, validCount := s.validateBulkLinks(r.Context(), a.ActiveRestaurantID, in.Links)
	if validCount != len(rows) {
		// Applying only the good rows would leave the menu in a state the user
		// never reviewed, so the whole batch is refused with the detail needed
		// to fix it.
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false, "code": "BULK_LINK_INVALID",
			"message": "Algunas filas no son validas; no se ha aplicado ningun cambio",
			"rows":    rows,
		})
		return
	}

	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error aplicando el lote")
		return
	}
	defer tx.Rollback()

	for _, row := range rows {
		if _, err := tx.ExecContext(r.Context(), `
			UPDATE comida_items
			   SET production_type='MANUFACTURED', stock_recipe_id=?, stock_item_id=?
			 WHERE restaurant_id=? AND id=?`,
			row.StockRecipeID, row.OutputItemID, a.ActiveRestaurantID, row.ItemID); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error aplicando el lote")
			return
		}
	}

	linksJSON, _ := json.Marshal(in.Links)
	// The audit row is written inside the same transaction, so the record and
	// the change can never disagree.
	if _, err := tx.ExecContext(r.Context(), `
		INSERT INTO comida_bulk_link_batches (restaurant_id,idempotency_key,links_json,linked_count,actor_user_id)
		VALUES (?,?,?,?,?)`,
		a.ActiveRestaurantID, key, string(linksJSON), len(rows), a.User.ID); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error registrando el lote")
		return
	}
	if err := tx.Commit(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error aplicando el lote")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "applied": len(rows)})
}
