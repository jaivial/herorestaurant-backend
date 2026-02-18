package api

import (
	"context"
	"database/sql"
	"net/http"
	"sort"
	"strings"

	"preactvillacarmen/internal/httpx"
)

const (
	boSectionReservas = "reservas"
	boSectionMenus    = "menus"
	boSectionAjustes  = "ajustes"
	boSectionMiembros = "miembros"
	boSectionFichaje  = "fichaje"
	boSectionHorarios = "horarios"
	boSectionFacturas = "facturas"
)

var defaultRolePermissions = map[string]map[string]bool{
	"root": {
		boSectionReservas: true,
		boSectionMenus:    true,
		boSectionAjustes:  true,
		boSectionMiembros: true,
		boSectionFichaje:  true,
		boSectionHorarios: true,
		boSectionFacturas: true,
	},
	"admin": {
		boSectionReservas: true,
		boSectionMenus:    true,
		boSectionAjustes:  true,
		boSectionMiembros: true,
		boSectionFichaje:  true,
		boSectionHorarios: true,
		boSectionFacturas: true,
	},
	"metre": {
		boSectionReservas: true,
		boSectionMenus:    true,
		boSectionFichaje:  true,
		boSectionFacturas: true,
	},
	"jefe_cocina": {
		boSectionReservas: true,
		boSectionMenus:    true,
		boSectionFichaje:  true,
	},
	"arrocero": {
		boSectionFichaje: true,
	},
	"pinche_cocina": {
		boSectionFichaje: true,
	},
	"fregaplatos": {
		boSectionFichaje: true,
	},
	"ayudante_cocina": {
		boSectionFichaje: true,
	},
	"camarero": {
		boSectionFichaje: true,
	},
	"responsable_sala": {
		boSectionFichaje: true,
	},
	"ayudante_camarero": {
		boSectionFichaje: true,
	},
	"runner": {
		boSectionFichaje: true,
	},
	"barista": {
		boSectionFichaje: true,
	},
}

var defaultRoleImportance = map[string]int{
	"root":              100,
	"admin":             90,
	"metre":             75,
	"jefe_cocina":       74,
	"responsable_sala":  65,
	"arrocero":          60,
	"camarero":          58,
	"barista":           55,
	"runner":            50,
	"ayudante_camarero": 45,
	"ayudante_cocina":   40,
	"pinche_cocina":     35,
	"fregaplatos":       30,
}

func isRoleSlugValid(slug string) bool {
	if len(slug) < 2 || len(slug) > 32 {
		return false
	}
	for _, ch := range slug {
		if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '_' {
			continue
		}
		return false
	}
	return true
}

func normalizeBORole(role string) string {
	r := strings.ToLower(strings.TrimSpace(role))
	if r == "owner" {
		return "admin"
	}
	if !isRoleSlugValid(r) {
		return ""
	}
	if _, ok := defaultRolePermissions[r]; ok {
		return r
	}
	return r
}

func normalizeBOSection(section string) string {
	switch strings.ToLower(strings.TrimSpace(section)) {
	case boSectionReservas:
		return boSectionReservas
	case boSectionMenus:
		return boSectionMenus
	case boSectionAjustes:
		return boSectionAjustes
	case boSectionMiembros:
		return boSectionMiembros
	case boSectionFichaje:
		return boSectionFichaje
	case boSectionHorarios:
		return boSectionHorarios
	default:
		return ""
	}
}

func (s *Server) roleCanAccessSection(ctx context.Context, role, section string) (bool, error) {
	role = normalizeBORole(role)
	section = normalizeBOSection(section)
	if role == "" || section == "" {
		return false, nil
	}

	var allowed int
	err := s.db.QueryRowContext(ctx, `
		SELECT is_allowed
		FROM bo_role_permissions
		WHERE role_slug = ? AND section_key = ?
		LIMIT 1
	`, role, section).Scan(&allowed)
	if err == nil {
		return allowed != 0, nil
	}
	if err != sql.ErrNoRows {
		return false, err
	}

	perms := defaultRolePermissions[role]
	return perms != nil && perms[section], nil
}

func (s *Server) roleImportance(ctx context.Context, role string) (int, error) {
	role = normalizeBORole(role)
	if role == "" {
		return 0, nil
	}

	var importance int
	err := s.db.QueryRowContext(ctx, `
		SELECT importance
		FROM bo_roles
		WHERE slug = ? AND is_active = 1
		LIMIT 1
	`, role).Scan(&importance)
	if err == nil {
		if importance < 0 {
			return 0, nil
		}
		if importance > 100 {
			return 100, nil
		}
		return importance, nil
	}
	if err != sql.ErrNoRows {
		return 0, err
	}

	if v, ok := defaultRoleImportance[role]; ok {
		return v, nil
	}
	return 0, nil
}

func (s *Server) roleExists(ctx context.Context, role string) (bool, error) {
	role = normalizeBORole(role)
	if role == "" {
		return false, nil
	}

	var tmp int
	err := s.db.QueryRowContext(ctx, `
		SELECT 1
		FROM bo_roles
		WHERE slug = ? AND is_active = 1
		LIMIT 1
	`, role).Scan(&tmp)
	if err == nil {
		return true, nil
	}
	if err == sql.ErrNoRows {
		_, ok := defaultRolePermissions[role]
		return ok, nil
	}
	return false, err
}

func (s *Server) roleSections(ctx context.Context, role string) ([]string, error) {
	role = normalizeBORole(role)
	if role == "" {
		return []string{}, nil
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT section_key
		FROM bo_role_permissions
		WHERE role_slug = ? AND is_allowed = 1
		ORDER BY section_key ASC
	`, role)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]string, 0, 8)
	for rows.Next() {
		var section string
		if err := rows.Scan(&section); err != nil {
			return nil, err
		}
		section = normalizeBOSection(section)
		if section == "" {
			continue
		}
		out = append(out, section)
	}

	// Always include default permissions as fallback/base.
	// This ensures all standard sections are available even if DB is incomplete.
	perms := defaultRolePermissions[role]
	if len(perms) > 0 {
		seen := make(map[string]bool)
		for _, s := range out {
			seen[s] = true
		}
		for section, allowed := range perms {
			if allowed && !seen[section] {
				out = append(out, section)
			}
		}
	}

	sort.Strings(out)
	return out, nil
}

func (s *Server) requireBORoleImportanceAtLeast(minImportance int) func(http.Handler) http.Handler {
	if minImportance < 0 {
		minImportance = 0
	}
	if minImportance > 100 {
		minImportance = 100
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			a, ok := boAuthFromContext(r.Context())
			if !ok {
				httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
				return
			}

			importance, err := s.roleImportance(r.Context(), a.Role)
			if err != nil {
				httpx.WriteError(w, http.StatusInternalServerError, "Error validating role importance")
				return
			}
			if importance < minImportance {
				httpx.WriteError(w, http.StatusForbidden, "Forbidden")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func (s *Server) requireBOSection(section string) func(http.Handler) http.Handler {
	normalized := normalizeBOSection(section)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if normalized == "" {
				httpx.WriteError(w, http.StatusInternalServerError, "Invalid RBAC section")
				return
			}

			a, ok := boAuthFromContext(r.Context())
			if !ok {
				httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
				return
			}

			allowed, err := s.roleCanAccessSection(r.Context(), a.Role, normalized)
			if err != nil {
				httpx.WriteError(w, http.StatusInternalServerError, "Error validating permissions")
				return
			}
			if !allowed {
				httpx.WriteError(w, http.StatusForbidden, "Forbidden")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
