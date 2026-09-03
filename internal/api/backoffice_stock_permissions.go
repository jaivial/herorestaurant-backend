package api

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"preactvillacarmen/internal/httpx"
)

// Stock role permissions are stored sparsely: a missing row means "fall back to
// the role default", which is allow for root/admin and deny for everyone else.
// The catalogue endpoint materialises that fallback so the UI can show the
// effective answer without duplicating the rule.

func (s *Server) stockPermissionsCatalogue(ctx context.Context, restaurantID int, role string) ([]map[string]any, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT permission_key,is_allowed FROM stock_role_permissions WHERE restaurant_id=? AND role_slug=?`,
		restaurantID, role)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	overrides := map[string]bool{}
	for rows.Next() {
		var key string
		var allowed int
		if err = rows.Scan(&key, &allowed); err != nil {
			return nil, err
		}
		overrides[key] = allowed != 0
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(stockPermissionKeys))
	for _, key := range stockPermissionKeys {
		allowed, exists := overrides[key]
		if !exists {
			allowed = role == "root" || role == "admin"
		}
		items = append(items, map[string]any{"key": key, "allowed": allowed})
	}
	return items, nil
}

func (s *Server) handleBOStockRolePermissionsGet(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	role := normalizeBORole(chi.URLParam(r, "slug"))
	if role == "" {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid role")
		return
	}
	items, err := s.stockPermissionsCatalogue(r.Context(), a.ActiveRestaurantID, role)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading stock permissions")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "role": role, "permissions": items})
}

// The catalogue endpoint requires stock.settings.manage, so a restricted role
// cannot read its own effective permissions to render the UI. This endpoint is
// session-only and returns the caller's own permissions instead.
func (s *Server) handleBOStockPermissionsMine(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	role := normalizeBORole(a.Role)
	if role == "" {
		httpx.WriteError(w, http.StatusForbidden, "Invalid role")
		return
	}
	items, err := s.stockPermissionsCatalogue(r.Context(), a.ActiveRestaurantID, role)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading stock permissions")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "role": role, "permissions": items})
}

func (s *Server) handleBOStockRolePermissionsPut(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	role := normalizeBORole(chi.URLParam(r, "slug"))
	var in struct {
		Permissions []string `json:"permissions"`
	}
	if role == "" || json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid stock permissions")
		return
	}
	selected := map[string]bool{}
	for _, key := range in.Permissions {
		selected[key] = true
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error saving stock permissions")
		return
	}
	defer tx.Rollback()
	// Only catalogue keys are written, so a stale or hostile client cannot seed
	// rows that no handler will ever check.
	for _, key := range stockPermissionKeys {
		if _, err = tx.ExecContext(r.Context(),
			`INSERT INTO stock_role_permissions (restaurant_id,role_slug,permission_key,is_allowed) VALUES (?,?,?,?)
			 ON DUPLICATE KEY UPDATE is_allowed=VALUES(is_allowed)`,
			a.ActiveRestaurantID, role, key, stockBoolInt(selected[key])); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error saving stock permissions")
			return
		}
	}
	if err = tx.Commit(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error saving stock permissions")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}
