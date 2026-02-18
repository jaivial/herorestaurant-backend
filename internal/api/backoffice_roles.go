package api

import (
	"database/sql"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"

	"preactvillacarmen/internal/httpx"
)

type boRoleCatalogItem struct {
	Slug        string   `json:"slug"`
	Label       string   `json:"label"`
	SortOrder   int      `json:"sortOrder"`
	Importance  int      `json:"importance"`
	IconKey     string   `json:"iconKey"`
	IsSystem    bool     `json:"isSystem"`
	Permissions []string `json:"permissions"`
}

type boRoleUserItem struct {
	ID             int    `json:"id"`
	Email          string `json:"email"`
	Name           string `json:"name"`
	Role           string `json:"role"`
	RoleImportance int    `json:"roleImportance"`
}

type boRoleCurrentUser struct {
	ID             int    `json:"id"`
	Role           string `json:"role"`
	RoleImportance int    `json:"roleImportance"`
}

type boPatchUserRoleRequest struct {
	Role string `json:"role"`
}

type boCreateRoleRequest struct {
	Slug        *string  `json:"slug"`
	Label       string   `json:"label"`
	Importance  *int     `json:"importance"`
	IconKey     *string  `json:"iconKey"`
	Permissions []string `json:"permissions"`
}

func defaultRoleLabel(role string) string {
	switch role {
	case "root":
		return "Root"
	case "admin":
		return "Admin"
	case "metre":
		return "Metre"
	case "jefe_cocina":
		return "Jefe de cocina"
	case "arrocero":
		return "Arrocero"
	case "pinche_cocina":
		return "Pinche de cocina"
	case "fregaplatos":
		return "Fregaplatos"
	case "ayudante_cocina":
		return "Ayudante de cocina"
	case "camarero":
		return "Camarero"
	case "responsable_sala":
		return "Responsable de sala"
	case "ayudante_camarero":
		return "Ayudante camarero"
	case "runner":
		return "Runner"
	case "barista":
		return "Barista"
	default:
		return role
	}
}

func defaultRoleIconKey(role string) string {
	switch role {
	case "root":
		return "crown"
	case "admin":
		return "shield-user"
	case "metre":
		return "clipboard-list"
	case "jefe_cocina":
		return "chef-hat"
	case "arrocero":
		return "flame"
	case "pinche_cocina":
		return "utensils-crossed"
	case "fregaplatos":
		return "droplets"
	case "ayudante_cocina":
		return "utensils"
	case "camarero":
		return "glass-water"
	case "responsable_sala":
		return "users-round"
	case "ayudante_camarero":
		return "user-round-plus"
	case "runner":
		return "route"
	case "barista":
		return "coffee"
	default:
		return "shield-user"
	}
}

func defaultRoleSortOrder(role string) int {
	switch role {
	case "root":
		return 0
	case "admin":
		return 10
	case "metre":
		return 20
	case "jefe_cocina":
		return 30
	case "arrocero":
		return 40
	case "pinche_cocina":
		return 50
	case "fregaplatos":
		return 60
	case "ayudante_cocina":
		return 70
	case "camarero":
		return 80
	case "responsable_sala":
		return 90
	case "ayudante_camarero":
		return 100
	case "runner":
		return 110
	case "barista":
		return 120
	default:
		return 1000
	}
}

func normalizeIconKey(raw string) string {
	icon := strings.ToLower(strings.TrimSpace(raw))
	if icon == "" {
		return "shield-user"
	}
	if len(icon) < 2 || len(icon) > 32 {
		return ""
	}
	for _, ch := range icon {
		if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '_' || ch == '-' {
			continue
		}
		return ""
	}
	return icon
}

func roleSlugFromLabel(label string) string {
	label = strings.ToLower(strings.TrimSpace(label))
	if label == "" {
		return ""
	}

	var b strings.Builder
	prevSep := false
	for _, ch := range label {
		if ch >= 'A' && ch <= 'Z' {
			ch = ch + ('a' - 'A')
		}
		if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') {
			b.WriteRune(ch)
			prevSep = false
			continue
		}
		if ch == ' ' || ch == '-' || ch == '_' || ch == '/' {
			if !prevSep && b.Len() > 0 {
				b.WriteByte('_')
				prevSep = true
			}
		}
	}
	out := strings.Trim(b.String(), "_")
	if !isRoleSlugValid(out) {
		return ""
	}
	return out
}

func normalizeSections(input []string) []string {
	if len(input) == 0 {
		return []string{}
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(input))
	for _, raw := range input {
		section := normalizeBOSection(raw)
		if section == "" || seen[section] {
			continue
		}
		seen[section] = true
		out = append(out, section)
	}
	sort.Strings(out)
	return out
}

func (s *Server) handleBORolesGet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	roleRows, err := s.db.QueryContext(r.Context(), `
		SELECT slug, label, sort_order, importance, COALESCE(icon_key, ''), is_system
		FROM bo_roles
		WHERE is_active = 1
		ORDER BY importance DESC, sort_order ASC, slug ASC
	`)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando roles")
		return
	}
	defer roleRows.Close()

	catalog := make([]boRoleCatalogItem, 0, 24)
	seen := map[string]bool{}
	for roleRows.Next() {
		var item boRoleCatalogItem
		var icon string
		var systemInt int
		if err := roleRows.Scan(&item.Slug, &item.Label, &item.SortOrder, &item.Importance, &icon, &systemInt); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo roles")
			return
		}
		item.Slug = normalizeBORole(item.Slug)
		if item.Slug == "" {
			continue
		}
		item.IconKey = normalizeIconKey(icon)
		if item.IconKey == "" {
			item.IconKey = defaultRoleIconKey(item.Slug)
		}
		item.IsSystem = systemInt != 0
		item.Permissions = []string{}
		seen[item.Slug] = true
		catalog = append(catalog, item)
	}

	// Ensure system roles are always listed even if the DB was partially seeded.
	for role := range defaultRolePermissions {
		if seen[role] {
			continue
		}
		importance := defaultRoleImportance[role]
		catalog = append(catalog, boRoleCatalogItem{
			Slug:        role,
			Label:       defaultRoleLabel(role),
			SortOrder:   defaultRoleSortOrder(role),
			Importance:  importance,
			IconKey:     defaultRoleIconKey(role),
			IsSystem:    true,
			Permissions: []string{},
		})
	}

	permRows, err := s.db.QueryContext(r.Context(), `
		SELECT role_slug, section_key
		FROM bo_role_permissions
		WHERE is_allowed = 1
	`)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando permisos")
		return
	}
	defer permRows.Close()

	permsByRole := map[string][]string{}
	for permRows.Next() {
		var roleSlug, section string
		if err := permRows.Scan(&roleSlug, &section); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo permisos")
			return
		}
		roleSlug = normalizeBORole(roleSlug)
		section = normalizeBOSection(section)
		if roleSlug == "" || section == "" {
			continue
		}
		permsByRole[roleSlug] = append(permsByRole[roleSlug], section)
	}
	for i := range catalog {
		if perms, ok := permsByRole[catalog[i].Slug]; ok {
			sort.Strings(perms)
			catalog[i].Permissions = perms
			continue
		}
		fallback := make([]string, 0, 8)
		for section, allowed := range defaultRolePermissions[catalog[i].Slug] {
			if allowed {
				fallback = append(fallback, section)
			}
		}
		sort.Strings(fallback)
		catalog[i].Permissions = fallback
	}

	rows, err := s.db.QueryContext(r.Context(), `
		SELECT
			u.id,
			u.email,
			u.name,
			u.is_superadmin,
			ur.role AS role_slug
		FROM bo_users u
		LEFT JOIN bo_user_restaurants ur
			ON ur.user_id = u.id AND ur.restaurant_id = ?
		LEFT JOIN restaurant_members m
			ON m.restaurant_id = ?
			AND m.is_active = 1
			AND (
				m.bo_user_id = u.id
				OR (
					m.bo_user_id IS NULL
					AND m.email IS NOT NULL
					AND LOWER(TRIM(m.email)) = LOWER(TRIM(u.email))
				)
			)
		WHERE ur.user_id IS NOT NULL OR m.id IS NOT NULL OR u.is_superadmin = 1
		ORDER BY u.name ASC, u.email ASC, u.id ASC
	`, a.ActiveRestaurantID, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando usuarios")
		return
	}
	defer rows.Close()

	users := make([]boRoleUserItem, 0, 24)
	for rows.Next() {
		var (
			uu      boRoleUserItem
			isSuper int
			rawRole sql.NullString
		)
		if err := rows.Scan(&uu.ID, &uu.Email, &uu.Name, &isSuper, &rawRole); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo usuarios")
			return
		}
		if isSuper != 0 {
			uu.Role = "root"
		} else {
			if rawRole.Valid {
				uu.Role = normalizeBORole(rawRole.String)
				if uu.Role == "" {
					uu.Role = "admin"
				}
			} else {
				uu.Role = ""
			}
		}
		imp, err := s.roleImportance(r.Context(), uu.Role)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo importancia de usuario")
			return
		}
		uu.RoleImportance = imp
		users = append(users, uu)
	}

	actorImportance, err := s.roleImportance(r.Context(), a.Role)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo importancia de rol")
		return
	}

	sort.Slice(catalog, func(i, j int) bool {
		if catalog[i].Importance != catalog[j].Importance {
			return catalog[i].Importance > catalog[j].Importance
		}
		if catalog[i].SortOrder != catalog[j].SortOrder {
			return catalog[i].SortOrder < catalog[j].SortOrder
		}
		return catalog[i].Slug < catalog[j].Slug
	})

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"roles":   catalog,
		"users":   users,
		"currentUser": boRoleCurrentUser{
			ID:             a.User.ID,
			Role:           a.Role,
			RoleImportance: actorImportance,
		},
	})
}

func (s *Server) handleBORoleCreate(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	actorImportance, err := s.roleImportance(r.Context(), a.Role)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error validando rol de sesión")
		return
	}
	if actorImportance < 90 {
		httpx.WriteError(w, http.StatusForbidden, "Forbidden")
		return
	}

	var req boCreateRoleRequest
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Invalid JSON",
		})
		return
	}

	label := strings.TrimSpace(req.Label)
	if len(label) < 2 || len(label) > 64 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Label invalido (2-64 caracteres)",
		})
		return
	}

	roleSlug := ""
	if req.Slug != nil {
		roleSlug = normalizeBORole(*req.Slug)
	}
	if roleSlug == "" {
		roleSlug = roleSlugFromLabel(label)
	}
	if roleSlug == "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Slug invalido",
		})
		return
	}
	if _, system := defaultRolePermissions[roleSlug]; system {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Ese slug está reservado",
		})
		return
	}

	exists, err := s.roleExists(r.Context(), roleSlug)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error verificando rol")
		return
	}
	if exists {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Ya existe un rol con ese slug",
		})
		return
	}

	importance := 50
	if req.Importance != nil {
		importance = *req.Importance
	}
	if importance < 0 || importance > 100 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Importance debe estar entre 0 y 100",
		})
		return
	}
	if importance >= actorImportance {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "No puedes crear un rol con importancia igual o superior a la tuya",
		})
		return
	}

	iconKey := "shield-user"
	if req.IconKey != nil {
		iconKey = normalizeIconKey(*req.IconKey)
		if iconKey == "" {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"message": "IconKey invalido",
			})
			return
		}
	}

	permissions := normalizeSections(req.Permissions)
	if len(permissions) == 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "El rol debe tener al menos un permiso de sección",
		})
		return
	}

	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error iniciando transacción")
		return
	}
	defer func() { _ = tx.Rollback() }()

	sortOrder := 1000
	if err := tx.QueryRowContext(r.Context(), "SELECT COALESCE(MAX(sort_order), 0) + 10 FROM bo_roles").Scan(&sortOrder); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error calculando orden de rol")
		return
	}

	if _, err := tx.ExecContext(r.Context(), `
		INSERT INTO bo_roles (slug, label, sort_order, is_active, importance, icon_key, is_system)
		VALUES (?, ?, ?, 1, ?, ?, 0)
	`, roleSlug, label, sortOrder, importance, iconKey); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error creando rol")
		return
	}

	for _, section := range permissions {
		if _, err := tx.ExecContext(r.Context(), `
			INSERT INTO bo_role_permissions (role_slug, section_key, is_allowed)
			VALUES (?, ?, 1)
			ON DUPLICATE KEY UPDATE is_allowed = VALUES(is_allowed)
		`, roleSlug, section); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error creando permisos de rol")
			return
		}
	}

	if err := tx.Commit(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error finalizando transacción")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"role": boRoleCatalogItem{
			Slug:        roleSlug,
			Label:       label,
			SortOrder:   sortOrder,
			Importance:  importance,
			IconKey:     iconKey,
			IsSystem:    false,
			Permissions: permissions,
		},
	})
}

func (s *Server) handleBOMemberEnsureUser(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	actorImportance, err := s.roleImportance(r.Context(), a.Role)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error validando rol de sesión")
		return
	}
	if actorImportance < 90 {
		httpx.WriteError(w, http.StatusForbidden, "Forbidden")
		return
	}

	memberID, err := parseBOIDParam(r, "id")
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "id invalido",
		})
		return
	}

	var (
		memberUserID sql.NullInt64
		firstName    string
		lastName     string
		emailRaw     sql.NullString
	)
	if err := s.db.QueryRowContext(r.Context(), `
		SELECT
			bo_user_id,
			first_name,
			last_name,
			email
		FROM restaurant_members
		WHERE id = ? AND restaurant_id = ? AND is_active = 1
		LIMIT 1
	`, memberID, a.ActiveRestaurantID).Scan(&memberUserID, &firstName, &lastName, &emailRaw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
				"success": false,
				"message": "Miembro no encontrado",
			})
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo miembro")
		return
	}

	email := strings.ToLower(strings.TrimSpace(emailRaw.String))
	if email == "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "El miembro no tiene email para crear usuario backoffice",
		})
		return
	}

	userName := strings.TrimSpace(firstName + " " + lastName)
	if userName == "" {
		userName = email
	}

	userID := 0
	userEmail := email
	created := false

	if memberUserID.Valid && memberUserID.Int64 > 0 {
		userID = int(memberUserID.Int64)
		var existingName string
		var existingEmail string
		err := s.db.QueryRowContext(r.Context(), `
			SELECT name, email
			FROM bo_users
			WHERE id = ?
			LIMIT 1
		`, userID).Scan(&existingName, &existingEmail)
		if err == nil {
			userName = strings.TrimSpace(existingName)
			userEmail = strings.TrimSpace(existingEmail)
			if userName == "" {
				userName = email
			}
			if userEmail == "" {
				userEmail = email
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			httpx.WriteError(w, http.StatusInternalServerError, "Error consultando usuario")
			return
		} else {
			userID = 0
		}
	}

	if userID == 0 {
		err := s.db.QueryRowContext(r.Context(), `
			SELECT id, name, email
			FROM bo_users
			WHERE LOWER(TRIM(email)) = LOWER(TRIM(?))
			LIMIT 1
		`, email).Scan(&userID, &userName, &userEmail)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			httpx.WriteError(w, http.StatusInternalServerError, "Error consultando usuario")
			return
		}
		if errors.Is(err, sql.ErrNoRows) {
			tempPassword, _, tokenErr := newBOSessionToken()
			if tokenErr != nil {
				httpx.WriteError(w, http.StatusInternalServerError, "Error creando credenciales de usuario")
				return
			}
			hash, hashErr := bcrypt.GenerateFromPassword([]byte(tempPassword), bcrypt.DefaultCost)
			if hashErr != nil {
				httpx.WriteError(w, http.StatusInternalServerError, "Error creando credenciales de usuario")
				return
			}

			res, insertErr := s.db.ExecContext(r.Context(), `
				INSERT INTO bo_users (email, name, password_hash, is_superadmin)
				VALUES (?, ?, ?, 0)
			`, email, userName, string(hash))
			if insertErr != nil {
				// Handle race on unique email by resolving again.
				retryErr := s.db.QueryRowContext(r.Context(), `
					SELECT id, name, email
					FROM bo_users
					WHERE LOWER(TRIM(email)) = LOWER(TRIM(?))
					LIMIT 1
				`, email).Scan(&userID, &userName, &userEmail)
				if retryErr != nil {
					httpx.WriteError(w, http.StatusInternalServerError, "Error creando usuario")
					return
				}
			} else {
				created = true
				lastID, _ := res.LastInsertId()
				userID = int(lastID)
			}
		}
	}

	if userID <= 0 {
		httpx.WriteError(w, http.StatusInternalServerError, "Error resolviendo usuario")
		return
	}

	if _, err := s.db.ExecContext(r.Context(), `
		UPDATE restaurant_members
		SET bo_user_id = ?
		WHERE id = ? AND restaurant_id = ? AND is_active = 1
	`, userID, memberID, a.ActiveRestaurantID); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "No se pudo vincular el usuario al miembro",
		})
		return
	}

	if strings.TrimSpace(userName) == "" {
		userName = email
	}
	if strings.TrimSpace(userEmail) == "" {
		userEmail = email
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"user": map[string]any{
			"id":      userID,
			"email":   strings.TrimSpace(userEmail),
			"name":    strings.TrimSpace(userName),
			"created": created,
		},
		"member": map[string]any{
			"id":       memberID,
			"boUserId": userID,
		},
	})
}

func (s *Server) handleBOUserRolePatch(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	actorImportance, err := s.roleImportance(r.Context(), a.Role)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error validando rol de sesión")
		return
	}
	if actorImportance < 90 {
		httpx.WriteError(w, http.StatusForbidden, "Forbidden")
		return
	}

	rawID := strings.TrimSpace(chi.URLParam(r, "id"))
	userID, err := strconv.Atoi(rawID)
	if err != nil || userID <= 0 {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "id invalido",
		})
		return
	}

	if userID == a.User.ID {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "No puedes cambiar tu propio rol",
		})
		return
	}

	var req boPatchUserRoleRequest
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Invalid JSON",
		})
		return
	}

	roleSlug := normalizeBORole(req.Role)
	exists, err := s.roleExists(r.Context(), roleSlug)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error verificando rol")
		return
	}
	if roleSlug == "" || !exists {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Rol invalido",
		})
		return
	}

	newImportance, err := s.roleImportance(r.Context(), roleSlug)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo importancia del nuevo rol")
		return
	}
	if actorImportance <= newImportance {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "No puedes asignar un rol con importancia igual o superior a la tuya",
		})
		return
	}

	var (
		targetIsSuper      int
		targetRawRole      sql.NullString
		targetRestaurantID sql.NullInt64
	)
	err = s.db.QueryRowContext(r.Context(), `
		SELECT
			u.is_superadmin,
			ur.role AS role_slug,
			ur.user_id
		FROM bo_users u
		LEFT JOIN bo_user_restaurants ur
			ON ur.user_id = u.id AND ur.restaurant_id = ?
		LEFT JOIN restaurant_members m
			ON m.restaurant_id = ?
			AND m.is_active = 1
			AND (
				m.bo_user_id = u.id
				OR (
					m.bo_user_id IS NULL
					AND m.email IS NOT NULL
					AND LOWER(TRIM(m.email)) = LOWER(TRIM(u.email))
				)
			)
		WHERE
			u.id = ?
			AND (ur.user_id IS NOT NULL OR m.id IS NOT NULL OR u.is_superadmin = 1)
		LIMIT 1
	`, a.ActiveRestaurantID, a.ActiveRestaurantID, userID).Scan(&targetIsSuper, &targetRawRole, &targetRestaurantID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
				"success": false,
				"message": "Usuario no encontrado",
			})
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando usuario")
		return
	}

	targetRole := ""
	if targetIsSuper != 0 {
		targetRole = "root"
	} else if targetRestaurantID.Valid {
		targetRole = normalizeBORole(targetRawRole.String)
		if targetRole == "" {
			targetRole = "admin"
		}
	}

	targetImportance, err := s.roleImportance(r.Context(), targetRole)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo importancia del usuario objetivo")
		return
	}
	if actorImportance <= targetImportance {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Tu rol debe tener una importancia superior al del usuario objetivo",
		})
		return
	}

	_, err = s.db.ExecContext(r.Context(), `
		INSERT INTO bo_user_restaurants (user_id, restaurant_id, role)
		VALUES (?, ?, ?)
		ON DUPLICATE KEY UPDATE
			role = VALUES(role)
	`, userID, a.ActiveRestaurantID, roleSlug)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error actualizando rol")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"user": map[string]any{
			"id":             userID,
			"role":           roleSlug,
			"roleImportance": newImportance,
		},
	})
}
