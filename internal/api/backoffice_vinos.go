package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"preactvillacarmen/internal/httpx"
)

type boVino struct {
	Num                int     `json:"num"`
	Tipo               string  `json:"tipo"`
	Nombre             string  `json:"nombre"`
	Precio             float64 `json:"precio"`
	Descripcion        string  `json:"descripcion"`
	Bodega             string  `json:"bodega"`
	DenominacionOrigen string  `json:"denominacion_origen"`
	Graduacion         float64 `json:"graduacion"`
	Anyo               string  `json:"anyo"`
	Active             bool    `json:"active"`
	HasFoto            bool    `json:"has_foto"`
}

func (s *Server) handleBOVinosList(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	tipo := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("tipo")))
	activeFilter := strings.TrimSpace(r.URL.Query().Get("active"))
	var (
		activeOnly *int
	)
	if activeFilter != "" {
		if v, err := strconv.Atoi(activeFilter); err == nil {
			if v != 0 {
				v = 1
			}
			activeOnly = &v
		}
	}

	restaurantID := a.ActiveRestaurantID

	where := "WHERE restaurant_id = ?"
	args := []any{restaurantID}
	if tipo != "" {
		where += " AND tipo = ?"
		args = append(args, tipo)
	}
	if activeOnly != nil {
		where += " AND active = ?"
		args = append(args, *activeOnly)
	}

	rows, err := s.db.QueryContext(r.Context(), `
			SELECT
				num,
				COALESCE(tipo, ''),
				COALESCE(nombre, ''),
				COALESCE(precio, 0),
				COALESCE(descripcion, ''),
				COALESCE(bodega, ''),
				COALESCE(denominacion_origen, ''),
				COALESCE(graduacion, 0),
				COALESCE(anyo, ''),
				active,
				(foto_path IS NOT NULL AND LENGTH(foto_path) > 0) AS has_foto
			FROM VINOS
		`+where+`
			ORDER BY tipo ASC, nombre ASC, num ASC
		`, args...)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando VINOS")
		return
	}
	defer rows.Close()

	var out []boVino
	for rows.Next() {
		var (
			v          boVino
			activeInt  int
			hasFotoInt int
		)
		if err := rows.Scan(
			&v.Num,
			&v.Tipo,
			&v.Nombre,
			&v.Precio,
			&v.Descripcion,
			&v.Bodega,
			&v.DenominacionOrigen,
			&v.Graduacion,
			&v.Anyo,
			&activeInt,
			&hasFotoInt,
		); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo VINOS")
			return
		}
		v.Active = activeInt != 0
		v.HasFoto = hasFotoInt != 0
		out = append(out, v)
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"vinos":   out,
	})
}

// Create/patch accept JSON only for now. Photo upload is supported via base64 string.
type boVinoUpsertRequest struct {
	Tipo               *string  `json:"tipo,omitempty"`
	Nombre             *string  `json:"nombre,omitempty"`
	Precio             *float64 `json:"precio,omitempty"`
	Descripcion        *string  `json:"descripcion,omitempty"`
	Bodega             *string  `json:"bodega,omitempty"`
	DenominacionOrigen *string  `json:"denominacion_origen,omitempty"`
	Graduacion         *float64 `json:"graduacion,omitempty"`
	Anyo               *string  `json:"anyo,omitempty"`
	Active             *bool    `json:"active,omitempty"`
	ImageBase64        *string  `json:"imageBase64,omitempty"`
}

func (s *Server) handleBOVinoCreate(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req boVinoUpsertRequest
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Invalid JSON",
		})
		return
	}

	tipo := strings.ToUpper(strings.TrimSpace(derefString(req.Tipo)))
	nombre := strings.TrimSpace(derefString(req.Nombre))
	bodega := strings.TrimSpace(derefString(req.Bodega))
	if tipo == "" || nombre == "" || bodega == "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "tipo, nombre y bodega son requeridos",
		})
		return
	}

	precio := derefFloat(req.Precio)
	if precio <= 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "precio requerido",
		})
		return
	}

	activeInt := 1
	if req.Active != nil && !*req.Active {
		activeInt = 0
	}

	var img []byte
	if req.ImageBase64 != nil && strings.TrimSpace(*req.ImageBase64) != "" {
		b, err := decodeBase64Image(*req.ImageBase64)
		if err != nil {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"message": "Imagen invalida",
			})
			return
		}
		img = b
	}

	restaurantID := a.ActiveRestaurantID
	res, err := s.db.ExecContext(r.Context(), `
			INSERT INTO VINOS
				(restaurant_id, tipo, nombre, precio, descripcion, bodega, denominacion_origen, graduacion, anyo, active, foto_path, foto)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, NULL)
		`, restaurantID,
		tipo,
		nombre,
		precio,
		strings.TrimSpace(derefString(req.Descripcion)),
		bodega,
		strings.TrimSpace(derefString(req.DenominacionOrigen)),
		derefFloat(req.Graduacion),
		strings.TrimSpace(derefString(req.Anyo)),
		activeInt,
	)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error insertando VINOS")
		return
	}
	newID, _ := res.LastInsertId()

	if len(img) > 0 {
		objectPath, err := s.UploadWineImage(r.Context(), tipo, int(newID), img)
		if err != nil {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": true,
				"num":     int(newID),
				"warning": "Vino creado, pero la imagen no se pudo subir",
			})
			return
		}

		if _, err := s.db.ExecContext(r.Context(), "UPDATE VINOS SET foto_path = ?, foto = NULL WHERE num = ? AND restaurant_id = ?", objectPath, int(newID), restaurantID); err != nil {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": true,
				"num":     int(newID),
				"warning": "Vino creado, pero no se pudo guardar la imagen",
			})
			return
		}
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"num":     int(newID),
	})
}

func (s *Server) handleBOVinoPatch(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	idStr := strings.TrimSpace(chi.URLParam(r, "id"))
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Invalid wine id",
		})
		return
	}

	var req boVinoUpsertRequest
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Invalid JSON",
		})
		return
	}

	var (
		sets []string
		args []any
	)
	imageWarning := ""
	if req.Tipo != nil {
		t := strings.ToUpper(strings.TrimSpace(*req.Tipo))
		if t == "" {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"message": "tipo invalido",
			})
			return
		}
		sets = append(sets, "tipo = ?")
		args = append(args, t)
	}
	if req.Nombre != nil {
		v := strings.TrimSpace(*req.Nombre)
		if v == "" {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"message": "nombre invalido",
			})
			return
		}
		sets = append(sets, "nombre = ?")
		args = append(args, v)
	}
	if req.Precio != nil {
		if *req.Precio <= 0 {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"message": "precio invalido",
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
	if req.Bodega != nil {
		v := strings.TrimSpace(*req.Bodega)
		if v == "" {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"message": "bodega invalida",
			})
			return
		}
		sets = append(sets, "bodega = ?")
		args = append(args, v)
	}
	if req.DenominacionOrigen != nil {
		sets = append(sets, "denominacion_origen = ?")
		args = append(args, strings.TrimSpace(*req.DenominacionOrigen))
	}
	if req.Graduacion != nil {
		sets = append(sets, "graduacion = ?")
		args = append(args, *req.Graduacion)
	}
	if req.Anyo != nil {
		sets = append(sets, "anyo = ?")
		args = append(args, strings.TrimSpace(*req.Anyo))
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
			sets = append(sets, "foto_path = NULL", "foto = NULL")
		} else {
			b, err := decodeBase64Image(raw)
			if err != nil {
				httpx.WriteJSON(w, http.StatusOK, map[string]any{
					"success": false,
					"message": "Imagen invalida",
				})
				return
			}

			wineTipo := strings.ToUpper(strings.TrimSpace(derefString(req.Tipo)))
			if wineTipo == "" {
				if err := s.db.QueryRowContext(r.Context(), "SELECT COALESCE(tipo,'') FROM VINOS WHERE num = ? AND restaurant_id = ? LIMIT 1", id, a.ActiveRestaurantID).Scan(&wineTipo); err != nil || strings.TrimSpace(wineTipo) == "" {
					wineTipo = "OTROS"
				}
			}

			objectPath, err := s.UploadWineImage(r.Context(), wineTipo, id, b)
			if err != nil {
				imageWarning = "Vino actualizado, pero la imagen no se pudo subir"
			} else {
				sets = append(sets, "foto_path = ?", "foto = NULL")
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
	q := "UPDATE VINOS SET " + strings.Join(sets, ", ") + " WHERE num = ? AND restaurant_id = ?"
	res, err := s.db.ExecContext(r.Context(), q, args...)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error actualizando VINOS")
		return
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Wine not found",
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

func (s *Server) handleBOVinoDelete(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	idStr := strings.TrimSpace(chi.URLParam(r, "id"))
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Invalid wine id",
		})
		return
	}

	restaurantID := a.ActiveRestaurantID
	res, err := s.db.ExecContext(r.Context(), "DELETE FROM VINOS WHERE num = ? AND restaurant_id = ?", id, restaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error eliminando VINOS")
		return
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Wine not found",
		})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
	})
}

func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func derefFloat(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}
