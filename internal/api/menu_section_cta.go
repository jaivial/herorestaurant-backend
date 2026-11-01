package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"preactvillacarmen/internal/httpx"
)

// Coordination id: menu_section_cta_v1
//
// Optional call-to-action button under one menu section, edited in the menu
// settings and rendered by the public site. The target is a whitelisted
// public route key, never a free URL, so a section can only point at pages
// the site actually has. For now only "cafes".

// menuSectionCtaTargets maps each allowed target to its public path.
var menuSectionCtaTargets = map[string]string{
	"cafes": "/cafes",
}

const menuSectionCtaDefaultLabel = "Ver carta de cafés"

type menuSectionCta struct {
	Enabled bool   `json:"enabled"`
	Label   string `json:"label"`
	Target  string `json:"target"`
	// Href is resolved from Target on read; it is never stored.
	Href string `json:"href,omitempty"`
}

func normalizeMenuSectionCta(c menuSectionCta) (menuSectionCta, bool) {
	c.Target = strings.TrimSpace(strings.ToLower(c.Target))
	path, ok := menuSectionCtaTargets[c.Target]
	if !ok {
		return menuSectionCta{}, false
	}
	c.Label = strings.TrimSpace(c.Label)
	if c.Label == "" {
		c.Label = menuSectionCtaDefaultLabel
	}
	if len([]rune(c.Label)) > 80 {
		c.Label = string([]rune(c.Label)[:80])
	}
	c.Href = path
	return c, true
}

// decodeMenuSectionCta reads the stored JSON; anything invalid means "no button".
func decodeMenuSectionCta(raw sql.NullString) *menuSectionCta {
	if !raw.Valid || strings.TrimSpace(raw.String) == "" {
		return nil
	}
	var c menuSectionCta
	if json.Unmarshal([]byte(raw.String), &c) != nil {
		return nil
	}
	c, ok := normalizeMenuSectionCta(c)
	if !ok {
		return nil
	}
	return &c
}

// publicMenuSectionCta is what the public payload exposes: only enabled buttons.
func publicMenuSectionCta(raw sql.NullString) *menuSectionCta {
	c := decodeMenuSectionCta(raw)
	if c == nil || !c.Enabled {
		return nil
	}
	return c
}

// handleBOGroupMenusV2PutSectionCta: PUT /admin/group-menus-v2/{id}/sections/{sectionId}/cta
// Body {enabled, label, target}. target must be an allowed route ("cafes").
func (s *Server) handleBOGroupMenusV2PutSectionCta(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	menuID, err := parseChiPositiveInt64(r, "id")
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid menu id"})
		return
	}
	sectionID, err := parseChiPositiveInt64(r, "sectionId")
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid section id"})
		return
	}
	var req menuSectionCta
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid JSON body"})
		return
	}
	cta, ok := normalizeMenuSectionCta(req)
	if !ok {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Destino no permitido (disponible: cafes)"})
		return
	}
	stored, _ := json.Marshal(menuSectionCta{Enabled: cta.Enabled, Label: cta.Label, Target: cta.Target})
	res, err := s.db.ExecContext(r.Context(), `
		UPDATE group_menu_sections_v2 SET cta_json = ?
		WHERE id = ? AND menu_id = ? AND restaurant_id = ?
	`, string(stored), sectionID, menuID, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error guardando el botón de la sección")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		var exists int
		_ = s.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM group_menu_sections_v2 WHERE id = ? AND menu_id = ? AND restaurant_id = ?`, sectionID, menuID, a.ActiveRestaurantID).Scan(&exists)
		if exists == 0 {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Section not found"})
			return
		}
	}
	logCheckpoint(r, "menu_section_cta_saved", "menu_id", strconv.FormatInt(menuID, 10), "section_id", strconv.FormatInt(sectionID, 10), "target", cta.Target, "enabled", strconv.FormatBool(cta.Enabled))
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "section_id": sectionID, "cta": cta})
}
