package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"
)

// =============================================================================
// Special-date menus backed by a special-type menu.
//
// A special menu is priced per image section (special_menu_sections.price), so
// on a special date it is booked per section: guests are counted per section,
// the price is the section price, the adelanto is set per section
// (special_date_menu_sections) and the principales come from the menu's
// configuracion tab (special_menu_section_principales).
// Coordination id: special_date_section_menus_v1
// =============================================================================

// specialDateMenuSection is one bookable section of a special-type menu.
type specialDateMenuSection struct {
	ID             int64                  `json:"id"`
	Title          string                 `json:"title"`
	Price          *float64               `json:"price"`
	AdelantoAmount *float64               `json:"adelanto_amount"`
	Position       int                    `json:"position"`
	Principales    []specialMenuPrincipal `json:"principales"`
}

// specialDateMenuIsSpecialType reports whether a catalogue menu is special.
func (s *Server) specialDateMenuIsSpecialType(ctx context.Context, restaurantID int, menuID int64) bool {
	var menuType string
	if err := s.db.QueryRowContext(ctx,
		`SELECT menu_type FROM menus WHERE id = ? AND restaurant_id = ?`, menuID, restaurantID).Scan(&menuType); err != nil {
		return false
	}
	return menuType == "special"
}

// loadSpecialDateMenuSections returns the sections of the special menu behind
// one special_date_menus row, with the per-section adelanto of that date.
// publicPrincipales applies the guest rule (toggle on + non-empty list).
func (s *Server) loadSpecialDateMenuSections(ctx context.Context, restaurantID int, specialDateMenuID, menuID int64, publicPrincipales bool) []specialDateMenuSection {
	out := []specialDateMenuSection{}
	rows, err := s.db.QueryContext(ctx, `
		SELECT sms.id, sms.title, sms.price, sdms.adelanto_amount, sms.position
		FROM special_menu_sections sms
		LEFT JOIN special_date_menu_sections sdms
		  ON sdms.section_id = sms.id AND sdms.special_date_menu_id = ? AND sdms.restaurant_id = sms.restaurant_id
		WHERE sms.restaurant_id = ? AND sms.menu_id = ?
		ORDER BY sms.position ASC, sms.id ASC
	`, specialDateMenuID, restaurantID, menuID)
	if err != nil {
		log.Printf("[special_date_section_menus_v1] load sdm=%d err=%v", specialDateMenuID, err)
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var (
			sec      specialDateMenuSection
			price    sql.NullFloat64
			adelanto sql.NullFloat64
		)
		if err := rows.Scan(&sec.ID, &sec.Title, &price, &adelanto, &sec.Position); err != nil {
			continue
		}
		if price.Valid {
			sec.Price = &price.Float64
		}
		if adelanto.Valid {
			sec.AdelantoAmount = &adelanto.Float64
		}
		out = append(out, sec)
	}
	rows.Close()

	var principales map[int64][]specialMenuPrincipal
	if publicPrincipales {
		principales = s.loadPublicSpecialMenuPrincipales(ctx, restaurantID, menuID)
	} else {
		principales = s.loadSpecialMenuPrincipales(ctx, restaurantID, menuID)
	}
	for i := range out {
		out[i].Principales = principales[out[i].ID]
		if out[i].Principales == nil {
			out[i].Principales = []specialMenuPrincipal{}
		}
	}
	return out
}

// specialDateSectionAdelanto is the adelanto the backoffice sets for one section.
type specialDateSectionAdelanto struct {
	SectionID      int64    `json:"section_id"`
	AdelantoAmount *float64 `json:"adelanto_amount"`
}

// saveSpecialDateMenuSections stores the per-section adelantos for one
// special_date_menus row, keeping only sections that belong to its menu.
func saveSpecialDateMenuSections(ctx context.Context, tx *sql.Tx, restaurantID int, specialDateMenuID, menuID int64, sections []specialDateSectionAdelanto) error {
	for _, sec := range sections {
		if sec.SectionID <= 0 {
			continue
		}
		var amount any
		if sec.AdelantoAmount != nil && *sec.AdelantoAmount >= 0 {
			amount = *sec.AdelantoAmount
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO special_date_menu_sections (restaurant_id, special_date_menu_id, section_id, adelanto_amount)
			SELECT ?, ?, sms.id, ?
			FROM special_menu_sections sms
			WHERE sms.id = ? AND sms.menu_id = ? AND sms.restaurant_id = ?
		`, restaurantID, specialDateMenuID, amount, sec.SectionID, menuID, restaurantID); err != nil {
			return err
		}
	}
	return nil
}

// specialMenuSectionSnapshotLines validates the per-section selection of a
// special-type menu and returns one snapshot line per booked section.
func (s *Server) specialMenuSectionSnapshotLines(
	ctx context.Context,
	restaurantID int,
	rec specialDateMenuRecord,
	m specialBookingMenuReq,
	acceptedMethods map[string]bool,
	unifiedAdelanto *float64,
) ([]specialBookingSnapshotMenu, int, error) {
	if len(m.Sections) == 0 {
		return nil, 0, fmt.Errorf("Indica los comensales de cada sección del menú %s", strings.TrimSpace(rec.MenuTitle))
	}
	menuID := rec.MenuID.Int64
	byID := map[int64]specialDateMenuSection{}
	for _, sec := range s.loadSpecialDateMenuSections(ctx, restaurantID, rec.ID, menuID, true) {
		byID[sec.ID] = sec
	}

	var method *string
	if m.AdelantoPaymentMethod != nil {
		if pm := strings.TrimSpace(*m.AdelantoPaymentMethod); pm != "" {
			if !acceptedMethods[pm] {
				return nil, 0, fmt.Errorf("Método de pago inválido en menú %d", m.SpecialDateMenuID)
			}
			method = &pm
		}
	}

	lines := make([]specialBookingSnapshotMenu, 0, len(m.Sections))
	total := 0
	seen := map[int64]bool{}
	for _, req := range m.Sections {
		sec, ok := byID[req.SectionID]
		if !ok || seen[req.SectionID] {
			return nil, 0, fmt.Errorf("Sección %d no disponible en el menú", req.SectionID)
		}
		seen[req.SectionID] = true
		if req.Count <= 0 {
			continue
		}
		items := []specialBookingSnapshotItem(nil)
		if len(req.Items) > 0 {
			offered := map[int64]string{}
			for _, p := range sec.Principales {
				offered[p.DishID] = p.Title
			}
			for _, id := range req.Items {
				name, ok := offered[id]
				if !ok {
					return nil, 0, errors.New("Algunos platos seleccionados no pertenecen a la sección")
				}
				items = append(items, specialBookingSnapshotItem{DishID: id, Name: strings.TrimSpace(name)})
			}
		}
		unitPrice, adelanto := 0.0, 0.0
		if sec.Price != nil {
			unitPrice = *sec.Price
		}
		// Same precedence as regular menus: a unified date amount wins.
		if unifiedAdelanto != nil {
			adelanto = *unifiedAdelanto
		} else if sec.AdelantoAmount != nil {
			adelanto = *sec.AdelantoAmount
		}
		label := strings.TrimSpace(rec.MenuTitle)
		if title := strings.TrimSpace(sec.Title); title != "" {
			label = strings.TrimSpace(label + " · " + title)
		}
		mid, sid := menuID, sec.ID
		lines = append(lines, specialBookingSnapshotMenu{
			SpecialDateMenuID:     rec.ID,
			MenuID:                &mid,
			SectionID:             &sid,
			Label:                 label,
			UnitPrice:             unitPrice,
			Count:                 req.Count,
			AdelantoPerUnit:       adelanto,
			AdelantoPaymentMethod: method,
			Items:                 items,
		})
		total += req.Count
	}
	if total == 0 {
		return nil, 0, fmt.Errorf("La cantidad del menú %d debe ser mayor que 0", m.SpecialDateMenuID)
	}
	return lines, total, nil
}
