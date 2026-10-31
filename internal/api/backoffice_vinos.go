package api

import (
	"database/sql"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"preactvillacarmen/internal/httpx"
)

type boVino struct {
	Num                       int     `json:"num"`
	Tipo                      string  `json:"tipo"`
	Nombre                    string  `json:"nombre"`
	Precio                    float64 `json:"precio"`
	Descripcion               string  `json:"descripcion"`
	Bodega                    string  `json:"bodega"`
	DenominacionOrigen        string  `json:"denominacion_origen"`
	Graduacion                float64 `json:"graduacion"`
	Anyo                      string  `json:"anyo"`
	Active                    bool    `json:"active"`
	HasFoto                   bool    `json:"has_foto"`
	FotoURL                   *string `json:"foto_url,omitempty"`
	AIRequestedImg            bool    `json:"ai_requested_img"`
	AIGeneratingImg           bool    `json:"ai_generating_img"`
	AIGeneratedImg            *string `json:"ai_generated_img,omitempty"`
	NombreEnglish             string  `json:"nombre_english,omitempty"`
	DescripcionEnglish        string  `json:"descripcion_english,omitempty"`
	BodegaEnglish             string  `json:"bodega_english,omitempty"`
	DenominacionOrigenEnglish string  `json:"denominacion_origen_english,omitempty"`
	TipoEnglish               string  `json:"tipo_english,omitempty"`
	// The wine detail page hydrates from this struct, so the stock link has to
	// travel with it or a saved "Preparado" wine reappears as "Materia prima".
	ProductionType string `json:"production_type"`
	StockRecipeID  *int64 `json:"stock_recipe_id"`
	StockItemID    *int64 `json:"stock_item_id"`
}

func applyVinoTranslationsBO(m map[string]string, v *boVino) {
	if v == nil || len(m) == 0 {
		return
	}
	v.NombreEnglish = translationOr(m, "nombre")
	v.DescripcionEnglish = translationOr(m, "descripcion")
	v.BodegaEnglish = translationOr(m, "bodega")
	v.DenominacionOrigenEnglish = translationOr(m, "denominacion_origen")
	v.TipoEnglish = translationOr(m, "tipo")
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

	// The catalogue is joined to legacy_wine_map, so the predicate columns are
	// qualified: restaurant_id exists on both sides of the join.
	where := "WHERE v.restaurant_id = ?"
	args := []any{restaurantID}
	if tipo != "" {
		where += " AND v.tipo = ?"
		args = append(args, tipo)
	}
	if activeOnly != nil {
		where += " AND v.active = ?"
		args = append(args, *activeOnly)
	}

	rows, err := s.db.QueryContext(r.Context(), `
			SELECT
				`+vinoV2NumExpr+` AS num,
				COALESCE(v.tipo, ''),
				COALESCE(nombre, ''),
				COALESCE(precio, 0),
				COALESCE(descripcion, ''),
				COALESCE(bodega, ''),
				COALESCE(denominacion_origen, ''),
				COALESCE(graduacion, 0),
				COALESCE(anyo, ''),
				active,
				(foto_path IS NOT NULL AND LENGTH(foto_path) > 0) AS has_foto,
				foto_path,
				COALESCE(ai_requested_img, 0),
				COALESCE(ai_generating_img, 0),
				ai_generated_img,
				COALESCE(production_type, 'RAW'),
				stock_recipe_id,
				stock_item_id
			`+vinoV2From+`
	`+where+`
			ORDER BY v.tipo ASC, v.nombre ASC, num ASC
		`, args...)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando VINOS")
		return
	}
	defer rows.Close()

	var out []boVino
	for rows.Next() {
		var (
			v             boVino
			activeInt     int
			hasFotoInt    int
			fotoPath      sql.NullString
			requestedInt  int
			generatingInt int
			generatedRaw  sql.NullString
			recipeIDRaw   sql.NullInt64
			stockItemRaw  sql.NullInt64
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
			&fotoPath,
			&requestedInt,
			&generatingInt,
			&generatedRaw,
			&v.ProductionType,
			&recipeIDRaw,
			&stockItemRaw,
		); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo VINOS")
			return
		}
		if recipeIDRaw.Valid {
			v.StockRecipeID = &recipeIDRaw.Int64
		}
		if stockItemRaw.Valid {
			v.StockItemID = &stockItemRaw.Int64
		}
		v.Active = activeInt != 0
		v.HasFoto = hasFotoInt != 0
		if fotoPath.Valid && strings.TrimSpace(fotoPath.String) != "" {
			u := s.bunnyPullURL(r.Context(), a.ActiveRestaurantID, fotoPath.String)
			v.FotoURL = &u
		}
		v.AIRequestedImg = requestedInt != 0
		v.AIGeneratingImg = generatingInt != 0
		if generatedRaw.Valid {
			if g := strings.TrimSpace(generatedRaw.String); g != "" {
				v.AIGeneratedImg = &g
			}
		}
		out = append(out, v)
	}

	if len(out) > 0 {
		ids := make([]int64, 0, len(out))
		for _, v := range out {
			ids = append(ids, int64(v.Num))
		}
		if all, err := s.loadTranslations(r.Context(), restaurantID, entityVinos, ids, translationLang); err == nil {
			for i := range out {
				applyVinoTranslationsBO(all[int64(out[i].Num)], &out[i])
			}
		}
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
	_, legacyNum, err := s.insertVinoV2(r.Context(), restaurantID, vinoV2Insert{
		Tipo:               tipo,
		Nombre:             nombre,
		Precio:             precio,
		Descripcion:        strings.TrimSpace(derefString(req.Descripcion)),
		Bodega:             bodega,
		DenominacionOrigen: strings.TrimSpace(derefString(req.DenominacionOrigen)),
		Graduacion:         derefFloat(req.Graduacion),
		Anyo:               strings.TrimSpace(derefString(req.Anyo)),
		Active:             activeInt,
	})
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error insertando el vino")
		return
	}
	newID := int(legacyNum)

	if len(img) > 0 {
		objectPath, err := s.UploadWineImage(r.Context(), a.ActiveRestaurantID, tipo, newID, img)
		if err != nil {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": true,
				"num":     newID,
				"warning": "Vino creado, pero la imagen no se pudo subir",
			})
			return
		}

		if _, err := s.db.ExecContext(r.Context(), "UPDATE group_menu_wines_v2 SET foto_path = ? WHERE "+vinoV2WhereLegacyNum, append([]any{objectPath}, vinoV2LegacyNumArgs(restaurantID, newID)...)...); err != nil {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": true,
				"num":     newID,
				"warning": "Vino creado, pero no se pudo guardar la imagen",
			})
			return
		}
	}

	if item, _, err := s.getVinoByID(r, restaurantID, newID); err == nil {
		s.translateVino(r.Context(), restaurantID, &item)
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"num":     newID,
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
			sets = append(sets, "foto_path = NULL")
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
				if err := s.db.QueryRowContext(r.Context(), "SELECT COALESCE(tipo,'') FROM group_menu_wines_v2 WHERE "+vinoV2WhereLegacyNum+" LIMIT 1", vinoV2LegacyNumArgs(a.ActiveRestaurantID, id)...).Scan(&wineTipo); err != nil || strings.TrimSpace(wineTipo) == "" {
					wineTipo = "OTROS"
				}
			}

			objectPath, err := s.UploadWineImage(r.Context(), a.ActiveRestaurantID, wineTipo, id, b)
			if err != nil {
				imageWarning = "Vino actualizado, pero la imagen no se pudo subir"
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
	args = append(args, vinoV2LegacyNumArgs(restaurantID, id)...)
	q := "UPDATE group_menu_wines_v2 SET " + strings.Join(sets, ", ") + " WHERE " + vinoV2WhereLegacyNum
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

	if item, _, err := s.getVinoByID(r, restaurantID, id); err == nil {
		s.translateVino(r.Context(), restaurantID, &item)
	}

	// Coordination id: seo_item_routes_v1 - el vino sigue su ruta SEO sin
	// esperar al siguiente build del frontend.
	s.syncSeoItemRoutes(r.Context(), restaurantID)

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
	found, err := s.deleteVinoV2(r.Context(), restaurantID, id)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error eliminando el vino")
		return
	}
	if !found {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Wine not found",
		})
		return
	}

	s.deleteEntityTranslations(r.Context(), entityVinos, restaurantID, int64(id))

	// Coordination id: seo_item_routes_v1
	s.syncSeoItemRoutes(r.Context(), restaurantID)

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

func (s *Server) handleBOVinoImageUpload(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	wineNumStr := strings.TrimSpace(chi.URLParam(r, "id"))
	wineNum, err := strconv.Atoi(wineNumStr)
	if err != nil || wineNum <= 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid wine id"})
		return
	}

	maxInput := s.openAIInputMaxBytes()
	if err := r.ParseMultipartForm(int64(maxInput)); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Error parsing form"})
		return
	}

	file, _, err := r.FormFile("image")
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "No image file provided"})
		return
	}
	defer file.Close()

	raw, err := io.ReadAll(io.LimitReader(file, int64(maxInput)+1))
	if err != nil || len(raw) == 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Error reading file"})
		return
	}
	if len(raw) > maxInput {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Image too large"})
		return
	}

	var wineTipo string
	if err := s.db.QueryRowContext(r.Context(), "SELECT COALESCE(tipo,'') FROM group_menu_wines_v2 WHERE "+vinoV2WhereLegacyNum+" LIMIT 1", vinoV2LegacyNumArgs(a.ActiveRestaurantID, wineNum)...).Scan(&wineTipo); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Wine not found"})
		return
	}

	objectPath, err := s.UploadWineImage(r.Context(), a.ActiveRestaurantID, wineTipo, wineNum, raw)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Error uploading image"})
		return
	}

	if _, err := s.db.ExecContext(r.Context(), "UPDATE group_menu_wines_v2 SET foto_path = ? WHERE "+vinoV2WhereLegacyNum, append([]any{objectPath}, vinoV2LegacyNumArgs(a.ActiveRestaurantID, wineNum)...)...); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Error saving image path"})
		return
	}

	fullURL := s.bunnyPullURL(r.Context(), a.ActiveRestaurantID, objectPath)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":  true,
		"foto_url": fullURL,
	})
}

func (s *Server) handleBOVinoGet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	wineNumStr := strings.TrimSpace(chi.URLParam(r, "id"))
	wineNum, err := strconv.Atoi(wineNumStr)
	if err != nil || wineNum <= 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid wine id"})
		return
	}

	var (
		v             boVino
		activeInt     int
		hasFotoInt    int
		fotoPath      sql.NullString
		requestedInt  int
		generatingInt int
		generatedRaw  sql.NullString
		recipeIDRaw   sql.NullInt64
		stockItemRaw  sql.NullInt64
	)
	err = s.db.QueryRowContext(r.Context(), `
		SELECT
			`+vinoV2NumExpr+` AS num,
			COALESCE(v.tipo, ''),
			COALESCE(v.nombre, ''),
			COALESCE(v.precio, 0),
			COALESCE(v.descripcion, ''),
			COALESCE(v.bodega, ''),
			COALESCE(v.denominacion_origen, ''),
			COALESCE(v.graduacion, 0),
			COALESCE(v.anyo, ''),
			v.active,
			(v.foto_path IS NOT NULL AND LENGTH(v.foto_path) > 0) AS has_foto,
			v.foto_path,
			COALESCE(v.ai_requested_img, 0),
			COALESCE(v.ai_generating_img, 0),
			v.ai_generated_img,
			COALESCE(v.production_type, 'RAW'),
			v.stock_recipe_id,
			v.stock_item_id
		`+vinoV2From+`
		WHERE `+vinoV2WhereLegacyNumJoined+`
		LIMIT 1
	`, vinoV2LegacyNumArgs(a.ActiveRestaurantID, wineNum)...).Scan(
		&v.Num, &v.Tipo, &v.Nombre, &v.Precio, &v.Descripcion,
		&v.Bodega, &v.DenominacionOrigen, &v.Graduacion, &v.Anyo,
		&activeInt, &hasFotoInt, &fotoPath,
		&requestedInt, &generatingInt, &generatedRaw,
		&v.ProductionType, &recipeIDRaw, &stockItemRaw,
	)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Wine not found"})
		return
	}
	if recipeIDRaw.Valid {
		v.StockRecipeID = &recipeIDRaw.Int64
	}
	if stockItemRaw.Valid {
		v.StockItemID = &stockItemRaw.Int64
	}
	v.Active = activeInt != 0
	v.HasFoto = hasFotoInt != 0
	if fotoPath.Valid && strings.TrimSpace(fotoPath.String) != "" {
		u := s.bunnyPullURL(r.Context(), a.ActiveRestaurantID, fotoPath.String)
		v.FotoURL = &u
	}
	v.AIRequestedImg = requestedInt != 0
	v.AIGeneratingImg = generatingInt != 0
	if generatedRaw.Valid {
		if g := strings.TrimSpace(generatedRaw.String); g != "" {
			v.AIGeneratedImg = &g
		}
	}

	applyVinoTranslationsBO(s.loadEntityTranslations(r.Context(), a.ActiveRestaurantID, entityVinos, int64(v.Num), translationLang), &v)

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"vino":    v,
	})
}
