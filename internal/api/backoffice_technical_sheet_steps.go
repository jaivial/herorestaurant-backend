package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"preactvillacarmen/internal/httpx"
	"preactvillacarmen/internal/sqldb"
)

// Steps are the "Receta" tab: an ordered, gap-free list a cook reads by number.
// step_no is always contiguous from 1, so the UI never has to renumber and two
// people looking at the same sheet always mean the same "step 3".

const maxSheetSteps = 60

// stepReorderParkOffset lifts rows clear of the 1..maxSheetSteps range while a
// reorder is in flight.
const stepReorderParkOffset = 100000

func (s *Server) handleBOTechnicalSheetStepCreate(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	sheetID := sheetIDParam(r)

	var in struct {
		Title       string `json:"title"`
		Description string `json:"description"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Paso invalido")
		return
	}
	if _, err := s.sheetOwnedByTenant(r.Context(), a.ActiveRestaurantID, sheetID); err != nil {
		httpx.WriteError(w, http.StatusNotFound, "Ficha tecnica no encontrada")
		return
	}

	var count int
	s.db.QueryRowContext(r.Context(),
		portableSQL(`SELECT COUNT(*) FROM stock_recipe_steps WHERE restaurant_id=? AND recipe_id=?`),
		a.ActiveRestaurantID, sheetID).Scan(&count)
	if count >= maxSheetSteps {
		httpx.WriteError(w, http.StatusBadRequest, "Demasiados pasos en esta ficha")
		return
	}

	stepID, err := sqldb.InsertID(r.Context(), sqlDialect, s.db, portableSQL(`
		INSERT INTO stock_recipe_steps (restaurant_id,recipe_id,step_no,title,description)
		VALUES (?,?,?,?,?)`), "id",
		a.ActiveRestaurantID, sheetID, count+1, in.Title, in.Description)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "No se pudo crear el paso")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "stepId": stepID, "stepNo": count + 1})
}

func (s *Server) handleBOTechnicalSheetStepPatch(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	sheetID := sheetIDParam(r)
	stepID, _ := strconv.ParseInt(chi.URLParam(r, "stepId"), 10, 64)

	var in struct {
		Title       *string `json:"title"`
		Description *string `json:"description"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Datos invalidos")
		return
	}
	// RowsAffected contract (no clientFoundRows): MySQL reports CHANGED rows, so a
	// same-value update answers 404 here. PG reports matched rows, so its branch
	// only updates when the tuple really changes: a byte-wise (COLLATE "C")
	// IS DISTINCT FROM, like MySQL's changed-record check (old-nondeterministic-collation would treat a
	// case-only edit as unchanged). No write means no updated_at trigger, as in
	// MySQL. Values are bound once through VALUES, in the original order.
	res, err := s.db.ExecContext(r.Context(), nativeSQL(`
		UPDATE stock_recipe_steps
		   SET title=COALESCE(?,title), description=COALESCE(?,description)
		 WHERE restaurant_id=? AND id=? AND recipe_id=?`, `
		UPDATE stock_recipe_steps t
		   SET title=COALESCE(v.ti, t.title), description=COALESCE(v.de, t.description)
		  FROM (VALUES (CAST(? AS text), CAST(? AS text))) AS v(ti, de)
		 WHERE t.restaurant_id=? AND t.id=? AND t.recipe_id=?
		   AND (t.title COLLATE "C", t.description COLLATE "C")
		       IS DISTINCT FROM (COALESCE(v.ti, t.title) COLLATE "C", COALESCE(v.de, t.description) COLLATE "C")`),
		in.Title, in.Description, a.ActiveRestaurantID, stepID, sheetID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error actualizando el paso")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		httpx.WriteError(w, http.StatusNotFound, "Paso no encontrado")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) handleBOTechnicalSheetStepDelete(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	sheetID := sheetIDParam(r)
	stepID, _ := strconv.ParseInt(chi.URLParam(r, "stepId"), 10, 64)

	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error eliminando el paso")
		return
	}
	defer tx.Rollback()

	var stepNo int
	if err := tx.QueryRowContext(r.Context(),
		portableSQL(`SELECT step_no FROM stock_recipe_steps WHERE restaurant_id=? AND id=? AND recipe_id=? FOR UPDATE`),
		a.ActiveRestaurantID, stepID, sheetID).Scan(&stepNo); err != nil {
		httpx.WriteError(w, http.StatusNotFound, "Paso no encontrado")
		return
	}
	if _, err := tx.ExecContext(r.Context(),
		portableSQL(`DELETE FROM stock_recipe_steps WHERE restaurant_id=? AND id=?`), a.ActiveRestaurantID, stepID); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error eliminando el paso")
		return
	}
	// Close the hole in the same transaction: a sheet that shows "1, 3, 4" for
	// even a moment is a real kitchen error, not a cosmetic one.
	if sqlDialect == sqldb.Postgres {
		// PG has no UPDATE ... ORDER BY and checks uq_stock_recipe_step_no per
		// row, so the MySQL ordered shift could collide. Same park technique as
		// the reorder below (CHECK step_no > 0 forbids negatives): park every
		// later step above stepReorderParkOffset, then bring it down by offset+1.
		if _, err := tx.ExecContext(r.Context(), pgOnlySQL(`UPDATE stock_recipe_steps SET step_no=step_no+?
		 WHERE restaurant_id=? AND recipe_id=? AND step_no>?`), stepReorderParkOffset, a.ActiveRestaurantID, sheetID, stepNo); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error renumerando los pasos")
			return
		}
		if _, err := tx.ExecContext(r.Context(), pgOnlySQL(`UPDATE stock_recipe_steps SET step_no=step_no-?-1
		 WHERE restaurant_id=? AND recipe_id=? AND step_no>?`), stepReorderParkOffset, a.ActiveRestaurantID, sheetID, stepReorderParkOffset); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error renumerando los pasos")
			return
		}
	} else if _, err := tx.ExecContext(r.Context(), mysqlOnlySQL(`
		UPDATE stock_recipe_steps SET step_no=step_no-1
		 WHERE restaurant_id=? AND recipe_id=? AND step_no>?
		 ORDER BY step_no`), a.ActiveRestaurantID, sheetID, stepNo); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error renumerando los pasos")
		return
	}
	if err := tx.Commit(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error eliminando el paso")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) handleBOTechnicalSheetStepsReorder(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	sheetID := sheetIDParam(r)

	var in struct {
		StepIDs []int64 `json:"stepIds"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Orden invalido")
		return
	}

	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error reordenando")
		return
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(r.Context(),
		portableSQL(`SELECT id FROM stock_recipe_steps WHERE restaurant_id=? AND recipe_id=? FOR UPDATE`),
		a.ActiveRestaurantID, sheetID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error reordenando")
		return
	}
	existing := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			httpx.WriteError(w, http.StatusInternalServerError, "Error reordenando")
			return
		}
		existing[id] = true
	}
	rows.Close()

	// The payload must be a permutation of this sheet's steps. A partial or
	// foreign list would leave steps unnumbered or steal another sheet's rows,
	// so it is rejected before anything is written.
	if len(in.StepIDs) != len(existing) {
		httpx.WriteError(w, http.StatusBadRequest, "El orden debe incluir todos los pasos")
		return
	}
	seen := map[int64]bool{}
	for _, id := range in.StepIDs {
		if !existing[id] || seen[id] {
			httpx.WriteError(w, http.StatusBadRequest, "El orden contiene pasos no validos")
			return
		}
		seen[id] = true
	}

	// step_no carries a unique key per recipe and a CHECK(step_no > 0), so the
	// rows are parked above the final range instead of negated: the offset is
	// far beyond maxSheetSteps, so parked and final values can never collide.
	for _, id := range in.StepIDs {
		if _, err := tx.ExecContext(r.Context(),
			portableSQL(`UPDATE stock_recipe_steps SET step_no=step_no+? WHERE restaurant_id=? AND id=?`),
			stepReorderParkOffset, a.ActiveRestaurantID, id); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error reordenando")
			return
		}
	}
	for index, id := range in.StepIDs {
		if _, err := tx.ExecContext(r.Context(),
			portableSQL(`UPDATE stock_recipe_steps SET step_no=? WHERE restaurant_id=? AND id=?`),
			index+1, a.ActiveRestaurantID, id); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error reordenando")
			return
		}
	}
	if err := tx.Commit(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error reordenando")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) handleBOTechnicalSheetStepsList(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	sheetID := sheetIDParam(r)
	rows, err := s.db.QueryContext(r.Context(), portableSQL(`
		SELECT id, step_no, COALESCE(title,''), COALESCE(description,''), COALESCE(image_url,''),
		       generation_status, generation_mode, COALESCE(generation_error,'')
		  FROM stock_recipe_steps
		 WHERE restaurant_id=? AND recipe_id=? ORDER BY step_no`), a.ActiveRestaurantID, sheetID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error cargando pasos")
		return
	}
	defer rows.Close()
	steps := []map[string]any{}
	for rows.Next() {
		var id int64
		var stepNo int
		var title, description, imageURL, status, mode, genError string
		var modeNull sql.NullString
		if err := rows.Scan(&id, &stepNo, &title, &description, &imageURL, &status, &modeNull, &genError); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo pasos")
			return
		}
		mode = modeNull.String
		steps = append(steps, map[string]any{
			"id": id, "stepNo": stepNo, "title": title, "description": description,
			"imageUrl": imageURL, "generationStatus": status, "generationMode": mode,
			"generationError": genError,
		})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "steps": steps})
}
