package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"preactvillacarmen/internal/httpx"
)

// =============================================================================
// Special menu call-to-action button.
//
// The backoffice stores the raw config (menus.special_cta); every reader gets
// it back together with a resolved absolute href built from the restaurant's
// own website (restaurant_info.website), so the same button works for every
// tenant without the frontends knowing about domains.
// Coordination id: special_menu_cta_v1
// =============================================================================

const (
	specialMenuCtaActionMenu     = "menu"
	specialMenuCtaActionWhatsApp = "whatsapp"
	specialMenuCtaActionReservas = "reservas"
	specialMenuCtaDefaultLabel   = "RESERVAR"
)

// specialMenuCtaConfig is the persisted, editor-facing shape.
type specialMenuCtaConfig struct {
	Enabled         bool   `json:"enabled"`
	Label           string `json:"label"`
	Action          string `json:"action"`
	MenuID          int64  `json:"menu_id"`
	WhatsAppPhone   string `json:"whatsapp_phone"`
	WhatsAppMessage string `json:"whatsapp_message"`
	SpecialDateID   int64  `json:"special_date_id"`
}

// specialMenuCta is what the payloads expose: config + resolved link.
type specialMenuCta struct {
	specialMenuCtaConfig
	Href        string `json:"href"`
	OpensNewTab bool   `json:"opens_new_tab"`
	WebsiteBase string `json:"website_base_url"`
	TargetDate  string `json:"target_date,omitempty"`
	// DefaultWhatsAppPhone prefills the editor from /app/config?content=contacto.
	DefaultWhatsAppPhone string `json:"default_whatsapp_phone"`
}

func normalizeSpecialMenuCtaConfig(c specialMenuCtaConfig) specialMenuCtaConfig {
	c.Label = strings.TrimSpace(c.Label)
	if c.Label == "" {
		c.Label = specialMenuCtaDefaultLabel
	}
	switch c.Action {
	case specialMenuCtaActionMenu, specialMenuCtaActionWhatsApp, specialMenuCtaActionReservas:
	default:
		c.Action = specialMenuCtaActionReservas
	}
	c.WhatsAppPhone = digitsOnly(c.WhatsAppPhone)
	c.WhatsAppMessage = strings.TrimSpace(c.WhatsAppMessage)
	return c
}

// loadSpecialMenuCta reads the stored config and resolves its href. It never
// fails the caller: a broken row just means "no button".
func (s *Server) loadSpecialMenuCta(ctx context.Context, restaurantID int, menuID int64) *specialMenuCta {
	var raw sql.NullString
	if err := s.db.QueryRowContext(ctx,
		`SELECT special_cta FROM menus WHERE id = ? AND restaurant_id = ?`, menuID, restaurantID).Scan(&raw); err != nil {
		if err != sql.ErrNoRows {
			log.Printf("[special_menu_cta_v1] load menu=%d err=%v", menuID, err)
		}
		return nil
	}
	cfg := specialMenuCtaConfig{}
	if raw.Valid && strings.TrimSpace(raw.String) != "" {
		if err := json.Unmarshal([]byte(raw.String), &cfg); err != nil {
			log.Printf("[special_menu_cta_v1] decode menu=%d err=%v", menuID, err)
		}
	}
	cta := s.resolveSpecialMenuCta(ctx, restaurantID, normalizeSpecialMenuCtaConfig(cfg))
	return &cta
}

// loadPublicSpecialMenuCta hides the button from the public site unless it is
// switched on, so disabled configs never leak into preactvillacarmen.
func (s *Server) loadPublicSpecialMenuCta(ctx context.Context, restaurantID int, menuID int64) *specialMenuCta {
	cta := s.loadSpecialMenuCta(ctx, restaurantID, menuID)
	if cta == nil || !cta.Enabled || cta.Href == "" {
		return nil
	}
	cta.DefaultWhatsAppPhone = "" // editor-only prefill
	return cta
}

// resolveSpecialMenuCta turns a config into an absolute link on the tenant's
// website (menu page / reservas page, optionally with ?date=) or a wa.me link.
func (s *Server) resolveSpecialMenuCta(ctx context.Context, restaurantID int, cfg specialMenuCtaConfig) specialMenuCta {
	out := specialMenuCta{specialMenuCtaConfig: cfg}
	branding, err := s.loadRestaurantBranding(ctx, restaurantID)
	if err != nil {
		log.Printf("[special_menu_cta_v1] branding restaurant=%d err=%v", restaurantID, err)
	}
	out.WebsiteBase = strings.TrimRight(branding.Website, "/")
	out.DefaultWhatsAppPhone = digitsOnly(branding.ManagementPhone)
	if out.DefaultWhatsAppPhone == "" {
		out.DefaultWhatsAppPhone = digitsOnly(branding.Phone)
	}

	switch cfg.Action {
	case specialMenuCtaActionWhatsApp:
		phone := cfg.WhatsAppPhone
		if phone == "" {
			phone = out.DefaultWhatsAppPhone
		}
		if phone != "" {
			out.Href = "https://wa.me/" + phone
			if cfg.WhatsAppMessage != "" {
				out.Href += "?text=" + strings.ReplaceAll(url.QueryEscape(cfg.WhatsAppMessage), "+", "%20")
			}
			out.OpensNewTab = true
		}
	case specialMenuCtaActionMenu:
		var title string
		if cfg.MenuID > 0 && s.db.QueryRowContext(ctx,
			`SELECT menu_title FROM menus WHERE id = ? AND restaurant_id = ? AND active = 1`,
			cfg.MenuID, restaurantID).Scan(&title) == nil {
			out.Href = out.WebsiteBase + "/menu/" + strconv.FormatInt(cfg.MenuID, 10) + "/" + buildPublicMenuSlug(title, cfg.MenuID)
		}
	default:
		out.Href = out.WebsiteBase + "/reservas"
		if cfg.SpecialDateID > 0 {
			var date string
			if s.db.QueryRowContext(ctx,
				`SELECT DATE_FORMAT(date, '%Y-%m-%d') FROM special_dates WHERE id = ? AND restaurant_id = ?`,
				cfg.SpecialDateID, restaurantID).Scan(&date) == nil {
				out.TargetDate = date
				out.Href += "?date=" + url.QueryEscape(date)
			}
		}
	}
	return out
}

// handleBOGroupMenusV2PutSpecialMenuCta stores the whole CTA config and
// returns it resolved, so the editor preview shows the real link at once.
func (s *Server) handleBOGroupMenusV2PutSpecialMenuCta(w http.ResponseWriter, r *http.Request) {
	echoCorrelationID(w, r)

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
	owns, err := s.ensureBOMenuV2Belongs(a.ActiveRestaurantID, menuID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error checking menu")
		return
	}
	if !owns {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Menu not found"})
		return
	}

	var req specialMenuCtaConfig
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid JSON"})
		return
	}
	cfg := normalizeSpecialMenuCtaConfig(req)
	encoded, _ := json.Marshal(cfg)
	if _, err := s.db.ExecContext(r.Context(),
		`UPDATE menus SET special_cta = ? WHERE id = ? AND restaurant_id = ?`,
		string(encoded), menuID, a.ActiveRestaurantID); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error guardando boton reservar")
		return
	}

	cta := s.resolveSpecialMenuCta(r.Context(), a.ActiveRestaurantID, cfg)
	logCheckpoint(r, "special_menu_cta_persisted",
		"menu_id", strconv.FormatInt(menuID, 10),
		"enabled", strconv.FormatBool(cfg.Enabled),
		"action", cfg.Action)

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "special_cta": cta})
}
