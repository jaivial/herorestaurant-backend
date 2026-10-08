package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"preactvillacarmen/internal/httpx"
)

// =============================================================================
// Special menus bookable as a group menu (menu de grupo).
//
// A special menu (menus.menu_type = 6) is normally offered per image
// section, but the group wizard only reads GET /api/reservations/group-menus.
// The two columns added by migration 164_special_menu_group_booking.sql let the
// backoffice publish a special menu there without touching the closed_group
// catalogue:
//
//   special_group_menu_enabled -> the special menu is offered as a group menu
//   special_principales_required -> guests must pick the principales
//
// Shared rule: a special menu is bookable as a group menu as soon as it is
// flagged as one. Principales are optional -- a menu can be reserved without
// the guests picking a main course. Only special_principales_required depends
// on the principals, and it is only satisfiable when the menu offers some
// (toggle on + at least one dish, the same "on with no dishes == off" rule as
// special_menu_principales_v1).
// Coordination id: special_menu_group_booking_v1
// =============================================================================

// specialMenuGroupBookingFlags holds both per-menu toggles of the chain.
type specialMenuGroupBookingFlags struct {
	// GroupMenuEnabled mirrors menus.special_group_menu_enabled.
	GroupMenuEnabled bool
	// PrincipalesRequired mirrors menus.special_principales_required.
	PrincipalesRequired bool
	// PrincipalesEnabled mirrors menus.special_principales_enabled (v1 toggle).
	PrincipalesEnabled bool
	// SpecialType is true when menus.menu_type resolves to MenuTypeSpecial.
	SpecialType bool
	// HasPrincipales is true when the menu offers at least one principal dish.
	HasPrincipales bool
}

// BookableAsGroupMenu is the single rule used by the public group-menus listing,
// the backoffice payload and the booking validation. Being a group menu does
// not require the menu to offer principals: the guest books it as it is and
// only picks a main course when the menu lists some.
func (f specialMenuGroupBookingFlags) BookableAsGroupMenu() bool {
	return f.SpecialType && f.GroupMenuEnabled
}

// OffersPrincipales reports whether the menu actually offers a main course to
// choose from (v1 toggle on + at least one dish). Only "Plato principal
// obligatorio" needs it: it cannot be satisfied without dishes to pick.
func (f specialMenuGroupBookingFlags) OffersPrincipales() bool {
	return f.PrincipalesEnabled && f.HasPrincipales
}

// loadSpecialMenuGroupBookingFlags reads both flags of one menu row in a single
// query and applies the shared rule on top of the existing v1 readers, so the
// SQL of special_menu_section_principales is never duplicated.
func (s *Server) loadSpecialMenuGroupBookingFlags(ctx context.Context, restaurantID int, menuID int64) specialMenuGroupBookingFlags {
	var (
		groupEnabled  int
		required      int
		principalesOn int
		menuType      int
	)
	flags := specialMenuGroupBookingFlags{}
	if err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(special_group_menu_enabled, 0),
		       COALESCE(special_principales_required, 0),
		       COALESCE(special_principales_enabled, 0),
		       COALESCE(menu_type, 1)
		FROM menus WHERE id = ? AND restaurant_id = ? LIMIT 1
	`, menuID, restaurantID).Scan(&groupEnabled, &required, &principalesOn, &menuType); err != nil {
		if err != sql.ErrNoRows {
			log.Printf("[special_menu_group_booking_v1] flags menu=%d err=%v", menuID, err)
		}
		return flags
	}
	flags.GroupMenuEnabled = groupEnabled != 0
	flags.PrincipalesRequired = required != 0
	flags.PrincipalesEnabled = principalesOn != 0
	flags.SpecialType = IsSpecialMenuType(menuType)
	for _, list := range s.loadPublicSpecialMenuPrincipales(ctx, restaurantID, menuID) {
		if len(list) > 0 {
			flags.HasPrincipales = true
			break
		}
	}
	return flags
}

// loadSpecialGroupMenuPrincipales resolves, in one batch, every special menu of
// the restaurant that may be booked as a group menu, keyed by menu id, with the
// flattened {titulo_principales, items} the group wizard reads. Menus failing
// the shared rule are absent from the result, so the caller simply skips them.
// Coordination id: special_menu_group_booking_v1
func (s *Server) loadSpecialGroupMenuPrincipales(ctx context.Context, restaurantID int) (map[int64]map[string]any, error) {
	out := map[int64]map[string]any{}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id FROM menus
		WHERE restaurant_id = ?
		  AND active = 1
		  AND COALESCE(menu_type, 1) = 6
		  AND COALESCE(special_group_menu_enabled, 0) = 1
	`, restaurantID)
	if err != nil {
		return out, err
	}
	var menuIDs []int64
	for rows.Next() {
		var menuID int64
		if err := rows.Scan(&menuID); err != nil {
			continue
		}
		menuIDs = append(menuIDs, menuID)
	}
	rows.Close()

	for _, menuID := range menuIDs {
		// Same rule as the backoffice payload and the booking validation.
		flags := s.loadSpecialMenuGroupBookingFlags(ctx, restaurantID, menuID)
		if !flags.BookableAsGroupMenu() {
			continue
		}
		// A group menu without principals is still offered: the wizard shows
		// the block with no options and the guest simply does not pick one.
		out[menuID] = specialPrincipalesForGroupMenu(
			s.loadPublicSpecialMenuPrincipales(ctx, restaurantID, menuID))
	}
	return out, nil
}

// specialPrincipalesForGroupMenu flattens every section's principales of a
// special menu into the {titulo_principales, items} shape the group wizard
// already reads, so no frontend change is needed to book a special menu:
// items is a flat string[] of dish names, exactly like a closed_group menu.
// Coordination id: special_menu_group_booking_v1
func specialPrincipalesForGroupMenu(bySection map[int64][]specialMenuPrincipal) map[string]any {
	// Sections are keyed by an unordered map, so flatten them ordered by id.
	sectionIDs := make([]int64, 0, len(bySection))
	for sectionID := range bySection {
		sectionIDs = append(sectionIDs, sectionID)
	}
	sort.Slice(sectionIDs, func(i, j int) bool { return sectionIDs[i] < sectionIDs[j] })

	items := []any{}
	seen := map[string]bool{}
	for _, sectionID := range sectionIDs {
		for _, p := range bySection[sectionID] {
			title := strings.TrimSpace(p.Title)
			if title == "" || seen[title] {
				continue
			}
			seen[title] = true
			items = append(items, title)
		}
	}
	return map[string]any{
		"titulo_principales": "Principal a elegir",
		"items":              items,
	}
}

// handleBOSpecialMenuGroupBooking persists both group-booking toggles.
// Requiring principals implies offering the menu as a group menu: there is no
// point in demanding the principals of a menu nobody can reserve.
func (s *Server) handleBOSpecialMenuGroupBooking(w http.ResponseWriter, r *http.Request) {
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
	var req struct {
		GroupMenuEnabled    bool `json:"group_menu_enabled"`
		PrincipalesRequired bool `json:"principales_required"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid JSON"})
		return
	}
	groupMenuEnabled := req.GroupMenuEnabled || req.PrincipalesRequired
	res, err := s.db.ExecContext(r.Context(),
		`UPDATE menus SET special_group_menu_enabled = ?, special_principales_required = ? WHERE id = ? AND restaurant_id = ?`,
		boolToTinyint(groupMenuEnabled), boolToTinyint(req.PrincipalesRequired), menuID, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error guardando el menu de grupo")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		if owns, _ := s.ensureBOMenuV2Belongs(a.ActiveRestaurantID, menuID); !owns {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Menu not found"})
			return
		}
	}
	logCheckpoint(r, "special_menu_group_booking_saved",
		"menu_id", strconv.FormatInt(menuID, 10),
		"group_menu_enabled", strconv.FormatBool(groupMenuEnabled),
		"principales_required", strconv.FormatBool(req.PrincipalesRequired))
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":              true,
		"group_menu_enabled":   groupMenuEnabled,
		"principales_required": req.PrincipalesRequired,
	})
}
