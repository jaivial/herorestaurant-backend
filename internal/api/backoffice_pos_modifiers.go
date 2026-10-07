package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"preactvillacarmen/internal/httpx"
)

// POS modifiers ("variantes"/"extras"): the size, preparation or extra choices
// a guest picks when ordering -- "Talla mediana", "Con queso extra", "Al punto",
// "Sin gluten". The schema has always been there (pos_modifier_groups,
// pos_modifier_options, pos_product_modifier_groups, pos_ticket_line_modifiers)
// but nothing read or wrote it, so a line could never be priced differently
// from its catalog price. This is the cheapest high-value POS feature that was
// missing: no restaurant menu works without it.
//
// Coordination id: pos_modifiers_v1

type posModifierOption struct {
	ID              int64  `json:"id"`
	Name            string `json:"name"`
	PriceDeltaCents int64  `json:"priceDeltaCents"`
	SourceProductID *int64 `json:"sourceProductId,omitempty"`
	SortOrder       int    `json:"sortOrder"`
	IsActive        bool   `json:"isActive"`
}

type posModifierGroup struct {
	ID        int64               `json:"id"`
	Name      string              `json:"name"`
	Kind      string              `json:"kind"`
	MinSelect int                 `json:"minSelect"`
	MaxSelect int                 `json:"maxSelect"`
	SortOrder int                 `json:"sortOrder"`
	IsActive  bool                `json:"isActive"`
	Options   []posModifierOption `json:"options"`
}

func (g posModifierGroup) sortedOptions() []posModifierOption {
	sort.SliceStable(g.Options, func(i, j int) bool {
		if g.Options[i].SortOrder != g.Options[j].SortOrder {
			return g.Options[i].SortOrder < g.Options[j].SortOrder
		}
		return g.Options[i].Name < g.Options[j].Name
	})
	return g.Options
}

func sortModifierGroups(groups []posModifierGroup) {
	for i := range groups {
		groups[i].Options = groups[i].sortedOptions()
	}
	sort.SliceStable(groups, func(i, j int) bool {
		if groups[i].SortOrder != groups[j].SortOrder {
			return groups[i].SortOrder < groups[j].SortOrder
		}
		return groups[i].Name < groups[j].Name
	})
}

// posModifierGroupQueryer is satisfied by both *sql.DB and *sql.Tx so the
// catalog can be read outside a transaction (bootstrap, admin CRUD) and inside
// one (line insert), without duplicating the SQL.
type posModifierGroupQueryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func loadPOSModifierGroupsFrom(ctx context.Context, q posModifierGroupQueryer, restaurantID int, productID int64) ([]posModifierGroup, error) {
	where := `g.restaurant_id=? AND g.is_active=1`
	args := []any{restaurantID}
	if productID > 0 {
		where += ` AND pmg.pos_product_id=?`
		args = append(args, productID)
	}
	join := `JOIN pos_product_modifier_groups pmg ON pmg.restaurant_id=g.restaurant_id AND pmg.group_id=g.id`
	order := `pmg.sort_order, g.sort_order, g.name`
	if productID <= 0 {
		join = `LEFT JOIN pos_product_modifier_groups pmg ON pmg.restaurant_id=g.restaurant_id AND pmg.group_id=g.id AND pmg.pos_product_id<>0`
		order = `g.sort_order, g.name`
	}
	rows, err := q.QueryContext(ctx, `SELECT DISTINCT g.id,g.name,g.kind,g.min_select,g.max_select,g.sort_order,g.is_active FROM pos_modifier_groups g `+join+` WHERE `+where+` ORDER BY `+order, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := []posModifierGroup{}
	index := map[int64]int{}
	for rows.Next() {
		var g posModifierGroup
		var active int
		if err = rows.Scan(&g.ID, &g.Name, &g.Kind, &g.MinSelect, &g.MaxSelect, &g.SortOrder, &active); err != nil {
			return nil, err
		}
		g.IsActive = active != 0
		g.Options = []posModifierOption{}
		index[g.ID] = len(groups)
		groups = append(groups, g)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(groups) == 0 {
		return groups, nil
	}
	optRows, err := q.QueryContext(ctx, `SELECT o.id,o.group_id,o.name,o.price_delta_cents,o.source_product_id,o.sort_order,o.is_active FROM pos_modifier_options o JOIN pos_modifier_groups g ON g.restaurant_id=o.restaurant_id AND g.id=o.group_id WHERE o.restaurant_id=? AND o.is_active=1 AND g.is_active=1 ORDER BY o.sort_order,o.name`, restaurantID)
	if err != nil {
		return nil, err
	}
	defer optRows.Close()
	for optRows.Next() {
		var o posModifierOption
		var groupID int64
		var source sql.NullInt64
		var active int
		if err = optRows.Scan(&o.ID, &groupID, &o.Name, &o.PriceDeltaCents, &source, &o.SortOrder, &active); err != nil {
			return nil, err
		}
		if source.Valid {
			v := source.Int64
			o.SourceProductID = &v
		}
		o.IsActive = active != 0
		i, ok := index[groupID]
		if !ok {
			continue
		}
		groups[i].Options = append(groups[i].Options, o)
	}
	if err = optRows.Err(); err != nil {
		return nil, err
	}
	sortModifierGroups(groups)
	return groups, nil
}

// posModifierCatalogForProducts returns the groups per product id for the POS
// bootstrap, keyed by product id as a string (JSON object keys). Two queries
// total, not one per product: a menu with a modifier on every dish would
// otherwise fire hundreds of round trips on every POS load.
func (s *Server) posModifierCatalogForProducts(ctx context.Context, restaurantID int) (map[string]any, error) {
	out := map[string]any{}
	rows, err := s.db.QueryContext(ctx, `SELECT g.id,g.name,g.kind,g.min_select,g.max_select,g.sort_order,g.is_active,pmg.pos_product_id FROM pos_modifier_groups g JOIN pos_product_modifier_groups pmg ON pmg.restaurant_id=g.restaurant_id AND pmg.group_id=g.id WHERE g.restaurant_id=? AND g.is_active=1 ORDER BY pmg.pos_product_id,pmg.sort_order,g.sort_order,g.name`, restaurantID)
	if err != nil {
		return nil, err
	}
	type groupRef struct {
		group posModifierGroup
		key   string
	}
	// refs[productID] holds pointers into `slots` so options can be appended in
	// a second pass without another lookup table.
	slots := []posModifierGroup{}
	index := map[int64]map[int64]int{}
	productIDs := []int64{}
	for rows.Next() {
		var g posModifierGroup
		var active int
		var productID int64
		if err = rows.Scan(&g.ID, &g.Name, &g.Kind, &g.MinSelect, &g.MaxSelect, &g.SortOrder, &active, &productID); err != nil {
			rows.Close()
			return nil, err
		}
		g.IsActive = active != 0
		g.Options = []posModifierOption{}
		if _, ok := index[productID]; !ok {
			index[productID] = map[int64]int{}
			productIDs = append(productIDs, productID)
		}
		index[productID][g.ID] = len(slots)
		slots = append(slots, g)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	if len(slots) == 0 {
		return out, nil
	}
	optRows, err := s.db.QueryContext(ctx, `SELECT id,group_id,name,price_delta_cents,source_product_id,sort_order,is_active FROM pos_modifier_options WHERE restaurant_id=? AND is_active=1 ORDER BY sort_order,name`, restaurantID)
	if err != nil {
		return nil, err
	}
	defer optRows.Close()
	for optRows.Next() {
		var o posModifierOption
		var groupID int64
		var source sql.NullInt64
		var active int
		if err = optRows.Scan(&o.ID, &groupID, &o.Name, &o.PriceDeltaCents, &source, &o.SortOrder, &active); err != nil {
			return nil, err
		}
		if source.Valid {
			v := source.Int64
			o.SourceProductID = &v
		}
		o.IsActive = active != 0
		// A group can belong to several products; fan the option out to each.
		for _, groups := range index {
			if slot, ok := groups[groupID]; ok {
				slots[slot].Options = append(slots[slot].Options, o)
			}
		}
	}
	if err = optRows.Err(); err != nil {
		return nil, err
	}
	for _, productID := range productIDs {
		groups := make([]posModifierGroup, 0, len(index[productID]))
		for _, slot := range index[productID] {
			groups = append(groups, slots[slot])
		}
		sortModifierGroups(groups)
		out[strconv.FormatInt(productID, 10)] = groups
	}
	return out, nil
}

// posModifierSelection is one option the till picked for a line.
type posModifierSelection struct {
	ModifierOptionID int64   `json:"modifierOptionId"`
	Quantity         float64 `json:"quantity"`
}

type posModifierResolved struct {
	OptionID        int64
	Name            string
	PriceDeltaCents int64
	Quantity        float64
	SourceProductID *int64
}

var errPOSModifierSelection = errors.New("invalid modifier selection")

// resolvePOSModifiers validates the requested options against the groups that
// actually apply to the product and returns the rows to persist plus the total
// price delta for ONE unit of the line.
//
// The till sends bare option ids, so an unknown option, an option belonging to
// a group the product does not use, or a pick that breaks a group's min/max
// would all be a price the catalog never approved -- and it would land on a
// fiscal receipt. Reject instead of silently dropping.
//
// A group with max_select=0 means "unlimited" (COMBO-style), matching the
// schema default of 1 for a plain OPTION group.
func resolvePOSModifiers(groups []posModifierGroup, selections []posModifierSelection) ([]posModifierResolved, int64, error) {
	if len(selections) == 0 {
		return nil, 0, nil
	}
	if len(groups) == 0 {
		return nil, 0, errPOSModifierSelection
	}
	allowed := map[int64]posModifierResolved{}
	groupOf := map[int64]int64{}
	for _, g := range groups {
		for _, o := range g.Options {
			allowed[o.ID] = posModifierResolved{OptionID: o.ID, Name: o.Name, PriceDeltaCents: o.PriceDeltaCents, Quantity: 1, SourceProductID: o.SourceProductID}
			groupOf[o.ID] = g.ID
		}
	}
	// min/max count DISTINCT options: a group with max_select=1 means "choose
	// one", and "dos milanesas" is one option with quantity 2, not two options.
	// Keyed by option so a duplicated entry in the request folds into the same
	// option with its quantities added, instead of tripping the max.
	perGroup := map[int64]int{}
	byOption := map[int64]int{}
	resolved := make([]posModifierResolved, 0, len(selections))
	var delta int64
	for _, sel := range selections {
		opt, ok := allowed[sel.ModifierOptionID]
		if !ok {
			return nil, 0, errPOSModifierSelection
		}
		qty := sel.Quantity
		if qty == 0 {
			qty = 1
		}
		if qty < 0 || qty > 1000 {
			return nil, 0, errPOSModifierSelection
		}
		opt.Quantity = qty
		gid := groupOf[opt.OptionID]
		if _, dup := byOption[opt.OptionID]; !dup {
			perGroup[gid]++
			byOption[opt.OptionID] = len(resolved)
			resolved = append(resolved, opt)
			delta += int64(math.Round(qty * float64(opt.PriceDeltaCents)))
			continue
		}
		existing := &resolved[byOption[opt.OptionID]]
		existing.Quantity += qty
		delta += int64(math.Round(qty * float64(opt.PriceDeltaCents)))
	}
	for _, g := range groups {
		n := perGroup[g.ID]
		if n < g.MinSelect {
			return nil, 0, errPOSModifierSelection
		}
		if g.MaxSelect > 0 && n > g.MaxSelect {
			return nil, 0, errPOSModifierSelection
		}
	}
	return resolved, delta, nil
}

func persistPOSModifiers(ctx context.Context, tx *sql.Tx, restaurantID int, lineID int64, rows []posModifierResolved) error {
	for _, m := range rows {
		if _, err := tx.ExecContext(ctx, `INSERT INTO pos_ticket_line_modifiers (restaurant_id,ticket_line_id,modifier_option_id,name_snapshot,price_delta_cents,quantity,source_product_id) VALUES (?,?,?,?,?,?,?)`, restaurantID, lineID, m.OptionID, m.Name, m.PriceDeltaCents, m.Quantity, m.SourceProductID); err != nil {
			return err
		}
	}
	return nil
}

// loadPOSTicketModifiers returns the modifiers of each line keyed by line id.
// Voids keep their modifiers (status lives on the line, not the modifier) so an
// auditor can still read what was ordered before the void.
func (s *Server) loadPOSTicketModifiers(ctx context.Context, restaurantID int, ticketID int64) (map[int64][]map[string]any, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT m.ticket_line_id,m.modifier_option_id,m.name_snapshot,m.price_delta_cents,m.quantity FROM pos_ticket_line_modifiers m JOIN pos_ticket_lines l ON l.restaurant_id=m.restaurant_id AND l.id=m.ticket_line_id WHERE m.restaurant_id=? AND l.ticket_id=? ORDER BY m.ticket_line_id,m.id`, restaurantID, ticketID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]map[string]any{}
	for rows.Next() {
		var lineID int64
		var optionID sql.NullInt64
		var name string
		var delta int64
		var qty float64
		if err = rows.Scan(&lineID, &optionID, &name, &delta, &qty); err != nil {
			return nil, err
		}
		out[lineID] = append(out[lineID], map[string]any{"modifierOptionId": stockNullableDBInt(optionID), "name": name, "priceDeltaCents": delta, "quantity": qty})
	}
	return out, rows.Err()
}

// --- admin CRUD ---------------------------------------------------------

func (s *Server) posModifierAdminAllowed(w http.ResponseWriter, r *http.Request, a boAuth) bool {
	allowed, err := s.boPOSPermissionAllowed(r.Context(), a, posPermissionModifiers)
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

func (s *Server) handleBOPOSModifierGroups(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	if !s.posModifierAdminAllowed(w, r, a) {
		return
	}
	groups, err := loadPOSModifierGroupsFrom(r.Context(), s.db, a.ActiveRestaurantID, 0)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading modifier groups")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "groups": groups})
}

func (s *Server) handleBOPOSModifierGroupCreate(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	if !s.posModifierAdminAllowed(w, r, a) {
		return
	}
	var in struct {
		Name      string `json:"name"`
		Kind      string `json:"kind"`
		MinSelect int    `json:"minSelect"`
		MaxSelect int    `json:"maxSelect"`
		SortOrder int    `json:"sortOrder"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in) != nil || strings.TrimSpace(in.Name) == "" {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid modifier group")
		return
	}
	name := strings.TrimSpace(in.Name)
	if len(name) > 120 {
		name = name[:120]
	}
	kind := strings.ToUpper(strings.TrimSpace(in.Kind))
	if kind != "COMBO" && kind != "SUPPLEMENT" && kind != "OPTION" {
		kind = "OPTION"
	}
	minSelect, maxSelect := clampPOSSelectBounds(in.MinSelect, in.MaxSelect)
	res, err := s.db.ExecContext(r.Context(), `INSERT INTO pos_modifier_groups (restaurant_id,name,kind,min_select,max_select,sort_order) VALUES (?,?,?,?,?,?)`, a.ActiveRestaurantID, name, kind, minSelect, maxSelect, in.SortOrder)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Modifier group could not be created")
		return
	}
	id, _ := res.LastInsertId()
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"success": true, "id": id})
}

// clampPOSSelectBounds keeps min/max inside what the till can realistically
// offer and consistent with each other (max < min would make every selection
// unsatisfiable).
func clampPOSSelectBounds(minSelect, maxSelect int) (int, int) {
	if minSelect < 0 {
		minSelect = 0
	}
	if minSelect > 10 {
		minSelect = 10
	}
	if maxSelect < 0 {
		maxSelect = 0
	}
	if maxSelect > 20 {
		maxSelect = 20
	}
	if maxSelect > 0 && maxSelect < minSelect {
		maxSelect = minSelect
	}
	return minSelect, maxSelect
}

func (s *Server) handleBOPOSModifierGroupPatch(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	if !s.posModifierAdminAllowed(w, r, a) {
		return
	}
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if id <= 0 {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid modifier group")
		return
	}
	var in struct {
		Name      *string `json:"name"`
		Kind      *string `json:"kind"`
		MinSelect *int    `json:"minSelect"`
		MaxSelect *int    `json:"maxSelect"`
		SortOrder *int    `json:"sortOrder"`
		IsActive  *bool   `json:"isActive"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in) != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid modifier group")
		return
	}
	var exists int
	if err := s.db.QueryRowContext(r.Context(), `SELECT 1 FROM pos_modifier_groups WHERE restaurant_id=? AND id=?`, a.ActiveRestaurantID, id).Scan(&exists); err != nil {
		httpx.WriteError(w, http.StatusNotFound, "Modifier group not found")
		return
	}
	if in.Name != nil {
		n := strings.TrimSpace(*in.Name)
		if n == "" {
			httpx.WriteError(w, http.StatusBadRequest, "Invalid modifier group")
			return
		}
		if len(n) > 120 {
			n = n[:120]
		}
		if _, err := s.db.ExecContext(r.Context(), `UPDATE pos_modifier_groups SET name=? WHERE restaurant_id=? AND id=?`, n, a.ActiveRestaurantID, id); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "Modifier group could not be updated")
			return
		}
	}
	if in.Kind != nil {
		k := strings.ToUpper(strings.TrimSpace(*in.Kind))
		if k != "COMBO" && k != "SUPPLEMENT" && k != "OPTION" {
			k = "OPTION"
		}
		if _, err := s.db.ExecContext(r.Context(), `UPDATE pos_modifier_groups SET kind=? WHERE restaurant_id=? AND id=?`, k, a.ActiveRestaurantID, id); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "Modifier group could not be updated")
			return
		}
	}
	if in.MinSelect != nil || in.MaxSelect != nil {
		var minSelect, maxSelect int
		if err := s.db.QueryRowContext(r.Context(), `SELECT min_select,max_select FROM pos_modifier_groups WHERE restaurant_id=? AND id=?`, a.ActiveRestaurantID, id).Scan(&minSelect, &maxSelect); err != nil {
			httpx.WriteError(w, http.StatusNotFound, "Modifier group not found")
			return
		}
		if in.MinSelect != nil {
			minSelect = *in.MinSelect
		}
		if in.MaxSelect != nil {
			maxSelect = *in.MaxSelect
		}
		minSelect, maxSelect = clampPOSSelectBounds(minSelect, maxSelect)
		if _, err := s.db.ExecContext(r.Context(), `UPDATE pos_modifier_groups SET min_select=?,max_select=? WHERE restaurant_id=? AND id=?`, minSelect, maxSelect, a.ActiveRestaurantID, id); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "Modifier group could not be updated")
			return
		}
	}
	if in.SortOrder != nil {
		if _, err := s.db.ExecContext(r.Context(), `UPDATE pos_modifier_groups SET sort_order=? WHERE restaurant_id=? AND id=?`, *in.SortOrder, a.ActiveRestaurantID, id); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "Modifier group could not be updated")
			return
		}
	}
	if in.IsActive != nil {
		v := 0
		if *in.IsActive {
			v = 1
		}
		if _, err := s.db.ExecContext(r.Context(), `UPDATE pos_modifier_groups SET is_active=? WHERE restaurant_id=? AND id=?`, v, a.ActiveRestaurantID, id); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "Modifier group could not be updated")
			return
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) handleBOPOSModifierGroupDelete(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	if !s.posModifierAdminAllowed(w, r, a) {
		return
	}
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if id <= 0 {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid modifier group")
		return
	}
	// Ordered so a failure never leaves options pointing at a missing group.
	// pos_ticket_line_modifiers keeps name_snapshot/price_delta_cents, so
	// already-issued tickets stay readable; only the catalog is removed.
	if _, err := s.db.ExecContext(r.Context(), `DELETE FROM pos_product_modifier_groups WHERE restaurant_id=? AND group_id=?`, a.ActiveRestaurantID, id); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Modifier group could not be removed")
		return
	}
	if _, err := s.db.ExecContext(r.Context(), `DELETE FROM pos_modifier_options WHERE restaurant_id=? AND group_id=?`, a.ActiveRestaurantID, id); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Modifier group could not be removed")
		return
	}
	if _, err := s.db.ExecContext(r.Context(), `DELETE FROM pos_modifier_groups WHERE restaurant_id=? AND id=?`, a.ActiveRestaurantID, id); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Modifier group could not be removed")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) handleBOPOSModifierOptionCreate(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	if !s.posModifierAdminAllowed(w, r, a) {
		return
	}
	groupID, _ := strconv.ParseInt(chi.URLParam(r, "groupId"), 10, 64)
	var in struct {
		Name            string `json:"name"`
		PriceDeltaCents int64  `json:"priceDeltaCents"`
		SourceProductID *int64 `json:"sourceProductId"`
		SortOrder       int    `json:"sortOrder"`
	}
	if groupID <= 0 || json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in) != nil || strings.TrimSpace(in.Name) == "" {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid modifier option")
		return
	}
	if in.PriceDeltaCents < -100000000 || in.PriceDeltaCents > 100000000 {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid modifier price")
		return
	}
	name := strings.TrimSpace(in.Name)
	if len(name) > 180 {
		name = name[:180]
	}
	var exists int
	if err := s.db.QueryRowContext(r.Context(), `SELECT 1 FROM pos_modifier_groups WHERE restaurant_id=? AND id=?`, a.ActiveRestaurantID, groupID).Scan(&exists); err != nil {
		httpx.WriteError(w, http.StatusNotFound, "Modifier group not found")
		return
	}
	res, err := s.db.ExecContext(r.Context(), `INSERT INTO pos_modifier_options (restaurant_id,group_id,name,price_delta_cents,source_product_id,sort_order) VALUES (?,?,?,?,?,?)`, a.ActiveRestaurantID, groupID, name, in.PriceDeltaCents, in.SourceProductID, in.SortOrder)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Modifier option could not be created")
		return
	}
	id, _ := res.LastInsertId()
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"success": true, "id": id})
}

func (s *Server) handleBOPOSModifierOptionPatch(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	if !s.posModifierAdminAllowed(w, r, a) {
		return
	}
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if id <= 0 {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid modifier option")
		return
	}
	var in struct {
		Name            *string `json:"name"`
		PriceDeltaCents *int64  `json:"priceDeltaCents"`
		SortOrder       *int    `json:"sortOrder"`
		IsActive        *bool   `json:"isActive"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in) != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid modifier option")
		return
	}
	var exists int
	if err := s.db.QueryRowContext(r.Context(), `SELECT 1 FROM pos_modifier_options WHERE restaurant_id=? AND id=?`, a.ActiveRestaurantID, id).Scan(&exists); err != nil {
		httpx.WriteError(w, http.StatusNotFound, "Modifier option not found")
		return
	}
	if in.Name != nil {
		n := strings.TrimSpace(*in.Name)
		if n == "" {
			httpx.WriteError(w, http.StatusBadRequest, "Invalid modifier option")
			return
		}
		if len(n) > 180 {
			n = n[:180]
		}
		if _, err := s.db.ExecContext(r.Context(), `UPDATE pos_modifier_options SET name=? WHERE restaurant_id=? AND id=?`, n, a.ActiveRestaurantID, id); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "Modifier option could not be updated")
			return
		}
	}
	if in.PriceDeltaCents != nil {
		if *in.PriceDeltaCents < -100000000 || *in.PriceDeltaCents > 100000000 {
			httpx.WriteError(w, http.StatusBadRequest, "Invalid modifier price")
			return
		}
		if _, err := s.db.ExecContext(r.Context(), `UPDATE pos_modifier_options SET price_delta_cents=? WHERE restaurant_id=? AND id=?`, *in.PriceDeltaCents, a.ActiveRestaurantID, id); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "Modifier option could not be updated")
			return
		}
	}
	if in.SortOrder != nil {
		if _, err := s.db.ExecContext(r.Context(), `UPDATE pos_modifier_options SET sort_order=? WHERE restaurant_id=? AND id=?`, *in.SortOrder, a.ActiveRestaurantID, id); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "Modifier option could not be updated")
			return
		}
	}
	if in.IsActive != nil {
		v := 0
		if *in.IsActive {
			v = 1
		}
		if _, err := s.db.ExecContext(r.Context(), `UPDATE pos_modifier_options SET is_active=? WHERE restaurant_id=? AND id=?`, v, a.ActiveRestaurantID, id); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "Modifier option could not be updated")
			return
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) handleBOPOSModifierOptionDelete(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	if !s.posModifierAdminAllowed(w, r, a) {
		return
	}
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if id <= 0 {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid modifier option")
		return
	}
	if _, err := s.db.ExecContext(r.Context(), `DELETE FROM pos_modifier_options WHERE restaurant_id=? AND id=?`, a.ActiveRestaurantID, id); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Modifier option could not be removed")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) handleBOPOSProductModifierGroups(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	productID, _ := strconv.ParseInt(chi.URLParam(r, "productId"), 10, 64)
	if productID <= 0 {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid product")
		return
	}
	if r.Method == http.MethodPut || r.Method == http.MethodPost {
		if !s.posModifierAdminAllowed(w, r, a) {
			return
		}
		var in struct {
			GroupIDs []int64 `json:"groupIds"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in) != nil {
			httpx.WriteError(w, http.StatusBadRequest, "Invalid modifier assignment")
			return
		}
		if len(in.GroupIDs) > 20 {
			httpx.WriteError(w, http.StatusBadRequest, "Too many modifier groups")
			return
		}
		for _, gid := range in.GroupIDs {
			var exists int
			if err := s.db.QueryRowContext(r.Context(), `SELECT 1 FROM pos_modifier_groups WHERE restaurant_id=? AND id=?`, a.ActiveRestaurantID, gid).Scan(&exists); err != nil {
				httpx.WriteError(w, http.StatusBadRequest, "Unknown modifier group")
				return
			}
		}
		// Only assign groups that exist and belong to this restaurant, so a
		// crafted id cannot attach another restaurant's group.
		if _, err := s.db.ExecContext(r.Context(), `DELETE FROM pos_product_modifier_groups WHERE restaurant_id=? AND pos_product_id=?`, a.ActiveRestaurantID, productID); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "Modifier assignment could not be saved")
			return
		}
		var prodExists int
		if err := s.db.QueryRowContext(r.Context(), `SELECT 1 FROM pos_products WHERE restaurant_id=? AND id=?`, a.ActiveRestaurantID, productID).Scan(&prodExists); err != nil {
			httpx.WriteError(w, http.StatusNotFound, "POS product not found")
			return
		}
		for i, gid := range in.GroupIDs {
			if _, err := s.db.ExecContext(r.Context(), `INSERT INTO pos_product_modifier_groups (restaurant_id,pos_product_id,group_id,is_required,sort_order) VALUES (?,?,?,?,?)`, a.ActiveRestaurantID, productID, gid, 0, i); err != nil {
				httpx.WriteError(w, http.StatusBadRequest, "Modifier assignment could not be saved")
				return
			}
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
		return
	}
	groups, err := loadPOSModifierGroupsFrom(r.Context(), s.db, a.ActiveRestaurantID, productID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading modifier assignment")
		return
	}
	if groups == nil {
		groups = []posModifierGroup{}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "groups": groups})
}

// mustPOSModifierGroups reads the groups assigned to a product inside the
// line-insert transaction. A read failure cannot be distinguished from "no
// groups" here without duplicating the validation error, so it yields no
// groups and resolvePOSModifiers rejects the selection -- the same outcome as a
// product that has no modifiers, which is the safe direction: no modifier is
// ever priced from an unreadable catalog.
func mustPOSModifierGroups(ctx context.Context, tx *sql.Tx, restaurantID int, productID int64) []posModifierGroup {
	groups, err := loadPOSModifierGroupsFrom(ctx, tx, restaurantID, productID)
	if err != nil {
		return nil
	}
	return groups
}
