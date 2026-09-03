package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"preactvillacarmen/internal/httpx"
)

var defaultMenuBeverageSlugs = []string{"agua", "refrescos", "cerveza-de-barril", "vino"}

type beverageOption struct {
	ID       int64  `json:"id"`
	Slug     string `json:"slug"`
	Name     string `json:"name"`
	Custom   bool   `json:"is_custom"`
	Selected bool   `json:"selected"`
}

func defaultBeverageSlug(slug string) bool {
	for _, candidate := range defaultMenuBeverageSlugs {
		if slug == candidate {
			return true
		}
	}
	return false
}

func (s *Server) loadMenuBeverageOptions(restaurantID int, menuID int64) ([]beverageOption, error) {
	rows, err := s.db.Query(`
		SELECT o.id, o.slug, o.name, o.is_custom,
		       COALESCE(m.selected, CASE WHEN o.slug IN ('agua', 'refrescos', 'cerveza-de-barril', 'vino') THEN 1 ELSE 0 END)
		FROM restaurant_beverage_options o
		LEFT JOIN menu_beverage_options m
		  ON m.restaurant_id = o.restaurant_id
		 AND m.menu_id = ?
		 AND m.beverage_option_id = o.id
		WHERE o.restaurant_id = ? AND o.active = 1
		ORDER BY o.is_custom ASC, o.id ASC
	`, menuID, restaurantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	options := make([]beverageOption, 0, 12)
	for rows.Next() {
		var option beverageOption
		var custom, selected int
		if err := rows.Scan(&option.ID, &option.Slug, &option.Name, &custom, &selected); err != nil {
			return nil, err
		}
		option.Custom = custom != 0
		option.Selected = selected != 0
		options = append(options, option)
	}
	return options, rows.Err()
}

func (s *Server) selectedMenuBeverageNames(restaurantID int, menuID int64) ([]string, error) {
	options, err := s.loadMenuBeverageOptions(restaurantID, menuID)
	if err != nil {
		return nil, err
	}
	selected := make([]string, 0, len(options))
	for _, option := range options {
		if option.Selected {
			selected = append(selected, option.Name)
		}
	}
	return selected, nil
}

func beverageOptionSlug(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	var b strings.Builder
	lastDash := false
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case r == 'á' || r == 'à' || r == 'ä' || r == 'â':
			b.WriteRune('a')
			lastDash = false
		case r == 'é' || r == 'è' || r == 'ë' || r == 'ê':
			b.WriteRune('e')
			lastDash = false
		case r == 'í' || r == 'ì' || r == 'ï' || r == 'î':
			b.WriteRune('i')
			lastDash = false
		case r == 'ó' || r == 'ò' || r == 'ö' || r == 'ô':
			b.WriteRune('o')
			lastDash = false
		case r == 'ú' || r == 'ù' || r == 'ü' || r == 'û':
			b.WriteRune('u')
			lastDash = false
		case r == 'ñ':
			b.WriteString("n")
			lastDash = false
		default:
			if b.Len() > 0 && !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

func (s *Server) upsertMenuBeverageSelection(restaurantID int, menuID, optionID int64, selected bool) error {
	_, err := s.db.Exec(`
		INSERT INTO menu_beverage_options (menu_id, restaurant_id, beverage_option_id, selected)
		VALUES (?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE selected = VALUES(selected), updated_at = CURRENT_TIMESTAMP
	`, menuID, restaurantID, optionID, boolToTinyint(selected))
	return err
}

func (s *Server) createRestaurantBeverageOption(restaurantID int, name string) (beverageOption, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 255 {
		return beverageOption{}, sql.ErrNoRows
	}
	slug := beverageOptionSlug(name)
	if slug == "" {
		return beverageOption{}, sql.ErrNoRows
	}
	_, err := s.db.Exec(`
		INSERT INTO restaurant_beverage_options (restaurant_id, slug, name, is_custom, active)
		VALUES (?, ?, ?, 1, 1)
		ON DUPLICATE KEY UPDATE name = VALUES(name), active = 1
	`, restaurantID, slug, name)
	if err != nil {
		return beverageOption{}, err
	}
	var option beverageOption
	var custom, selected int
	err = s.db.QueryRow(`
		SELECT id, slug, name, is_custom, 0
		FROM restaurant_beverage_options
		WHERE restaurant_id = ? AND slug = ? LIMIT 1
	`, restaurantID, slug).Scan(&option.ID, &option.Slug, &option.Name, &custom, &selected)
	option.Custom = custom != 0
	option.Selected = selected != 0
	return option, err
}

func (s *Server) deleteRestaurantBeverageOption(restaurantID int, optionID int64) error {
	_, err := s.db.Exec(`
		UPDATE restaurant_beverage_options
		SET active = 0
		WHERE id = ? AND restaurant_id = ? AND is_custom = 1
	`, optionID, restaurantID)
	return err
}

func (s *Server) handleBOBeverageOptionsList(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	menuID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || menuID <= 0 {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid menu id"})
		return
	}
	options, err := s.loadMenuBeverageOptions(a.ActiveRestaurantID, menuID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error cargando bebidas")
		return
	}
	echoCorrelationID(w, r)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "options": options})
}

func (s *Server) handleBOBeverageOptionsWSMessage(r *http.Request, restaurantID int, menuID int64, client *boGroupMenuV2AIClient, raw []byte) {
	var msg struct {
		Type       string `json:"type"`
		MenuID     int64  `json:"menu_id"`
		OptionID   int64  `json:"option_id"`
		Selected   *bool  `json:"selected"`
		Name       string `json:"name"`
		Correlation string `json:"correlation_id"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil {
		return
	}
	if msg.MenuID > 0 && msg.MenuID != menuID {
		return
	}
	cid := strings.TrimSpace(msg.Correlation)
	if cid == "" {
		cid = strings.TrimSpace(r.URL.Query().Get("correlationId"))
	}
	if cid != "" {
		// WebSocket messages carry the correlation id in their envelope. Clone the
		// request so the existing checkpoint helper can trace the message too.
		r = r.Clone(r.Context())
		r.Header.Set("x-correlation-id", cid)
	}
	logCheckpoint(r, "menu_beverage_ws_message_received", "menu_id", strconv.FormatInt(menuID, 10), "type", msg.Type)

	var response map[string]any
	switch strings.ToLower(strings.TrimSpace(msg.Type)) {
	case "beverage_list", "beverage_refresh":
		options, err := s.loadMenuBeverageOptions(restaurantID, menuID)
		if err != nil {
			response = map[string]any{"type": "beverage_error", "message": "Error cargando bebidas"}
		} else {
			response = map[string]any{"type": "beverage_options", "menu_id": menuID, "options": options}
		}
	case "beverage_set":
		if msg.OptionID <= 0 || msg.Selected == nil || !s.beverageOptionBelongs(restaurantID, msg.OptionID) {
			response = map[string]any{"type": "beverage_error", "message": "Opcion de bebida no valida"}
			break
		}
		err := s.upsertMenuBeverageSelection(restaurantID, menuID, msg.OptionID, *msg.Selected)
		if err != nil {
			response = map[string]any{"type": "beverage_error", "message": "No se pudo guardar la bebida"}
			break
		}
		logCheckpoint(r, "menu_beverage_persisted", "menu_id", strconv.FormatInt(menuID, 10), "option_id", strconv.FormatInt(msg.OptionID, 10))
		options, _ := s.loadMenuBeverageOptions(restaurantID, menuID)
		response = map[string]any{"type": "beverage_options", "menu_id": menuID, "options": options}
	case "beverage_create":
		option, err := s.createRestaurantBeverageOption(restaurantID, msg.Name)
		if err != nil {
			response = map[string]any{"type": "beverage_error", "message": "No se pudo crear la bebida"}
			break
		}
		logCheckpoint(r, "menu_beverage_custom_created", "menu_id", strconv.FormatInt(menuID, 10), "option_id", strconv.FormatInt(option.ID, 10))
		options, _ := s.loadMenuBeverageOptions(restaurantID, menuID)
		response = map[string]any{"type": "beverage_options", "menu_id": menuID, "options": options}
	case "beverage_delete":
		if msg.OptionID <= 0 {
			response = map[string]any{"type": "beverage_error", "message": "Opcion de bebida no valida"}
			break
		}
		if err := s.deleteRestaurantBeverageOption(restaurantID, msg.OptionID); err != nil {
			response = map[string]any{"type": "beverage_error", "message": "No se pudo eliminar la bebida"}
			break
		}
		logCheckpoint(r, "menu_beverage_custom_deleted", "menu_id", strconv.FormatInt(menuID, 10), "option_id", strconv.FormatInt(msg.OptionID, 10))
		options, _ := s.loadMenuBeverageOptions(restaurantID, menuID)
		response = map[string]any{"type": "beverage_options", "menu_id": menuID, "options": options}
	default:
		return
	}
	if response == nil {
		return
	}
	response["correlation_id"] = cid
	response["observed_at"] = time.Now().UTC().Format(time.RFC3339)
	if s.groupMenusV2AIHub != nil {
		// broadcast includes the initiating socket and keeps all editor tabs in sync.
		s.groupMenusV2AIHub.broadcast(restaurantID, menuID, response)
	} else if client != nil {
		_ = client.writeJSON(response)
	}
}

func (s *Server) beverageOptionBelongs(restaurantID int, optionID int64) bool {
	var found int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM restaurant_beverage_options WHERE id = ? AND restaurant_id = ? AND active = 1`, optionID, restaurantID).Scan(&found); err != nil {
		return false
	}
	return found > 0
}

func (s *Server) menuBeverageOptionsPayload(restaurantID int, menuID int64) []map[string]any {
	names, err := s.selectedMenuBeverageNames(restaurantID, menuID)
	if err != nil {
		return []map[string]any{}
	}
	out := make([]map[string]any, 0, len(names))
	for _, name := range names {
		out = append(out, map[string]any{"name": name})
	}
	return out
}

