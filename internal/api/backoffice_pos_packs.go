package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"preactvillacarmen/internal/httpx"
)

// POS packs ("menú del día", "combinado"): a fixed-price bundle that expands
// into its component lines when it is rung up. The schema has been in place
// (pos_packs, pos_pack_components, pos_ticket_lines.pack_id/parent_line_id)
// with nothing reading or writing it, so a waiter could only sell a menu by
// tapping every component separately, and the kitchen lost the "this is one
// menu" signal.
//
// Money rule, matching migration 078: the parent line carries the PACK price
// (not the sum of its parts) and the component lines are priced at zero. That
// way a discount or comp on the parent is the whole menu, and the receipt
// shows the menu as one item plus its contents, the way a guest expects.
//
// Components sharing a slot_group are alternatives: the operator picks exactly
// one per slot. A slot with a single default is applied automatically.
//
// Coordination id: pos_packs_v1

type posPackComponent struct {
	ProductID   int64   `json:"productId"`
	ProductName string  `json:"productName"`
	Quantity    float64 `json:"quantity"`
	// SlotGroup: alternatives share a name; the operator chooses one. Empty
	// means a fixed component.
	SlotGroup string `json:"slotGroup,omitempty"`
	IsDefault bool   `json:"isDefault"`
	SortOrder int    `json:"sortOrder"`
	// VATRate is resolved from the component product so a component line is
	// taxed like the dish it is, not like the menu.
	VATRate float64 `json:"vatRate"`
}

type posPack struct {
	ID              int64              `json:"id"`
	Name            string             `json:"name"`
	Description     string             `json:"description,omitempty"`
	PriceGrossCents int64              `json:"priceGrossCents"`
	VATRate         float64            `json:"vatRate"`
	IsActive        bool               `json:"isActive"`
	SortOrder       int                `json:"sortOrder"`
	Components      []posPackComponent `json:"components"`
	/** Slot names in display order, so the picker can label each choice. */
	Slots []string `json:"slots"`
}

func (p posPack) slotComponents(slot string) []posPackComponent {
	out := []posPackComponent{}
	for _, c := range p.Components {
		if c.SlotGroup == slot {
			out = append(out, c)
		}
	}
	return out
}

// loadPOSPacks reads the active packs with their components. Components are
// joined in one query so a 40-pack menu is 2 queries, not 41.
func (s *Server) loadPOSPacks(ctx context.Context, q posModifierGroupQueryer, restaurantID int, activeOnly bool) ([]posPack, error) {
	where := "p.restaurant_id=?"
	if activeOnly {
		where += " AND p.is_active=1"
	}
	rows, err := q.QueryContext(ctx, `SELECT p.id,p.name,COALESCE(p.description,''),p.price_gross_cents,`+posProductVATRateSQL+`,p.is_active,p.sort_order FROM pos_packs p LEFT JOIN stock_vat_rates v ON v.restaurant_id=p.restaurant_id AND v.id=p.vat_rate_id WHERE `+where+` ORDER BY p.sort_order,p.name`, restaurantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	// Capacity grows here, but the pointers below are taken only after the
	// loop is finished, so no append can move them afterwards.
	packs := make([]posPack, 0, 16)
	for rows.Next() {
		var p posPack
		var active int
		if err = rows.Scan(&p.ID, &p.Name, &p.Description, &p.PriceGrossCents, &p.VATRate, &active, &p.SortOrder); err != nil {
			return nil, err
		}
		p.IsActive = active != 0
		p.Components = []posPackComponent{}
		p.Slots = []string{}
		packs = append(packs, p)
	}
	// Pointers are taken only after the loop: appending during the loop can
	// reallocate the backing array and leave earlier pointers aimed at the
	// discarded copy, so every pack but the last would come back empty.
	ptrs := make([]*posPack, len(packs))
	for i := range packs {
		ptrs[i] = &packs[i]
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(packs) == 0 {
		return packs, nil
	}
	// One joined query for every pack's components, so a 40-menu catalog is two
	// queries, not forty-one.
	if err = s.scanPOSPackComponents(ctx, q, restaurantID, ptrs); err != nil {
		return nil, err
	}
	return packs, nil
}

// posPackRowQueryer additionally needs QueryRow for single-row reads. Both
// *sql.DB and *sql.Tx satisfy it.
type posPackRowQueryer interface {
	posModifierGroupQueryer
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// scanPOSPackComponents fills the components and slot list for one pack and
// resolves each component's VAT rate. It is shared by the list and single
// loaders so both derive the same component shape.
func (s *Server) scanPOSPackComponents(ctx context.Context, q posModifierGroupQueryer, restaurantID int, packs []*posPack) error {
	rows, err := q.QueryContext(ctx, `SELECT c.pack_id,c.pos_product_id,pr.name,c.quantity,COALESCE(c.slot_group,''),c.is_default,c.sort_order,`+posProductVATRateSQL+` FROM pos_pack_components c JOIN pos_products pr ON pr.restaurant_id=c.restaurant_id AND pr.id=c.pos_product_id LEFT JOIN stock_vat_rates v ON v.restaurant_id=c.restaurant_id AND v.id=pr.vat_rate_id WHERE c.restaurant_id=? ORDER BY c.sort_order,pr.name`, restaurantID)
	if err != nil {
		return err
	}
	defer rows.Close()
	index := make(map[int64]*posPack, len(packs))
	for _, p := range packs {
		index[p.ID] = p
	}
	seen := map[int64]map[string]bool{}
	for rows.Next() {
		var packID int64
		var c posPackComponent
		var qty float64
		var def int
		if err = rows.Scan(&packID, &c.ProductID, &c.ProductName, &qty, &c.SlotGroup, &def, &c.SortOrder, &c.VATRate); err != nil {
			return err
		}
		c.Quantity = qty
		c.IsDefault = def != 0
		p, ok := index[packID]
		if !ok {
			continue
		}
		p.Components = append(p.Components, c)
		if c.SlotGroup != "" {
			if seen[packID] == nil {
				seen[packID] = map[string]bool{}
			}
			if !seen[packID][c.SlotGroup] {
				seen[packID][c.SlotGroup] = true
				p.Slots = append(p.Slots, c.SlotGroup)
			}
		}
	}
	return rows.Err()
}

// loadPOSPack reads one pack with its components and its components' VAT.
func (s *Server) loadPOSPack(ctx context.Context, q posPackRowQueryer, restaurantID int, packID int64) (*posPack, error) {
	var p posPack
	var active int
	if err := q.QueryRowContext(ctx, `SELECT p.id,p.name,COALESCE(p.description,''),p.price_gross_cents,`+posProductVATRateSQL+`,p.is_active,p.sort_order FROM pos_packs p LEFT JOIN stock_vat_rates v ON v.restaurant_id=p.restaurant_id AND v.id=p.vat_rate_id WHERE p.restaurant_id=? AND p.id=?`, restaurantID, packID).Scan(&p.ID, &p.Name, &p.Description, &p.PriceGrossCents, &p.VATRate, &active, &p.SortOrder); err != nil {
		return nil, err
	}
	p.IsActive = active != 0
	p.Components = []posPackComponent{}
	p.Slots = []string{}
	if err := s.scanPOSPackComponents(ctx, q, restaurantID, []*posPack{&p}); err != nil {
		return nil, err
	}
	return &p, nil
}

// posPackSelection is the operator's answer for a pack: the line quantity plus
// one chosen component per slot.
type posPackSelection struct {
	Quantity float64 `json:"quantity"`
	// Choices maps slot group name -> chosen component id. A slot with a
	// single default may be omitted.
	Choices map[string]int64 `json:"choices"`
}

// resolvePOSPack expands a pack into its component lines.
//
// Exactly one component per slot is enforced: the pack price is fixed, so a
// slot with two chosen components would charge the menu price for a double
// portion, and a slot with none would sell the menu missing a course. A slot
// whose components are all defaults is filled automatically so a simple menu
// needs no taps at all.
func resolvePOSPack(pack posPack, sel posPackSelection) ([]posPackComponent, int64, error) {
	quantity := sel.Quantity
	if quantity == 0 {
		quantity = 1
	}
	// Menus are sold whole: a menu priced at 13,50 with a line quantity of 1,5
	// would either round to a surprise total or split the components in half.
	// Rejecting it makes the mistake visible instead of inventing a price.
	if quantity < 0 || quantity > 1000 || quantity != math.Trunc(quantity) {
		return nil, 0, errPOSPackSelection
	}
	// One component per slot, enforced by the slot loop itself. No global
	// "seen product" guard: two different slots may legitimately both choose
	// the same dish (an "ensalada" as a starter and as a garnish), and one
	// slot cannot repeat a product because it only ever contributes one pick.
	chosen := make([]posPackComponent, 0, len(pack.Components))
	for _, slot := range pack.Slots {
		options := pack.slotComponents(slot)
		if len(options) == 0 {
			continue
		}
		pickedID, ok := sel.Choices[slot]
		var picked *posPackComponent
		if ok {
			for i := range options {
				if options[i].ProductID == pickedID {
					picked = &options[i]
					break
				}
			}
			if picked == nil {
				// A choice outside the slot (or from another slot) would be a
				// component the pack does not sell at that price.
				return nil, 0, errPOSPackSelection
			}
		} else {
			// No answer for the slot: only acceptable when it is not a real
			// choice, i.e. every option is the default (nothing to pick between).
			allDefault := true
			for i := range options {
				if !options[i].IsDefault {
					allDefault = false
					break
				}
			}
			if !allDefault {
				return nil, 0, errPOSPackSelection
			}
			picked = &options[0]
		}
		chosen = append(chosen, *picked)
	}
	// Components with no slot are fixed parts of the pack.
	for _, c := range pack.Components {
		if c.SlotGroup != "" {
			continue
		}
		chosen = append(chosen, c)
	}
	if len(chosen) == 0 {
		return nil, 0, errPOSPackSelection
	}
	return chosen, int64(quantity), nil
}

var errPOSPackSelection = errors.New("invalid pack selection")

// posPackLineTotal is what the guest pays for the pack: the fixed pack price
// times the quantity. Components are priced at zero so the total is not
// double-counted (migration 078 money rule).
func posPackLineTotal(pack posPack, quantity int64) int64 {
	return pack.PriceGrossCents * quantity
}

// --- admin CRUD ---------------------------------------------------------

func (s *Server) posPackAdminAllowed(w http.ResponseWriter, r *http.Request, a boAuth) bool {
	allowed, err := s.boPOSPermissionAllowed(r.Context(), a, posPermissionCatalog)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error validating POS permission")
		return false
	}
	if !allowed {
		httpx.WriteError(w, http.StatusForbidden, "Forbidden")
		return false
	}
	return true
}

func (s *Server) handleBOPOSPacks(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	packs, err := s.loadPOSPacks(r.Context(), s.db, a.ActiveRestaurantID, r.URL.Query().Get("all") != "1")
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading packs")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "packs": packs})
}

func (s *Server) handleBOPOSPackCreate(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	if !s.posPackAdminAllowed(w, r, a) {
		return
	}
	var in struct {
		Name            string `json:"name"`
		Description     string `json:"description"`
		PriceGrossCents int64  `json:"priceGrossCents"`
		VATRateID       *int64 `json:"vatRateId"`
		SortOrder       int    `json:"sortOrder"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in) != nil || strings.TrimSpace(in.Name) == "" {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid pack")
		return
	}
	name := strings.TrimSpace(in.Name)
	if len(name) > 180 {
		name = name[:180]
	}
	if in.PriceGrossCents < 0 || in.PriceGrossCents > 100000000 {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid pack price")
		return
	}
	res, err := s.db.ExecContext(r.Context(), `INSERT INTO pos_packs (restaurant_id,name,description,price_gross_cents,vat_rate_id,sort_order) VALUES (?,?,?,?,?,?)`, a.ActiveRestaurantID, name, stockNullableString(in.Description), in.PriceGrossCents, in.VATRateID, in.SortOrder)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Pack could not be created")
		return
	}
	id, _ := res.LastInsertId()
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"success": true, "id": id})
}

func (s *Server) handleBOPOSPackPatch(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	if !s.posPackAdminAllowed(w, r, a) {
		return
	}
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if id <= 0 {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid pack")
		return
	}
	var in struct {
		Name            *string `json:"name"`
		Description     *string `json:"description"`
		PriceGrossCents *int64  `json:"priceGrossCents"`
		VATRateID       *int64  `json:"vatRateId"`
		SortOrder       *int    `json:"sortOrder"`
		IsActive        *bool   `json:"isActive"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in) != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid pack")
		return
	}
	var exists int
	if err := s.db.QueryRowContext(r.Context(), `SELECT 1 FROM pos_packs WHERE restaurant_id=? AND id=?`, a.ActiveRestaurantID, id).Scan(&exists); err != nil {
		httpx.WriteError(w, http.StatusNotFound, "Pack not found")
		return
	}
	if in.Name != nil {
		n := strings.TrimSpace(*in.Name)
		if n == "" {
			httpx.WriteError(w, http.StatusBadRequest, "Invalid pack")
			return
		}
		if len(n) > 180 {
			n = n[:180]
		}
		if _, err := s.db.ExecContext(r.Context(), `UPDATE pos_packs SET name=? WHERE restaurant_id=? AND id=?`, n, a.ActiveRestaurantID, id); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "Pack could not be updated")
			return
		}
	}
	if in.Description != nil {
		if _, err := s.db.ExecContext(r.Context(), `UPDATE pos_packs SET description=? WHERE restaurant_id=? AND id=?`, stockNullableString(*in.Description), a.ActiveRestaurantID, id); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "Pack could not be updated")
			return
		}
	}
	if in.PriceGrossCents != nil {
		if *in.PriceGrossCents < 0 || *in.PriceGrossCents > 100000000 {
			httpx.WriteError(w, http.StatusBadRequest, "Invalid pack price")
			return
		}
		if _, err := s.db.ExecContext(r.Context(), `UPDATE pos_packs SET price_gross_cents=? WHERE restaurant_id=? AND id=?`, *in.PriceGrossCents, a.ActiveRestaurantID, id); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "Pack could not be updated")
			return
		}
	}
	if in.VATRateID != nil {
		if _, err := s.db.ExecContext(r.Context(), `UPDATE pos_packs SET vat_rate_id=? WHERE restaurant_id=? AND id=?`, in.VATRateID, a.ActiveRestaurantID, id); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "Pack could not be updated")
			return
		}
	}
	if in.SortOrder != nil {
		if _, err := s.db.ExecContext(r.Context(), `UPDATE pos_packs SET sort_order=? WHERE restaurant_id=? AND id=?`, *in.SortOrder, a.ActiveRestaurantID, id); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "Pack could not be updated")
			return
		}
	}
	if in.IsActive != nil {
		v := 0
		if *in.IsActive {
			v = 1
		}
		if _, err := s.db.ExecContext(r.Context(), `UPDATE pos_packs SET is_active=? WHERE restaurant_id=? AND id=?`, v, a.ActiveRestaurantID, id); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "Pack could not be updated")
			return
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) handleBOPOSPackDelete(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	if !s.posPackAdminAllowed(w, r, a) {
		return
	}
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if id <= 0 {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid pack")
		return
	}
	// Sold lines keep pack_id, so the pack can only be deactivated once it has
	// been sold: deleting it would orphan paid tickets.
	var sold int
	if err := s.db.QueryRowContext(r.Context(), `SELECT 1 FROM pos_ticket_lines WHERE restaurant_id=? AND pack_id=? LIMIT 1`, a.ActiveRestaurantID, id).Scan(&sold); err == nil {
		if _, err := s.db.ExecContext(r.Context(), `UPDATE pos_packs SET is_active=0 WHERE restaurant_id=? AND id=?`, a.ActiveRestaurantID, id); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "Pack could not be removed")
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "deactivated": true})
		return
	}
	if _, err := s.db.ExecContext(r.Context(), `DELETE FROM pos_pack_components WHERE restaurant_id=? AND pack_id=?`, a.ActiveRestaurantID, id); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Pack could not be removed")
		return
	}
	if _, err := s.db.ExecContext(r.Context(), `DELETE FROM pos_packs WHERE restaurant_id=? AND id=?`, a.ActiveRestaurantID, id); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Pack could not be removed")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) handleBOPOSPackComponentCreate(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	if !s.posPackAdminAllowed(w, r, a) {
		return
	}
	packID, _ := strconv.ParseInt(chi.URLParam(r, "packId"), 10, 64)
	var in struct {
		ProductID int64   `json:"productId"`
		Quantity  float64 `json:"quantity"`
		SlotGroup string  `json:"slotGroup"`
		IsDefault bool    `json:"isDefault"`
		SortOrder int     `json:"sortOrder"`
	}
	if packID <= 0 || json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in) != nil || in.ProductID <= 0 {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid pack component")
		return
	}
	if in.Quantity == 0 {
		in.Quantity = 1
	}
	if in.Quantity < 0 || in.Quantity > 1000 {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid pack component quantity")
		return
	}
	slot := strings.TrimSpace(in.SlotGroup)
	if len(slot) > 80 {
		slot = slot[:80]
	}
	var prodExists int
	if err := s.db.QueryRowContext(r.Context(), `SELECT 1 FROM pos_products WHERE restaurant_id=? AND id=?`, a.ActiveRestaurantID, in.ProductID).Scan(&prodExists); err != nil {
		httpx.WriteError(w, http.StatusNotFound, "POS product not found")
		return
	}
	def := 0
	if in.IsDefault {
		def = 1
	}
	res, err := s.db.ExecContext(r.Context(), `INSERT INTO pos_pack_components (restaurant_id,pack_id,pos_product_id,quantity,slot_group,is_default,sort_order) VALUES (?,?,?,?,?,?,?)`, a.ActiveRestaurantID, packID, in.ProductID, in.Quantity, stockNullableString(slot), def, in.SortOrder)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Pack component could not be created")
		return
	}
	id, _ := res.LastInsertId()
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"success": true, "id": id})
}

func (s *Server) handleBOPOSPackComponentPatch(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	if !s.posPackAdminAllowed(w, r, a) {
		return
	}
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if id <= 0 {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid pack component")
		return
	}
	var in struct {
		Quantity  *float64 `json:"quantity"`
		SlotGroup *string  `json:"slotGroup"`
		IsDefault *bool    `json:"isDefault"`
		SortOrder *int     `json:"sortOrder"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in) != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid pack component")
		return
	}
	var exists int
	if err := s.db.QueryRowContext(r.Context(), `SELECT 1 FROM pos_pack_components WHERE restaurant_id=? AND id=?`, a.ActiveRestaurantID, id).Scan(&exists); err != nil {
		httpx.WriteError(w, http.StatusNotFound, "Pack component not found")
		return
	}
	if in.Quantity != nil {
		if *in.Quantity <= 0 || *in.Quantity > 1000 {
			httpx.WriteError(w, http.StatusBadRequest, "Invalid pack component quantity")
			return
		}
		if _, err := s.db.ExecContext(r.Context(), `UPDATE pos_pack_components SET quantity=? WHERE restaurant_id=? AND id=?`, *in.Quantity, a.ActiveRestaurantID, id); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "Pack component could not be updated")
			return
		}
	}
	if in.SlotGroup != nil {
		slot := strings.TrimSpace(*in.SlotGroup)
		if len(slot) > 80 {
			slot = slot[:80]
		}
		if _, err := s.db.ExecContext(r.Context(), `UPDATE pos_pack_components SET slot_group=? WHERE restaurant_id=? AND id=?`, stockNullableString(slot), a.ActiveRestaurantID, id); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "Pack component could not be updated")
			return
		}
	}
	if in.IsDefault != nil {
		def := 0
		if *in.IsDefault {
			def = 1
		}
		if _, err := s.db.ExecContext(r.Context(), `UPDATE pos_pack_components SET is_default=? WHERE restaurant_id=? AND id=?`, def, a.ActiveRestaurantID, id); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "Pack component could not be updated")
			return
		}
	}
	if in.SortOrder != nil {
		if _, err := s.db.ExecContext(r.Context(), `UPDATE pos_pack_components SET sort_order=? WHERE restaurant_id=? AND id=?`, *in.SortOrder, a.ActiveRestaurantID, id); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "Pack component could not be updated")
			return
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) handleBOPOSPackComponentDelete(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	if !s.posPackAdminAllowed(w, r, a) {
		return
	}
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if id <= 0 {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid pack component")
		return
	}
	if _, err := s.db.ExecContext(r.Context(), `DELETE FROM pos_pack_components WHERE restaurant_id=? AND id=?`, a.ActiveRestaurantID, id); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Pack component could not be removed")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}
