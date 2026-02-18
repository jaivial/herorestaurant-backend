package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"preactvillacarmen/internal/httpx"
)

// Food types for different categories
const (
	FoodTypeCafe   = "CAFE"
	FoodTypeBebida = "BEBIDA"
	FoodTypePlato  = "PLATO"
)

// boFoodItem represents a food item (cafe, bebida, plato)
type boFoodItem struct {
	Num          int      `json:"num"`
	Tipo         string   `json:"tipo"`
	Nombre       string   `json:"nombre"`
	Precio       float64  `json:"precio"`
	Descripcion  string   `json:"descripcion"`
	Titulo       string   `json:"titulo"`
	Suplemento   float64  `json:"suplemento"`
	Alergenos    []string `json:"alergenos"`
	Active       bool     `json:"active"`
	HasFoto      bool     `json:"has_foto"`
	FotoURL      string   `json:"foto_url,omitempty"`
}

func parseAlergenosFromJSON(alergRaw sql.NullString) []string {
	if !alergRaw.Valid || alergRaw.String == "" {
		return nil
	}
	var alergs []string
	if err := json.Unmarshal([]byte(alergRaw.String), &alergs); err != nil {
		return nil
	}
	return alergs
}

// Food table mapping
type foodTable struct {
	name     string
	typeName string
}

var foodTables = map[string]foodTable{
	"cafes":   {name: "CAKES", typeName: FoodTypeCafe},
	"bebidas": {name: "BEBIDAS", typeName: FoodTypeBebida},
	"platos":  {name: "PLATOS", typeName: FoodTypePlato},
}

// handleBOFoodList - GET /admin/{cafes,bebidas,platos}
func (s *Server) handleBOFoodList(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	foodType := strings.TrimSpace(chi.URLParam(r, "type"))
	table, ok := foodTables[foodType]
	if !ok {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid food type")
		return
	}

	tipoFilter := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("tipo")))
	activeFilter := strings.TrimSpace(r.URL.Query().Get("active"))
	var activeOnly *int
	if activeFilter != "" {
		if v, err := strconv.Atoi(activeFilter); err == nil {
			if v != 0 {
				v = 1
			}
			activeOnly = &v
		}
	}

	searchQuery := strings.TrimSpace(r.URL.Query().Get("search"))

	restaurantID := a.ActiveRestaurantID

	where := "WHERE restaurant_id = ?"
	args := []any{restaurantID}
	if tipoFilter != "" {
		where += " AND tipo = ?"
		args = append(args, tipoFilter)
	}
	if activeOnly != nil {
		where += " AND active = ?"
		args = append(args, *activeOnly)
	}
	if searchQuery != "" {
		where += " AND (nombre LIKE ? OR descripcion LIKE ? OR titulo LIKE ?)"
		searchPattern := "%" + searchQuery + "%"
		args = append(args, searchPattern, searchPattern, searchPattern)
	}

	rows, err := s.db.QueryContext(r.Context(), `
		SELECT
			num,
			COALESCE(tipo, ''),
			COALESCE(nombre, ''),
			COALESCE(precio, 0),
			COALESCE(descripcion, ''),
			COALESCE(titulo, ''),
			COALESCE(suplemento, 0),
			COALESCE(alergenos, '[]'),
			active,
			(foto_path IS NOT NULL AND LENGTH(foto_path) > 0) AS has_foto,
			foto_path
		FROM `+table.name+`
		`+where+`
		ORDER BY tipo ASC, nombre ASC, num ASC
	`, args...)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consulting "+table.name)
		return
	}
	defer rows.Close()

	var out []boFoodItem
	for rows.Next() {
		var (
			f          boFoodItem
			activeInt  int
			hasFotoInt int
			alergRaw   sql.NullString
			fotoPath   sql.NullString
		)
		if err := rows.Scan(
			&f.Num,
			&f.Tipo,
			&f.Nombre,
			&f.Precio,
			&f.Descripcion,
			&f.Titulo,
			&f.Suplemento,
			&alergRaw,
			&activeInt,
			&hasFotoInt,
			&fotoPath,
		); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error reading "+table.name)
			return
		}
		f.Active = activeInt != 0
		f.HasFoto = hasFotoInt != 0
		if f.HasFoto && fotoPath.Valid {
			f.FotoURL = s.bunnyPullURL(fotoPath.String)
		}
		f.Alergenos = parseAlergenosFromJSON(alergRaw)
		out = append(out, f)
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"items":   out,
	})
}

// boFoodUpsertRequest - POST/PATCH request body
type boFoodUpsertRequest struct {
	Tipo         *string  `json:"tipo,omitempty"`
	Nombre       *string  `json:"nombre,omitempty"`
	Precio       *float64 `json:"precio,omitempty"`
	Descripcion  *string  `json:"descripcion,omitempty"`
	Titulo       *string  `json:"titulo,omitempty"`
	Suplemento   *float64 `json:"suplemento,omitempty"`
	Alergenos    *[]string `json:"alergenos,omitempty"`
	Active       *bool    `json:"active,omitempty"`
	ImageBase64  *string  `json:"imageBase64,omitempty"`
}

// handleBOFoodCreate - POST /admin/{cafes,bebidas,platos}
func (s *Server) handleBOFoodCreate(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	foodType := strings.TrimSpace(chi.URLParam(r, "type"))
	table, ok := foodTables[foodType]
	if !ok {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid food type")
		return
	}

	var req boFoodUpsertRequest
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Invalid JSON",
		})
		return
	}

	tipo := strings.ToUpper(strings.TrimSpace(derefString(req.Tipo)))
	if tipo == "" {
		tipo = table.typeName
	}
	nombre := strings.TrimSpace(derefString(req.Nombre))
	if nombre == "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "nombre requerido",
		})
		return
	}

	precio := derefFloat(req.Precio)
	if precio < 0 {
		precio = 0
	}

	suplemento := derefFloat(req.Suplemento)
	if suplemento < 0 {
		suplemento = 0
	}

	active := true
	if req.Active != nil {
		active = *req.Active
	}
	activeInt := 0
	if active {
		activeInt = 1
	}

	var img []byte
	if req.ImageBase64 != nil && strings.TrimSpace(*req.ImageBase64) != "" {
		b, err := decodeBase64Image(*req.ImageBase64)
		if err != nil {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"message": "Imagen inválida",
			})
			return
		}
		img = b
	}

	var alergenosJSON string
	if req.Alergenos != nil && len(*req.Alergenos) > 0 {
		alergJSON, _ := json.Marshal(*req.Alergenos)
		alergenosJSON = string(alergJSON)
	} else {
		alergenosJSON = "[]"
	}

	restaurantID := a.ActiveRestaurantID
	res, err := s.db.ExecContext(r.Context(), `
		INSERT INTO `+table.name+`
			(restaurant_id, tipo, nombre, precio, descripcion, titulo, suplemento, alergenos, active, foto_path)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)
	`, restaurantID,
		tipo,
		nombre,
		precio,
		strings.TrimSpace(derefString(req.Descripcion)),
		strings.TrimSpace(derefString(req.Titulo)),
		suplemento,
		alergenosJSON,
		activeInt,
	)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error inserting into "+table.name)
		return
	}
	newID, _ := res.LastInsertId()

	if len(img) > 0 {
		objectPath, err := s.UploadFoodImage(r.Context(), restaurantID, foodType, int(newID), img)
		if err != nil {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": true,
				"num":     int(newID),
				"warning": "Item creado, pero la imagen no se pudo subir",
			})
			return
		}

		if _, err := s.db.ExecContext(r.Context(), "UPDATE "+table.name+" SET foto_path = ? WHERE num = ? AND restaurant_id = ?", objectPath, int(newID), restaurantID); err != nil {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": true,
				"num":     int(newID),
				"warning": "Item creado, pero no se pudo guardar la imagen",
			})
			return
		}
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"num":     int(newID),
	})
}

// handleBOFoodPatch - PATCH /admin/{cafes,bebidas,platos}/{id}
func (s *Server) handleBOFoodPatch(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	foodType := strings.TrimSpace(chi.URLParam(r, "type"))
	table, ok := foodTables[foodType]
	if !ok {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid food type")
		return
	}

	idStr := strings.TrimSpace(chi.URLParam(r, "id"))
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Invalid item id",
		})
		return
	}

	var req boFoodUpsertRequest
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Invalid JSON",
		})
		return
	}

	var sets []string
	var args []any
	imageWarning := ""

	if req.Tipo != nil {
		t := strings.ToUpper(strings.TrimSpace(*req.Tipo))
		if t == "" {
			t = table.typeName
		}
		sets = append(sets, "tipo = ?")
		args = append(args, t)
	}
	if req.Nombre != nil {
		v := strings.TrimSpace(*req.Nombre)
		if v == "" {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"message": "nombre inválido",
			})
			return
		}
		sets = append(sets, "nombre = ?")
		args = append(args, v)
	}
	if req.Precio != nil {
		if *req.Precio < 0 {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"message": "precio inválido",
			})
			return
		}
		sets = append(sets, "precio = ?")
		args = append(args, *req.Precio)
	}
	if req.Descripcion != nil {
		sets = append(sets, "descripcion = ?")
		args = append(args, strings.TrimSpace(*req.Descripcion))
	}
	if req.Titulo != nil {
		sets = append(sets, "titulo = ?")
		args = append(args, strings.TrimSpace(*req.Titulo))
	}
	if req.Suplemento != nil {
		if *req.Suplemento < 0 {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"message": "suplemento inválido",
			})
			return
		}
		sets = append(sets, "suplemento = ?")
		args = append(args, *req.Suplemento)
	}
	if req.Alergenos != nil {
		alergJSON, _ := json.Marshal(*req.Alergenos)
		sets = append(sets, "alergenos = ?")
		args = append(args, string(alergJSON))
	}
	if req.Active != nil {
		activeInt := 0
		if *req.Active {
			activeInt = 1
		}
		sets = append(sets, "active = ?")
		args = append(args, activeInt)
	}
	if req.ImageBase64 != nil {
		raw := strings.TrimSpace(*req.ImageBase64)
		if raw == "" {
			sets = append(sets, "foto_path = NULL")
		} else {
			b, err := decodeBase64Image(raw)
			if err != nil {
				httpx.WriteJSON(w, http.StatusOK, map[string]any{
					"success": false,
					"message": "Imagen inválida",
				})
				return
			}

			restaurantID := a.ActiveRestaurantID
			objectPath, err := s.UploadFoodImage(r.Context(), restaurantID, foodType, id, b)
			if err != nil {
				imageWarning = "Item actualizado, pero la imagen no se pudo subir"
			} else {
				sets = append(sets, "foto_path = ?")
				args = append(args, objectPath)
			}
		}

		if len(sets) == 0 && imageWarning != "" {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"message": imageWarning,
			})
			return
		}
	}
	if len(sets) == 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "No fields to update",
		})
		return
	}

	restaurantID := a.ActiveRestaurantID
	args = append(args, id, restaurantID)
	q := "UPDATE " + table.name + " SET " + strings.Join(sets, ", ") + " WHERE num = ? AND restaurant_id = ?"
	res, err := s.db.ExecContext(r.Context(), q, args...)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error updating "+table.name)
		return
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Item not found",
		})
		return
	}

	out := map[string]any{
		"success": true,
	}
	if imageWarning != "" {
		out["warning"] = imageWarning
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// handleBOFoodDelete - DELETE /admin/{cafes,bebidas,platos}/{id}
func (s *Server) handleBOFoodDelete(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	foodType := strings.TrimSpace(chi.URLParam(r, "type"))
	table, ok := foodTables[foodType]
	if !ok {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid food type")
		return
	}

	idStr := strings.TrimSpace(chi.URLParam(r, "id"))
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Invalid item id",
		})
		return
	}

	restaurantID := a.ActiveRestaurantID
	res, err := s.db.ExecContext(r.Context(), "DELETE FROM "+table.name+" WHERE num = ? AND restaurant_id = ?", id, restaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error deleting from "+table.name)
		return
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Item not found",
		})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
	})
}

// handleBOFoodToggleActive - POST /admin/{cafes,bebidas,platos}/{id}/toggle
func (s *Server) handleBOFoodToggleActive(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	foodType := strings.TrimSpace(chi.URLParam(r, "type"))
	table, ok := foodTables[foodType]
	if !ok {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid food type")
		return
	}

	idStr := strings.TrimSpace(chi.URLParam(r, "id"))
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Invalid item id",
		})
		return
	}

	restaurantID := a.ActiveRestaurantID

	// Get current active status
	var currentActive int
	err = s.db.QueryRowContext(r.Context(), "SELECT COALESCE(active, 0) FROM "+table.name+" WHERE num = ? AND restaurant_id = ?", id, restaurantID).Scan(&currentActive)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error reading "+table.name)
		return
	}

	newActive := 0
	if currentActive == 0 {
		newActive = 1
	}

	res, err := s.db.ExecContext(r.Context(), "UPDATE "+table.name+" SET active = ? WHERE num = ? AND restaurant_id = ?", newActive, id, restaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error updating "+table.name)
		return
	}

	affected, _ := res.RowsAffected()
	if affected == 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Item not found",
		})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"active":  newActive == 1,
	})
}
