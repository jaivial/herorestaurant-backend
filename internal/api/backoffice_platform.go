package api

// Platform (superadmin) administration: cross-restaurant management.
//
// All endpoints require a valid backoffice session whose user has
// is_superadmin=1 (role "root"). The gate is requireBOSuperadmin, registered
// in server.go. None of these endpoints are scoped to the session's
// active_restaurant_id — they operate across all tenants.
//
// Covers:
//   - Restaurant CRUD + suspend/activate/revoke
//   - Platform users (bo_users) management: list, create, reset password,
//     toggle superadmin, deactivate
//   - Subscriptions / recurring features per restaurant
//   - UAZAPI WhatsApp instances across all restaurants
//   - Stripe payments list + refund
//   - Domain registrations across all restaurants
//   - Platform dashboard metrics

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"

	"preactvillacarmen/internal/httpx"
	"preactvillacarmen/internal/integrations"
)

func newPlatformStripeClient(secretKey string) *integrations.PlatformStripeClient {
	return integrations.NewPlatformStripeClient(secretKey)
}

// requireBOSuperadmin gates platform endpoints to is_superadmin users only.
func (s *Server) requireBOSuperadmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a, ok := boAuthFromContext(r.Context())
		if !ok {
			httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
			return
		}
		if !a.User.isSuperadmin {
			httpx.WriteError(w, http.StatusForbidden, "Forbidden")
			return
		}
		if !appCapabilityAllowed(boCapabilityPlataforma, a.User.AppVersion) {
			httpx.WriteError(w, http.StatusForbidden, "Forbidden")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ---------- Restaurant management ----------

type platformRestaurant struct {
	ID             int            `json:"id"`
	Slug           string         `json:"slug"`
	Name           string         `json:"name"`
	Avatar         sql.NullString `json:"-"`
	AvatarStr      string         `json:"avatar"`
	CIF            sql.NullString `json:"-"`
	CIFStr         string         `json:"cif"`
	ContactPhone   sql.NullString `json:"-"`
	ContactPhoneS  string         `json:"contactPhone"`
	ContactEmail   sql.NullString `json:"-"`
	ContactEmailS  string         `json:"contactEmail"`
	Location       sql.NullString `json:"-"`
	LocationStr    string         `json:"location"`
	WebsiteURL     sql.NullString `json:"-"`
	WebsiteURLStr  string         `json:"websiteUrl"`
	IsActive       bool           `json:"isActive"`
	CreatedAt      string         `json:"createdAt"`
	UserCount      int            `json:"userCount"`
	MemberCount    int            `json:"memberCount"`
	BookingCount   int            `json:"bookingCount"`
	DomainCount    int            `json:"domainCount"`
	WhatsAppStatus string         `json:"whatsappStatus"`
}

func (s *Server) handlePlatformRestaurantsList(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT
			r.id, r.slug, r.name,
			r.avatar, r.cif, r.contact_phone, r.contact_email,
			r.location, r.website_url,
			1 AS is_active,
			DATE_FORMAT(r.created_at, '%Y-%m-%dT%H:%i:%sZ') AS created_at,
			COALESCE(u.cnt, 0) AS user_count,
			COALESCE(m.cnt, 0) AS member_count,
			COALESCE(b.cnt, 0) AS booking_count,
			COALESCE(d.cnt, 0) AS domain_count,
			COALESCE(wa.status, '') AS wa_status
		FROM restaurants r
		LEFT JOIN (SELECT restaurant_id, COUNT(*) AS cnt FROM bo_user_restaurants GROUP BY restaurant_id) u ON u.restaurant_id = r.id
		LEFT JOIN (SELECT restaurant_id, COUNT(*) AS cnt FROM restaurant_members WHERE is_active = 1 GROUP BY restaurant_id) m ON m.restaurant_id = r.id
		LEFT JOIN (SELECT restaurant_id, COUNT(*) AS cnt FROM bookings GROUP BY restaurant_id) b ON b.restaurant_id = r.id
		LEFT JOIN (SELECT restaurant_id, COUNT(*) AS cnt FROM restaurant_domains GROUP BY restaurant_id) d ON d.restaurant_id = r.id
		LEFT JOIN (SELECT restaurant_id, status FROM restaurant_uazapi_instances) wa ON wa.restaurant_id = r.id
		ORDER BY r.id ASC
	`)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo restaurantes")
		return
	}
	defer rows.Close()

	var out []platformRestaurant
	for rows.Next() {
		var p platformRestaurant
		if err := rows.Scan(
			&p.ID, &p.Slug, &p.Name,
			&p.Avatar, &p.CIF, &p.ContactPhone, &p.ContactEmail,
			&p.Location, &p.WebsiteURL,
			&p.IsActive,
			&p.CreatedAt,
			&p.UserCount, &p.MemberCount, &p.BookingCount, &p.DomainCount,
			&p.WhatsAppStatus,
		); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo filas de restaurantes")
			return
		}
		p.AvatarStr = nullStr(p.Avatar)
		p.CIFStr = nullStr(p.CIF)
		p.ContactPhoneS = nullStr(p.ContactPhone)
		p.ContactEmailS = nullStr(p.ContactEmail)
		p.LocationStr = nullStr(p.Location)
		p.WebsiteURLStr = nullStr(p.WebsiteURL)
		out = append(out, p)
	}

	if out == nil {
		out = []platformRestaurant{}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":     true,
		"restaurants": out,
	})
}

func nullStr(ns sql.NullString) string {
	if ns.Valid {
		return ns.String
	}
	return ""
}

type platformRestaurantCreateRequest struct {
	Slug         string  `json:"slug"`
	Name         string  `json:"name"`
	CIF          *string `json:"cif"`
	ContactPhone *string `json:"contactPhone"`
	ContactEmail *string `json:"contactEmail"`
	Location     *string `json:"location"`
	WebsiteURL   *string `json:"websiteUrl"`
}

func (s *Server) handlePlatformRestaurantCreate(w http.ResponseWriter, r *http.Request) {
	var req platformRestaurantCreateRequest
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "JSON invalido"})
		return
	}
	slug := strings.ToLower(strings.TrimSpace(req.Slug))
	name := strings.TrimSpace(req.Name)
	if slug == "" || name == "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Slug y nombre son requeridos"})
		return
	}

	res, err := s.db.ExecContext(r.Context(), `
		INSERT INTO restaurants (slug, name, cif, contact_phone, contact_email, location, website_url)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`,
		slug, name,
		nullableStr(req.CIF), nullableStr(req.ContactPhone), nullableStr(req.ContactEmail),
		nullableStr(req.Location), nullableStr(req.WebsiteURL),
	)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Error creando restaurante: " + err.Error()})
		return
	}
	id, _ := res.LastInsertId()

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":      true,
		"restaurantId": id,
	})
}

func nullableStr(s *string) any {
	if s == nil {
		return nil
	}
	v := strings.TrimSpace(*s)
	if v == "" {
		return nil
	}
	return v
}

type platformRestaurantPatchRequest struct {
	Name         *string `json:"name"`
	CIF          *string `json:"cif"`
	ContactPhone *string `json:"contactPhone"`
	ContactEmail *string `json:"contactEmail"`
	Location     *string `json:"location"`
	WebsiteURL   *string `json:"websiteUrl"`
}

func (s *Server) handlePlatformRestaurantPatch(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	restaurantID, err := parseIntParam(idStr)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "ID invalido")
		return
	}

	var req platformRestaurantPatchRequest
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "JSON invalido"})
		return
	}

	sets := []string{}
	args := []any{}
	if req.Name != nil {
		sets = append(sets, "name = ?")
		args = append(args, strings.TrimSpace(*req.Name))
	}
	if req.CIF != nil {
		sets = append(sets, "cif = ?")
		args = append(args, nullableStr(req.CIF))
	}
	if req.ContactPhone != nil {
		sets = append(sets, "contact_phone = ?")
		args = append(args, nullableStr(req.ContactPhone))
	}
	if req.ContactEmail != nil {
		sets = append(sets, "contact_email = ?")
		args = append(args, nullableStr(req.ContactEmail))
	}
	if req.Location != nil {
		sets = append(sets, "location = ?")
		args = append(args, nullableStr(req.Location))
	}
	if req.WebsiteURL != nil {
		sets = append(sets, "website_url = ?")
		args = append(args, nullableStr(req.WebsiteURL))
	}

	if len(sets) == 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Sin cambios"})
		return
	}

	args = append(args, restaurantID)
	query := "UPDATE restaurants SET " + strings.Join(sets, ", ") + " WHERE id = ?"
	if _, err := s.db.ExecContext(r.Context(), query, args...); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Error actualizando: " + err.Error()})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) handlePlatformRestaurantDeactivate(w http.ResponseWriter, r *http.Request) {
	restaurantID, err := parseIntParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "ID invalido")
		return
	}
	ctx := r.Context()

	// Deactivate all sessions for this restaurant's users.
	_, _ = s.db.ExecContext(ctx, `
		DELETE s FROM bo_sessions s
		JOIN bo_user_restaurants ur ON ur.user_id = s.user_id AND ur.restaurant_id = s.active_restaurant_id
		WHERE s.active_restaurant_id = ?
	`, restaurantID)

	// Deactivate all recurring features.
	_, _ = s.db.ExecContext(ctx, "UPDATE recurring_invoices SET is_active = 0 WHERE restaurant_id = ?", restaurantID)

	// Suspend WhatsApp instance.
	_ = s.suspendRestaurantUAZAPIInstance(ctx, restaurantID)

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": "Restaurante suspendido: sesiones cerradas, suscripciones desactivadas, WhatsApp desconectado",
	})
}

func (s *Server) handlePlatformRestaurantActivate(w http.ResponseWriter, r *http.Request) {
	restaurantID, err := parseIntParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "ID invalido")
		return
	}
	_, err = s.db.ExecContext(r.Context(), "UPDATE recurring_invoices SET is_active = 1 WHERE restaurant_id = ?", restaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error reactivando")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Restaurante reactivado"})
}

// ---------- Platform users (bo_users) ----------

type platformUser struct {
	ID                 int                        `json:"id"`
	Email              string                     `json:"email"`
	Username           sql.NullString             `json:"-"`
	UsernameStr        string                     `json:"username"`
	Name               string                     `json:"name"`
	IsSuperadmin       bool                       `json:"isSuperadmin"`
	MustChangePass     bool                       `json:"mustChangePassword"`
	CreatedAt          string                     `json:"createdAt"`
	Restaurants        []userRestaurantAssignment `json:"restaurants"`
	ActiveSessionCount int                        `json:"activeSessionCount"`
}

type userRestaurantAssignment struct {
	RestaurantID   int    `json:"restaurantId"`
	RestaurantName string `json:"restaurantName"`
	Role           string `json:"role"`
}

func (s *Server) handlePlatformUsersList(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT
			u.id, u.email, u.username, u.name, u.is_superadmin, u.must_change_password,
			DATE_FORMAT(u.created_at, '%Y-%m-%dT%H:%i:%sZ') AS created_at,
			(SELECT COUNT(*) FROM bo_sessions s WHERE s.user_id = u.id AND s.expires_at > NOW()) AS active_sessions
		FROM bo_users u
		ORDER BY u.is_superadmin DESC, u.id ASC
	`)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo usuarios")
		return
	}
	defer rows.Close()

	type userRow struct {
		platformUser
	}

	users := map[int]*platformUser{}
	var orderedIDs []int
	for rows.Next() {
		var (
			id             int
			email          string
			username       sql.NullString
			name           string
			isSuper        int
			mustChange     int
			createdAt      string
			activeSessions int
		)
		if err := rows.Scan(&id, &email, &username, &name, &isSuper, &mustChange, &createdAt, &activeSessions); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo filas de usuarios")
			return
		}
		users[id] = &platformUser{
			ID:                 id,
			Email:              email,
			Username:           username,
			UsernameStr:        nullStr(username),
			Name:               name,
			IsSuperadmin:       isSuper != 0,
			MustChangePass:     mustChange != 0,
			CreatedAt:          createdAt,
			ActiveSessionCount: activeSessions,
			Restaurants:        []userRestaurantAssignment{},
		}
		orderedIDs = append(orderedIDs, id)
	}

	// Load restaurant assignments.
	if len(orderedIDs) > 0 {
		rRows, err := s.db.QueryContext(r.Context(), `
			SELECT ur.user_id, ur.restaurant_id, r.name, ur.role
			FROM bo_user_restaurants ur
			JOIN restaurants r ON r.id = ur.restaurant_id
		`)
		if err == nil {
			for rRows.Next() {
				var userID, restID int
				var restName, role string
				if err := rRows.Scan(&userID, &restID, &restName, &role); err == nil {
					if u, ok := users[userID]; ok {
						u.Restaurants = append(u.Restaurants, userRestaurantAssignment{
							RestaurantID:   restID,
							RestaurantName: restName,
							Role:           role,
						})
					}
				}
			}
			rRows.Close()
		}
	}

	out := make([]platformUser, 0, len(orderedIDs))
	for _, id := range orderedIDs {
		out = append(out, *users[id])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"users":   out,
	})
}

type platformUserCreateRequest struct {
	Email        string  `json:"email"`
	Name         string  `json:"name"`
	Password     string  `json:"password"`
	IsSuperadmin bool    `json:"isSuperadmin"`
	RestaurantID *int    `json:"restaurantId"`
	Role         *string `json:"role"`
}

func (s *Server) handlePlatformUserCreate(w http.ResponseWriter, r *http.Request) {
	var req platformUserCreateRequest
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "JSON invalido"})
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	name := strings.TrimSpace(req.Name)
	password := strings.TrimSpace(req.Password)
	if email == "" || name == "" || password == "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Email, nombre y password son requeridos"})
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error generando hash")
		return
	}

	superVal := 0
	if req.IsSuperadmin {
		superVal = 1
	}

	res, err := s.db.ExecContext(r.Context(), `
		INSERT INTO bo_users (email, name, password_hash, is_superadmin, must_change_password)
		VALUES (?, ?, ?, ?, 1)
	`, email, name, string(hash), superVal)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Error creando usuario: " + err.Error()})
		return
	}
	userID, _ := res.LastInsertId()

	// Assign to restaurant if provided.
	if req.RestaurantID != nil && *req.RestaurantID > 0 {
		role := "admin"
		if req.Role != nil && strings.TrimSpace(*req.Role) != "" {
			role = strings.TrimSpace(*req.Role)
		}
		_, _ = s.db.ExecContext(r.Context(), `
			INSERT IGNORE INTO bo_user_restaurants (user_id, restaurant_id, role)
			VALUES (?, ?, ?)
		`, userID, *req.RestaurantID, role)
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"userId":  userID,
		"message": "Usuario creado. Debera cambiar su password en el primer login.",
	})
}

type platformUserPatchRequest struct {
	Name           *string `json:"name"`
	IsSuperadmin   *bool   `json:"isSuperadmin"`
	MustChangePass *bool   `json:"mustChangePassword"`
}

func (s *Server) handlePlatformUserPatch(w http.ResponseWriter, r *http.Request) {
	userID, err := parseIntParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "ID invalido")
		return
	}

	var req platformUserPatchRequest
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "JSON invalido"})
		return
	}

	sets := []string{}
	args := []any{}
	if req.Name != nil {
		sets = append(sets, "name = ?")
		args = append(args, strings.TrimSpace(*req.Name))
	}
	if req.IsSuperadmin != nil {
		v := 0
		if *req.IsSuperadmin {
			v = 1
		}
		sets = append(sets, "is_superadmin = ?")
		args = append(args, v)
	}
	if req.MustChangePass != nil {
		v := 0
		if *req.MustChangePass {
			v = 1
		}
		sets = append(sets, "must_change_password = ?")
		args = append(args, v)
	}

	if len(sets) > 0 {
		args = append(args, userID)
		query := "UPDATE bo_users SET " + strings.Join(sets, ", ") + " WHERE id = ?"
		if _, err := s.db.ExecContext(r.Context(), query, args...); err != nil {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Error: " + err.Error()})
			return
		}
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

type platformUserPasswordResetRequest struct {
	NewPassword string `json:"newPassword"`
}

func (s *Server) handlePlatformUserPasswordReset(w http.ResponseWriter, r *http.Request) {
	userID, err := parseIntParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "ID invalido")
		return
	}
	var req platformUserPasswordResetRequest
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "JSON invalido"})
		return
	}
	password := strings.TrimSpace(req.NewPassword)
	if len(password) < 6 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Password debe tener al menos 6 caracteres"})
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error generando hash")
		return
	}

	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error iniciando transaccion")
		return
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(r.Context(), "UPDATE bo_users SET password_hash = ?, must_change_password = 1 WHERE id = ?", string(hash), userID); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Error actualizando password"})
		return
	}
	// Kill all sessions for this user.
	if _, err := tx.ExecContext(r.Context(), "DELETE FROM bo_sessions WHERE user_id = ?", userID); err != nil {
		_ = tx.Rollback()
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Error cerrando sesiones"})
		return
	}
	if err := tx.Commit(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error guardando")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Password reseteada. Todas las sesiones cerradas."})
}

func (s *Server) handlePlatformUserRevokeSessions(w http.ResponseWriter, r *http.Request) {
	userID, err := parseIntParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "ID invalido")
		return
	}
	_, err = s.db.ExecContext(r.Context(), "DELETE FROM bo_sessions WHERE user_id = ?", userID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error revocando sesiones")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Todas las sesiones revocadas"})
}

type platformUserAssignRequest struct {
	RestaurantID int    `json:"restaurantId"`
	Role         string `json:"role"`
}

func (s *Server) handlePlatformUserAssign(w http.ResponseWriter, r *http.Request) {
	userID, err := parseIntParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "ID invalido")
		return
	}
	var req platformUserAssignRequest
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "JSON invalido"})
		return
	}
	if req.RestaurantID <= 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "restaurantId requerido"})
		return
	}
	role := strings.TrimSpace(req.Role)
	if role == "" {
		role = "admin"
	}

	_, err = s.db.ExecContext(r.Context(), `
		INSERT INTO bo_user_restaurants (user_id, restaurant_id, role)
		VALUES (?, ?, ?)
		ON DUPLICATE KEY UPDATE role = VALUES(role)
	`, userID, req.RestaurantID, role)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Error: " + err.Error()})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) handlePlatformUserUnassign(w http.ResponseWriter, r *http.Request) {
	userID, err := parseIntParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "ID invalido")
		return
	}
	restaurantID, err := parseIntParam(chi.URLParam(r, "restaurantId"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "restaurantId invalido")
		return
	}
	_, err = s.db.ExecContext(r.Context(), "DELETE FROM bo_user_restaurants WHERE user_id = ? AND restaurant_id = ?", userID, restaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error desasignando")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

// ---------- Subscriptions / recurring features ----------

type platformSubscription struct {
	ID             int     `json:"id"`
	RestaurantID   int     `json:"restaurantId"`
	RestaurantName string  `json:"restaurantName"`
	FeatureKey     string  `json:"featureKey"`
	Concept        string  `json:"concept"`
	Amount         float64 `json:"amount"`
	Currency       string  `json:"currency"`
	Frequency      string  `json:"frequency"`
	IsActive       bool    `json:"isActive"`
	StartDate      string  `json:"startDate"`
	NextRunAt      string  `json:"nextRunAt"`
}

func (s *Server) handlePlatformSubscriptionsList(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT
			ri.id, ri.restaurant_id, r.name,
			COALESCE(ri.feature_key, '') AS feature_key,
			COALESCE(ri.concept, '') AS concept,
			ri.amount, COALESCE(ri.currency, 'EUR') AS currency,
			ri.frequency, ri.is_active,
			DATE_FORMAT(ri.start_date, '%Y-%m-%d') AS start_date,
			COALESCE(DATE_FORMAT(ri.next_run_at, '%Y-%m-%dT%H:%i:%sZ'), '') AS next_run_at
		FROM recurring_invoices ri
		LEFT JOIN restaurants r ON r.id = ri.restaurant_id
		ORDER BY ri.is_active DESC, ri.id DESC
	`)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo suscripciones")
		return
	}
	defer rows.Close()

	var out []platformSubscription
	for rows.Next() {
		var sub platformSubscription
		var isActive int
		if err := rows.Scan(
			&sub.ID, &sub.RestaurantID, &sub.RestaurantName,
			&sub.FeatureKey, &sub.Concept,
			&sub.Amount, &sub.Currency,
			&sub.Frequency, &isActive,
			&sub.StartDate, &sub.NextRunAt,
		); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo filas de suscripciones")
			return
		}
		sub.IsActive = isActive != 0
		out = append(out, sub)
	}
	if out == nil {
		out = []platformSubscription{}
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":       true,
		"subscriptions": out,
	})
}

type platformSubscriptionToggleRequest struct {
	IsActive bool `json:"isActive"`
}

func (s *Server) handlePlatformSubscriptionToggle(w http.ResponseWriter, r *http.Request) {
	subID, err := parseIntParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "ID invalido")
		return
	}
	var req platformSubscriptionToggleRequest
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "JSON invalido"})
		return
	}

	val := 0
	if req.IsActive {
		val = 1
	}
	_, err = s.db.ExecContext(r.Context(), "UPDATE recurring_invoices SET is_active = ? WHERE id = ?", val, subID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error actualizando suscripcion")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

// ---------- Platform WhatsApp instances ----------

type platformWhatsAppInstance struct {
	ID             int64  `json:"id"`
	RestaurantID   int    `json:"restaurantId"`
	RestaurantName string `json:"restaurantName"`
	ServerID       int64  `json:"serverId"`
	InstanceName   string `json:"instanceName"`
	ConnectedPhone string `json:"connectedPhone"`
	Status         string `json:"status"`
	PairCode       string `json:"pairCode"`
	IsActive       bool   `json:"isActive"`
	ConnectedAt    string `json:"connectedAt"`
	UpdatedAt      string `json:"updatedAt"`
}

func (s *Server) handlePlatformWhatsAppList(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT
			i.id, i.restaurant_id, r.name,
			i.server_id, i.instance_name,
			COALESCE(i.connected_phone, '') AS connected_phone,
			i.status, COALESCE(i.pair_code, '') AS pair_code,
			i.is_active,
			COALESCE(DATE_FORMAT(i.connected_at, '%Y-%m-%dT%H:%i:%sZ'), '') AS connected_at,
			COALESCE(DATE_FORMAT(i.updated_at, '%Y-%m-%dT%H:%i:%sZ'), '') AS updated_at
		FROM restaurant_uazapi_instances i
		LEFT JOIN restaurants r ON r.id = i.restaurant_id
		ORDER BY i.id DESC
	`)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo instancias WhatsApp")
		return
	}
	defer rows.Close()

	var out []platformWhatsAppInstance
	for rows.Next() {
		var inst platformWhatsAppInstance
		var isActive int
		if err := rows.Scan(
			&inst.ID, &inst.RestaurantID, &inst.RestaurantName,
			&inst.ServerID, &inst.InstanceName,
			&inst.ConnectedPhone,
			&inst.Status, &inst.PairCode,
			&isActive,
			&inst.ConnectedAt, &inst.UpdatedAt,
		); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo filas WhatsApp")
			return
		}
		inst.IsActive = isActive != 0
		out = append(out, inst)
	}
	if out == nil {
		out = []platformWhatsAppInstance{}
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":   true,
		"instances": out,
	})
}

func (s *Server) handlePlatformWhatsAppRenewQR(w http.ResponseWriter, r *http.Request) {
	instanceID, err := parseInt64Param(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "ID invalido")
		return
	}

	ctx := r.Context()
	var restaurantID int
	err = s.db.QueryRowContext(ctx, "SELECT restaurant_id FROM restaurant_uazapi_instances WHERE id = ?", instanceID).Scan(&restaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusNotFound, "Instancia no encontrada")
		return
	}

	// Re-provision: disconnect old, create new QR.
	conn, err := s.provisionAndConnectRestaurantWhatsApp(ctx, restaurantID, "")
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Error renovando QR: " + err.Error()})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":    true,
		"connection": conn,
		"message":    "QR renovado",
	})
}

func (s *Server) handlePlatformWhatsAppDisconnect(w http.ResponseWriter, r *http.Request) {
	instanceID, err := parseInt64Param(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "ID invalido")
		return
	}

	ctx := r.Context()
	var restaurantID int
	err = s.db.QueryRowContext(ctx, "SELECT restaurant_id FROM restaurant_uazapi_instances WHERE id = ?", instanceID).Scan(&restaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusNotFound, "Instancia no encontrada")
		return
	}

	if err := s.suspendRestaurantUAZAPIInstance(ctx, restaurantID); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Error desconectando: " + err.Error()})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Instancia desconectada"})
}

// ---------- Platform domains ----------

type platformDomain struct {
	ID                  int     `json:"id"`
	RestaurantID        int     `json:"restaurantId"`
	RestaurantName      string  `json:"restaurantName"`
	Domain              string  `json:"domain"`
	IsPrimary           bool    `json:"isPrimary"`
	RegistrationStatus  string  `json:"registrationStatus"`
	StripePaymentStatus string  `json:"stripePaymentStatus"`
	RegistrationCost    float64 `json:"registrationCost"`
	AutoRenew           bool    `json:"autoRenew"`
	CreatedAt           string  `json:"createdAt"`
}

func (s *Server) handlePlatformDomainsList(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT
			d.id, d.restaurant_id, r.name,
			d.domain, d.is_primary,
			d.registration_status, d.stripe_payment_status,
			COALESCE(d.registration_cost, 0), d.auto_renew,
			DATE_FORMAT(d.created_at, '%Y-%m-%dT%H:%i:%sZ') AS created_at
		FROM restaurant_domains d
		LEFT JOIN restaurants r ON r.id = d.restaurant_id
		ORDER BY d.id DESC
	`)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo dominios")
		return
	}
	defer rows.Close()

	var out []platformDomain
	for rows.Next() {
		var d platformDomain
		var isPrimary, autoRenew int
		if err := rows.Scan(
			&d.ID, &d.RestaurantID, &d.RestaurantName,
			&d.Domain, &isPrimary,
			&d.RegistrationStatus, &d.StripePaymentStatus,
			&d.RegistrationCost, &autoRenew,
			&d.CreatedAt,
		); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo filas de dominios")
			return
		}
		d.IsPrimary = isPrimary != 0
		d.AutoRenew = autoRenew != 0
		out = append(out, d)
	}
	if out == nil {
		out = []platformDomain{}
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"domains": out,
	})
}

// ---------- Stripe payments + refunds ----------

type platformStripePayment struct {
	EventID        string `json:"eventId"`
	Type           string `json:"type"`
	Amount         int64  `json:"amount"`
	Currency       string `json:"currency"`
	Domain         string `json:"domain"`
	RestaurantID   int    `json:"restaurantId"`
	RestaurantName string `json:"restaurantName"`
	PaidAt         string `json:"paidAt"`
}

func (s *Server) handlePlatformStripePaymentsList(w http.ResponseWriter, r *http.Request) {
	// First: local stripe_events (checkout completions).
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT
			e.event_id, e.type,
			COALESCE(JSON_UNQUOTE(JSON_EXTRACT(e.payload, '$.data.object.amount')), 0) AS amount,
			COALESCE(JSON_UNQUOTE(JSON_EXTRACT(e.payload, '$.data.object.currency')), '') AS currency,
			COALESCE(JSON_UNQUOTE(JSON_EXTRACT(e.payload, '$.data.object.metadata.domain')), '') AS domain,
			COALESCE(JSON_UNQUOTE(JSON_EXTRACT(e.payload, '$.data.object.metadata.restaurant_id')), '0') AS restaurant_id,
			r.name AS restaurant_name,
			COALESCE(DATE_FORMAT(e.created_at, '%Y-%m-%dT%H:%i:%sZ'), '') AS paid_at
		FROM stripe_events e
		LEFT JOIN restaurants r ON r.id = CAST(COALESCE(JSON_UNQUOTE(JSON_EXTRACT(e.payload, '$.data.object.metadata.restaurant_id')), '0') AS UNSIGNED)
		ORDER BY e.created_at DESC
		LIMIT 200
	`)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo pagos de Stripe")
		return
	}
	defer rows.Close()

	var out []platformStripePayment
	for rows.Next() {
		var p platformStripePayment
		if err := rows.Scan(&p.EventID, &p.Type, &p.Amount, &p.Currency, &p.Domain, &p.RestaurantID, &p.RestaurantName, &p.PaidAt); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo filas de pagos")
			return
		}
		out = append(out, p)
	}

	// Second: live payments from Stripe API (if key configured).
	livePayments := []map[string]any{}
	if s.cfg.StripeSecretKey != "" {
		stripe := newPlatformStripeClient(s.cfg.StripeSecretKey)
		live, err := stripe.ListCharges(r.Context(), 50)
		if err == nil {
			livePayments = live
		}
	}

	if out == nil {
		out = []platformStripePayment{}
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":      true,
		"localEvents":  out,
		"livePayments": livePayments,
	})
}

type platformStripeRefundRequest struct {
	PaymentIntentID string `json:"paymentIntentId"`
	ChargeID        string `json:"chargeId"`
	Amount          int64  `json:"amount"` // cents, 0 = full
	Reason          string `json:"reason"`
}

func (s *Server) handlePlatformStripeRefund(w http.ResponseWriter, r *http.Request) {
	if s.cfg.StripeSecretKey == "" {
		httpx.WriteError(w, http.StatusServiceUnavailable, "Stripe no configurado")
		return
	}
	var req platformStripeRefundRequest
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "JSON invalido"})
		return
	}

	id := strings.TrimSpace(req.ChargeID)
	if id == "" {
		id = strings.TrimSpace(req.PaymentIntentID)
	}
	if id == "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "chargeId o paymentIntentId requerido"})
		return
	}

	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		reason = "requested_by_customer"
	}

	stripe := newPlatformStripeClient(s.cfg.StripeSecretKey)
	refund, err := stripe.CreateRefund(r.Context(), id, req.Amount, reason)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Error en devolucion: " + err.Error()})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"refund":  refund,
	})
}

// ---------- Platform dashboard metrics ----------

func (s *Server) handlePlatformDashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var restaurantCount, userCount, superadminCount, activeSessionCount, subCount, activeSubCount, domainCount, waCount int

	_ = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM restaurants").Scan(&restaurantCount)
	_ = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM bo_users").Scan(&userCount)
	_ = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM bo_users WHERE is_superadmin = 1").Scan(&superadminCount)
	_ = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM bo_sessions WHERE expires_at > NOW()").Scan(&activeSessionCount)
	_ = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM recurring_invoices").Scan(&subCount)
	_ = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM recurring_invoices WHERE is_active = 1").Scan(&activeSubCount)
	_ = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM restaurant_domains").Scan(&domainCount)
	_ = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM restaurant_uazapi_instances").Scan(&waCount)

	var monthlyRecurringRevenue float64
	_ = s.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(
			CASE
				WHEN frequency = 'monthly' THEN amount
				WHEN frequency = 'yearly' THEN amount / 12
				WHEN frequency = 'quarterly' THEN amount / 3
				WHEN frequency = 'weekly' THEN amount * 4.33
				ELSE 0
			END
		), 0)
		FROM recurring_invoices WHERE is_active = 1
	`).Scan(&monthlyRecurringRevenue)

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"metrics": map[string]any{
			"restaurants":             restaurantCount,
			"users":                   userCount,
			"superadmins":             superadminCount,
			"activeSessions":          activeSessionCount,
			"subscriptions":           subCount,
			"activeSubscriptions":     activeSubCount,
			"domains":                 domainCount,
			"whatsappInstances":       waCount,
			"monthlyRecurringRevenue": monthlyRecurringRevenue,
		},
	})
}

// ---------- UAZAPI servers management ----------

type platformUAZAPIServer struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Provider  string `json:"provider"`
	BaseURL   string `json:"baseUrl"`
	Capacity  int    `json:"capacity"`
	UsedCount int    `json:"usedCount"`
	IsActive  bool   `json:"isActive"`
}

func (s *Server) handlePlatformUAZAPIServersList(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT id, name, provider, base_url, capacity, used_count, is_active
		FROM uazapi_servers
		ORDER BY is_active DESC, priority DESC, id ASC
	`)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo servidores UAZAPI")
		return
	}
	defer rows.Close()

	var out []platformUAZAPIServer
	for rows.Next() {
		var srv platformUAZAPIServer
		var isActive int
		if err := rows.Scan(&srv.ID, &srv.Name, &srv.Provider, &srv.BaseURL, &srv.Capacity, &srv.UsedCount, &isActive); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo servidores")
			return
		}
		srv.IsActive = isActive != 0
		out = append(out, srv)
	}
	if out == nil {
		out = []platformUAZAPIServer{}
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"servers": out,
	})
}

// ---------- Helpers ----------

func parseIntParam(s string) (int, error) {
	if s == "" {
		return 0, errors.New("empty")
	}
	n, err := strconv.Atoi(s)
	return n, err
}

func parseInt64Param(s string) (int64, error) {
	if s == "" {
		return 0, errors.New("empty")
	}
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err
}
