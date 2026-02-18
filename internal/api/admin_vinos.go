package api

import (
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"preactvillacarmen/internal/httpx"
)

func (s *Server) handleVinosAdmin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpx.WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Legacy admin uses FormData (multipart/form-data). Support urlencoded as fallback.
	if err := parseLegacyForm(r, 32<<20); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Invalid form data",
		})
		return
	}

	action := strings.TrimSpace(r.Form.Get("action"))
	switch action {
	case "update_status":
		s.handleVinosUpdateStatus(w, r)
		return
	case "delete":
		s.handleVinosDelete(w, r)
		return
	case "update":
		s.handleVinosUpdate(w, r)
		return
	case "add":
		s.handleVinosAdd(w, r)
		return
	default:
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Acción desconocida: " + action,
		})
		return
	}
}

func (s *Server) handleVinosUpdateStatus(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"message": "Unknown restaurant",
		})
		return
	}

	wineID, err := parsePositiveInt(r.Form.Get("wineId"))
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "ID de vino no válido",
		})
		return
	}
	status, _ := strconv.Atoi(strings.TrimSpace(r.Form.Get("status")))
	if status != 0 {
		status = 1
	}

	if _, err := s.db.ExecContext(r.Context(), "UPDATE VINOS SET active = ? WHERE restaurant_id = ? AND NUM = ?", status, restaurantID, wineID); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error al actualizar el estado")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
	})
}

func (s *Server) handleVinosDelete(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"message": "Unknown restaurant",
		})
		return
	}

	wineID, err := parsePositiveInt(r.Form.Get("wineId"))
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "ID de vino no válido",
		})
		return
	}

	if _, err := s.db.ExecContext(r.Context(), "DELETE FROM VINOS WHERE restaurant_id = ? AND NUM = ?", restaurantID, wineID); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error al eliminar el vino")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
	})
}

func (s *Server) handleVinosUpdate(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"message": "Unknown restaurant",
		})
		return
	}

	wineID, err := parsePositiveInt(r.Form.Get("wineId"))
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Faltan campos obligatorios",
		})
		return
	}

	nombre := strings.TrimSpace(r.Form.Get("nombre"))
	descripcion := strings.TrimSpace(r.Form.Get("descripcion"))
	precio, err := parseFloatLoose(r.Form.Get("precio"))
	if nombre == "" || err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Faltan campos obligatorios",
		})
		return
	}
	bodega := strings.TrimSpace(r.Form.Get("bodega"))
	denominacionOrigen := strings.TrimSpace(r.Form.Get("denominacion_origen"))
	graduacion, _ := parseFloatLoose(r.Form.Get("graduacion"))
	anyo := strings.TrimSpace(r.Form.Get("anyo"))

	if _, err := s.db.ExecContext(
		r.Context(),
		"UPDATE VINOS SET nombre = ?, precio = ?, descripcion = ?, bodega = ?, denominacion_origen = ?, graduacion = ?, anyo = ? WHERE restaurant_id = ? AND NUM = ?",
		nombre, precio, descripcion, bodega, denominacionOrigen, graduacion, anyo, restaurantID, wineID,
	); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error al actualizar el vino")
		return
	}

	img, hasImg, imgErr := extractWineImage(r)
	if imgErr != nil {
		// Match legacy behavior: ignore image decode errors and still report success.
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": true,
			"warning": "Vino actualizado, pero la imagen no se pudo procesar",
		})
		return
	}

	if hasImg {
		var wineTipo string
		if err := s.db.QueryRowContext(r.Context(), "SELECT COALESCE(tipo,'') FROM VINOS WHERE restaurant_id = ? AND num = ? LIMIT 1", restaurantID, wineID).Scan(&wineTipo); err != nil || strings.TrimSpace(wineTipo) == "" {
			wineTipo = "OTROS"
		}

		objectPath, err := s.UploadWineImage(r.Context(), wineTipo, wineID, img)
		if err != nil {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": true,
				"warning": "Vino actualizado pero la imagen no se pudo subir",
			})
			return
		}

		if _, err := s.db.ExecContext(r.Context(), "UPDATE VINOS SET foto_path = ?, foto = NULL WHERE restaurant_id = ? AND NUM = ?", objectPath, restaurantID, wineID); err != nil {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": true,
				"warning": "Vino actualizado pero la imagen no se pudo actualizar",
			})
			return
		}
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
	})
}

func (s *Server) handleVinosAdd(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"message": "Unknown restaurant",
		})
		return
	}

	tipo := strings.ToUpper(strings.TrimSpace(r.Form.Get("tipo")))
	nombre := strings.TrimSpace(r.Form.Get("nombre"))
	descripcion := strings.TrimSpace(r.Form.Get("descripcion"))
	precio, err := parseFloatLoose(r.Form.Get("precio"))
	bodega := strings.TrimSpace(r.Form.Get("bodega"))
	denominacionOrigen := strings.TrimSpace(r.Form.Get("denominacion_origen"))
	graduacion, _ := parseFloatLoose(r.Form.Get("graduacion"))
	anyo := strings.TrimSpace(r.Form.Get("anyo"))

	if tipo == "" || nombre == "" || err != nil || bodega == "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Faltan campos obligatorios",
		})
		return
	}

	switch tipo {
	case "TINTO", "BLANCO", "CAVA":
	default:
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Tipo de vino no válido",
		})
		return
	}

	// Legacy chooses a unique numeric identifier based on current epoch seconds.
	uniqueNum := int(time.Now().Unix())
	active := 1

	if _, err := s.db.ExecContext(
		r.Context(),
		"INSERT INTO VINOS (restaurant_id, num, tipo, nombre, precio, descripcion, bodega, denominacion_origen, graduacion, anyo, active) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		restaurantID, uniqueNum, tipo, nombre, precio, descripcion, bodega, denominacionOrigen, graduacion, anyo, active,
	); err != nil {
		// If we collide (same second), fall back to letting MySQL assign the AUTO_INCREMENT value.
		res, err2 := s.db.ExecContext(
			r.Context(),
			"INSERT INTO VINOS (restaurant_id, tipo, nombre, precio, descripcion, bodega, denominacion_origen, graduacion, anyo, active) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
			restaurantID, tipo, nombre, precio, descripcion, bodega, denominacionOrigen, graduacion, anyo, active,
		)
		if err2 != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error al añadir el vino")
			return
		}
		if id, err3 := res.LastInsertId(); err3 == nil && id > 0 {
			uniqueNum = int(id)
		}
	}

	img, hasImg, imgErr := extractWineImage(r)
	if imgErr != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": true,
			"wineId":  uniqueNum,
			"warning": "Vino añadido, pero la imagen no se pudo procesar",
		})
		return
	}

	if hasImg {
		objectPath, err := s.UploadWineImage(r.Context(), tipo, uniqueNum, img)
		if err != nil {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": true,
				"wineId":  uniqueNum,
				"warning": "Vino añadido, pero la imagen no se pudo subir",
			})
			return
		}

		if _, err := s.db.ExecContext(r.Context(), "UPDATE VINOS SET foto_path = ?, foto = NULL WHERE restaurant_id = ? AND num = ?", objectPath, restaurantID, uniqueNum); err != nil {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": true,
				"wineId":  uniqueNum,
				"warning": "Vino añadido pero sin imagen",
			})
			return
		}
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"wineId":  uniqueNum,
	})
}

func parseFloatLoose(s string) (float64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, errors.New("empty")
	}
	s = strings.ReplaceAll(s, ",", ".")
	return strconv.ParseFloat(s, 64)
}

func extractWineImage(r *http.Request) ([]byte, bool, error) {
	base64Str := strings.TrimSpace(r.Form.Get("imageBase64"))
	if base64Str != "" {
		img, err := decodeBase64Image(base64Str)
		if err != nil {
			return nil, false, err
		}
		if len(img) == 0 {
			return nil, false, errors.New("empty image")
		}
		return img, true, nil
	}

	// Fallback to file upload.
	f, _, err := r.FormFile("image")
	if err != nil {
		return nil, false, nil
	}
	defer f.Close()

	img, err := io.ReadAll(io.LimitReader(f, 20<<20))
	if err != nil {
		return nil, false, err
	}
	if len(img) == 0 {
		return nil, false, nil
	}
	return img, true, nil
}

func decodeBase64Image(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, errors.New("empty")
	}
	if strings.HasPrefix(s, "data:") {
		if comma := strings.IndexByte(s, ','); comma >= 0 {
			s = s[comma+1:]
		}
	}
	s = strings.TrimSpace(s)

	// Most clients send standard padded base64. Accept a few variants just in case.
	if out, err := base64.StdEncoding.DecodeString(s); err == nil {
		return out, nil
	}
	if out, err := base64.RawStdEncoding.DecodeString(s); err == nil {
		return out, nil
	}
	if out, err := base64.URLEncoding.DecodeString(s); err == nil {
		return out, nil
	}
	return base64.RawURLEncoding.DecodeString(s)
}
