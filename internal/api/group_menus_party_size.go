package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"preactvillacarmen/internal/httpx"
)

func isPartySizeClosedGroupMenuType(raw string) bool {
	return normalizeV2MenuType(raw) == "closed_group"
}

// hasPrincipalesItems checks if the principales object has a non-empty items array.
func hasPrincipalesItems(principales any) bool {
	if principales == nil {
		return false
	}
	// Try to extract items from map[string]any
	if m, ok := principales.(map[string]any); ok {
		items, exists := m["items"]
		if !exists {
			return false
		}
		if arr, ok := items.([]any); ok {
			return len(arr) > 0
		}
		return false
	}
	return false
}

// handleGetValidMenusForPartySize lists the group menus (menu de grupo) valid for
// a party size, both for GET /api/reservations/group-menus and the legacy alias
// getValidMenusForPartySize.php.
//
// Closed_group menus come from their own menus.principales JSON; special menus
// are offered here when flagged as a group menu and holding principals, with
// every special_menu_section_principales section flattened into the same
// {titulo_principales, items} shape the group wizard already reads.
// Coordination id: special_menu_group_booking_v1
func (s *Server) handleGetValidMenusForPartySize(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"message": "Unknown restaurant",
		})
		return
	}

	if r.Method != http.MethodGet {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Invalid request method. Only GET is allowed.",
		})
		return
	}

	rawPartySize := strings.TrimSpace(r.URL.Query().Get("party_size"))
	if rawPartySize == "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "party_size parameter is required",
		})
		return
	}
	partySize, err := strconv.Atoi(rawPartySize)
	if err != nil || partySize < 1 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "party_size must be a positive integer",
		})
		return
	}

	rows, err := s.db.QueryContext(r.Context(), `
		SELECT id, menu_title, price, included_coffee,
		       COALESCE(NULLIF(TRIM(menu_type), ''), 'closed_conventional') AS menu_type,
		       menu_subtitle,
		       entrantes, principales, postre, beverage, comments,
		       min_party_size, main_dishes_limit, main_dishes_limit_number, created_at,
		       COALESCE(special_group_menu_enabled, 0), COALESCE(special_principales_required, 0)
		FROM menus
		WHERE restaurant_id = ?
		  AND active = 1
		  AND min_party_size <= ?
		  AND (
		        LOWER(COALESCE(NULLIF(TRIM(menu_type), ''), 'closed_conventional')) = 'closed_group'
		        OR COALESCE(special_group_menu_enabled, 0) = 1
		      )
		ORDER BY min_party_size ASC, price ASC
	`, restaurantID, partySize)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Server error: " + err.Error(),
		})
		return
	}
	defer rows.Close()

	type PrincipalesFallback struct {
		Titulo string `json:"titulo_principales"`
		Items  []any  `json:"items"`
	}
	type BeverageFallback struct {
		Type           string   `json:"type"`
		PricePerPerson *float64 `json:"price_per_person"`
	}

	type menuOut struct {
		ID                    int      `json:"id"`
		MenuTitle             string   `json:"menu_title"`
		MenuTitleEnglish      string   `json:"menu_title_english,omitempty"`
		Price                 float64  `json:"price"`
		MinPartySize          int      `json:"min_party_size"`
		MainDishesLimit       bool     `json:"main_dishes_limit"`
		MainDishesLimitNumber int      `json:"main_dishes_limit_number"`
		IncludedCoffee        bool     `json:"included_coffee"`
		MenuSubtitle          any      `json:"menu_subtitle"`
		Entrantes             any      `json:"entrantes"`
		EntrantesEnglish      []string `json:"entrantes_english,omitempty"`
		Principales           any      `json:"principales"`
		PrincipalesEnglish    any      `json:"principales_english,omitempty"`
		Postre                any      `json:"postre"`
		Beverage              any      `json:"beverage"`
		Comments              any      `json:"comments"`
		CreatedAt             string   `json:"created_at"`
		// Coordination id: special_menu_group_booking_v1 - the front uses this
		// flag to tell a special menu from a closed_group one (both keep the
		// same "principales" shape) and to know whether principales are forced.
		SpecialGroupMenuEnabled    bool `json:"special_group_menu_enabled"`
		SpecialPrincipalesRequired bool `json:"special_principales_required"`
	}

	// Coordination id: special_menu_group_booking_v1 - resolve the special
	// menus' principals before walking the cursor: nested queries are only safe
	// once rows.Close() ran, matching loadSpecialMenuSectionsPayload.
	if err := rows.Close(); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Server error: " + err.Error(),
		})
		return
	}
	specialPrincipales, err := s.loadSpecialGroupMenuPrincipales(r.Context(), restaurantID)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Server error: " + err.Error(),
		})
		return
	}

	var menus []menuOut
	for rows.Next() {
		var (
			id                    int
			menuTitle             string
			price                 float64
			includedCoffeeInt     int
			menuType              string
			menuSubtitleRaw       sql.NullString
			entrantesRaw          sql.NullString
			principalesRaw        sql.NullString
			postreRaw             sql.NullString
			beverageRaw           sql.NullString
			commentsRaw           sql.NullString
			minPartySize          int
			mainDishesLimitInt    int
			mainDishesLimitNumber int
			createdAt             time.Time
			specialGroupEnabled   int
			specialPrincipalesReq int
		)
		if err := rows.Scan(
			&id,
			&menuTitle,
			&price,
			&includedCoffeeInt,
			&menuType,
			&menuSubtitleRaw,
			&entrantesRaw,
			&principalesRaw,
			&postreRaw,
			&beverageRaw,
			&commentsRaw,
			&minPartySize,
			&mainDishesLimitInt,
			&mainDishesLimitNumber,
			&createdAt,
			&specialGroupEnabled,
			&specialPrincipalesReq,
		); err != nil {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"message": "Server error: " + err.Error(),
			})
			return
		}
		isSpecialMenu := normalizeV2MenuType(menuType) == "special"
		if !isPartySizeClosedGroupMenuType(menuType) && !isSpecialMenu {
			continue
		}
		// Coordination id: special_menu_group_booking_v1 - a special menu is
		// offered here only when it is flagged as a group menu and has
		// principales (toggle on + non-empty list).
		if isSpecialMenu && specialPrincipales[int64(id)] == nil {
			continue
		}

		decodeOr := func(raw sql.NullString, fallback any) any {
			if !raw.Valid || strings.TrimSpace(raw.String) == "" {
				return fallback
			}
			var v any
			if err := json.Unmarshal([]byte(raw.String), &v); err != nil {
				return fallback
			}
			if v == nil {
				return fallback
			}
			return v
		}

		principalesFallback := decodeOr(principalesRaw, PrincipalesFallback{
			Titulo: "Principal a elegir",
			Items:  []any{},
		})

		if isSpecialMenu {
			// Coordination id: special_menu_group_booking_v1 - reuse the v1
			// readers: every section's principals are flattened into the same
			// {titulo_principales, items} shape the group wizard consumes.
			principalesFallback = specialPrincipales[int64(id)]
			// Special sections carry no per-section limit, so the menu row's own
			// main_dishes_limit is already the real limit (0/false when unset).
		}

		// Skip menus without principales items
		if !hasPrincipalesItems(principalesFallback) {
			continue
		}

		beverageFallback := decodeOr(beverageRaw, BeverageFallback{
			Type:           "no_incluida",
			PricePerPerson: nil,
		})

		menu := menuOut{
			ID:                         id,
			MenuTitle:                  menuTitle,
			Price:                      price,
			MinPartySize:               minPartySize,
			MainDishesLimit:            mainDishesLimitInt != 0,
			MainDishesLimitNumber:      mainDishesLimitNumber,
			IncludedCoffee:             includedCoffeeInt != 0,
			MenuSubtitle:               decodeOr(menuSubtitleRaw, []any{}),
			Entrantes:                  decodeOr(entrantesRaw, []any{}),
			Principales:                principalesFallback,
			Postre:                     decodeOr(postreRaw, []any{}),
			Beverage:                   beverageFallback,
			Comments:                   decodeOr(commentsRaw, []any{}),
			CreatedAt:                  createdAt.Format("2006-01-02 15:04:05"),
			SpecialGroupMenuEnabled:    specialGroupEnabled != 0,
			SpecialPrincipalesRequired: specialPrincipalesReq != 0,
		}
		menus = append(menus, menu)
	}

	ids := make([]int64, len(menus))
	for i := range menus {
		ids[i] = int64(menus[i].ID)
	}
	if all, err := s.loadTranslations(r.Context(), restaurantID, entityMenus, ids, translationLang); err == nil {
		for i := range menus {
			tr := all[int64(menus[i].ID)]
			menus[i].MenuTitleEnglish = translationOr(tr, "menu_title")
			menus[i].EntrantesEnglish = buildEnglishArray(tr, "entrantes", len(anySliceToStringList(menus[i].Entrantes)))
			if principales, ok := menus[i].Principales.(map[string]any); ok {
				items := anySliceToStringList(principales["items"])
				menus[i].PrincipalesEnglish = map[string]any{
					"titulo_principales": translationOr(tr, "principales_title"),
					"items":              buildEnglishArray(tr, "principales", len(items)),
				}
			}
		}
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":       true,
		"hasValidMenus": len(menus) > 0,
		"count":         len(menus),
		"menus":         menus,
	})
}
