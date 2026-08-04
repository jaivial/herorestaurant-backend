package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"preactvillacarmen/internal/httpx"
)

// Stock role permissions are stored sparsely: a missing row means "fall back to
// the role default", which is allow for root/admin and deny for everyone else.
// The catalogue endpoint materialises that fallback so the UI can show the
// effective answer without duplicating the rule.

func (s *Server) handleBOStockRolePermissionsGet(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	role := normalizeBORole(chi.URLParam(r, "slug"))
	if role == "" {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid role")
		return
	}
	rows, err := s.db.QueryContext(r.Context(),
		`SELECT permission_key,is_allowed FROM stock_role_permissions WHERE restaurant_id=? AND role_slug=?`,
		a.ActiveRestaurantID, role)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading stock permissions")
		return
	}
	defer rows.Close()
	overrides := map[string]bool{}
	for rows.Next() {
		var key string
		var allowed int
		if err = rows.Scan(&key, &allowed); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error reading stock permissions")
			return
		}
		overrides[key] = allowed != 0
	}
	if err = rows.Err(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error reading stock permissions")
		return
	}
	items := make([]map[string]any, 0, len(stockPermissionKeys))
	for _, key := range stockPermissionKeys {
		allowed, exists := overrides[key]
		if !exists {
			allowed = role == "root" || role == "admin"
		}
		items = append(items, map[string]any{"key": key, "allowed": allowed})
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
