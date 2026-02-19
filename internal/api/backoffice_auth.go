package api

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"preactvillacarmen/internal/httpx"
)

type boLoginRequest struct {
	Email      string `json:"email"`
	Identifier string `json:"identifier"`
	Password   string `json:"password"`
}

type boActiveRestaurantRequest struct {
	RestaurantID int `json:"restaurantId"`
}

type boSetPasswordRequest struct {
	Password        string `json:"password"`
	ConfirmPassword string `json:"confirmPassword"`
	PasswordRepeat  string `json:"passwordRepeat"`
}

func (s *Server) handleBOLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpx.WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Allow provisioning a first admin in brand new DBs.
	if err := s.ensureBootstrapAdmin(r.Context()); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error bootstrapping admin")
		return
	}

	var req boLoginRequest
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{ // client treats non-2xx as transport error
			"success": false,
			"message": "Invalid JSON",
		})
		return
	}

	identifier := strings.ToLower(strings.TrimSpace(req.Identifier))
	if identifier == "" {
		identifier = strings.ToLower(strings.TrimSpace(req.Email))
	}
	password := req.Password
	if identifier == "" || password == "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Usuario/email y password son requeridos",
		})
		return
	}

	var (
		userID             int
		dbEmail            string
		dbUsername         sql.NullString
		name               string
		hash               string
		isSuper            int
		mustChangePassword int
	)
	err := s.db.QueryRowContext(r.Context(), `
		SELECT
			id,
			email,
			username,
			name,
			password_hash,
			is_superadmin,
			must_change_password
		FROM bo_users
		WHERE LOWER(TRIM(email)) = LOWER(TRIM(?))
			OR LOWER(TRIM(COALESCE(username, ''))) = LOWER(TRIM(?))
		ORDER BY
			CASE
				WHEN LOWER(TRIM(email)) = LOWER(TRIM(?)) THEN 0
				ELSE 1
			END ASC
		LIMIT 1
	`, identifier, identifier, identifier).Scan(&userID, &dbEmail, &dbUsername, &name, &hash, &isSuper, &mustChangePassword)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"message": "Credenciales invalidas",
			})
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo usuario")
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Credenciales invalidas",
		})
		return
	}

	restaurants, err := s.listUserRestaurants(r.Context(), userID, isSuper != 0)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo restaurantes")
		return
	}
	if len(restaurants) == 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Tu cuenta no tiene restaurantes asignados",
		})
		return
	}

	activeRestaurantID := restaurants[0].ID
	roleSlug, err := s.getBOUserRoleForRestaurant(r.Context(), userID, activeRestaurantID, isSuper != 0)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo rol")
		return
	}
	roleImportance, err := s.roleImportance(r.Context(), roleSlug)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo importancia de rol")
		return
	}
	sectionAccess, err := s.roleSections(r.Context(), roleSlug)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo permisos de rol")
		return
	}

	token, tokenSHA, err := newBOSessionToken()
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error creando sesion")
		return
	}
	ttl := boSessionTTL()
	expiresAt := time.Now().Add(ttl)

	ip := clientIP(r)
	ua := strings.TrimSpace(r.Header.Get("User-Agent"))
	if len(ua) > 250 {
		ua = ua[:250]
	}

	_, err = s.db.ExecContext(r.Context(), `
		INSERT INTO bo_sessions (token_sha256, user_id, active_restaurant_id, expires_at, ip, user_agent)
		VALUES (?, ?, ?, ?, ?, ?)
	`, tokenSHA, userID, activeRestaurantID, expiresAt, ip, ua)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error guardando sesion")
		return
	}

	secure := r.TLS != nil
	http.SetCookie(w, &http.Cookie{
		Name:     boSessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		Expires:  expiresAt,
		MaxAge:   int(ttl.Seconds()),
	})

	sess := boSession{
		User: boUser{
			ID:             userID,
			Email:          dbEmail,
			Username:       strings.TrimSpace(dbUsername.String),
			Name:           name,
			Role:           roleSlug,
			RoleImportance: roleImportance,
			SectionAccess:  sectionAccess,
			MustChangePass: mustChangePassword != 0,
		},
		Restaurants:        restaurants,
		ActiveRestaurantID: activeRestaurantID,
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"session": sess,
	})
}

func (s *Server) handleBOLogout(w http.ResponseWriter, r *http.Request) {
	// Idempotent: always clear cookie.
	if c, err := r.Cookie(boSessionCookieName); err == nil && strings.TrimSpace(c.Value) != "" {
		_, _ = s.db.ExecContext(r.Context(), "DELETE FROM bo_sessions WHERE token_sha256 = ?", sha256Hex(c.Value))
	}

	secure := r.TLS != nil
	http.SetCookie(w, &http.Cookie{
		Name:     boSessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
	})

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
	})
}

func (s *Server) handleBOMe(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	restaurants, err := s.listUserRestaurants(r.Context(), a.User.ID, a.User.isSuperadmin)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo restaurantes")
		return
	}
	if len(restaurants) == 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Tu cuenta no tiene restaurantes asignados",
		})
		return
	}

	activeID := a.ActiveRestaurantID
	if activeID == 0 || !restaurantInList(restaurants, activeID) {
		activeID = restaurants[0].ID
		_, _ = s.db.ExecContext(r.Context(), "UPDATE bo_sessions SET active_restaurant_id = ? WHERE id = ?", activeID, a.SessionID)
	}
	roleSlug, err := s.getBOUserRoleForRestaurant(r.Context(), a.User.ID, activeID, a.User.isSuperadmin)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo rol")
		return
	}
	roleImportance, err := s.roleImportance(r.Context(), roleSlug)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo importancia de rol")
		return
	}
	sectionAccess, err := s.roleSections(r.Context(), roleSlug)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo permisos de rol")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"session": boSession{
			User: boUser{
				ID:             a.User.ID,
				Email:          a.User.Email,
				Username:       a.User.Username,
				Name:           a.User.Name,
				Role:           roleSlug,
				RoleImportance: roleImportance,
				SectionAccess:  sectionAccess,
				MustChangePass: a.User.MustChangePass,
			},
			Restaurants:        restaurants,
			ActiveRestaurantID: activeID,
		},
	})
}

func (s *Server) handleBOSetPassword(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req boSetPasswordRequest
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Invalid JSON",
		})
		return
	}
	password := strings.TrimSpace(req.Password)
	confirm := strings.TrimSpace(req.ConfirmPassword)
	if confirm == "" {
		confirm = strings.TrimSpace(req.PasswordRepeat)
	}
	if password == "" || confirm == "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Password requerida",
		})
		return
	}
	if password != confirm {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Las passwords no coinciden",
		})
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "No se pudo guardar password")
		return
	}

	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error iniciando transaccion")
		return
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(r.Context(), `
		UPDATE bo_users
		SET password_hash = ?, must_change_password = 0
		WHERE id = ?
	`, string(hash), a.User.ID); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "No se pudo actualizar password")
		return
	}

	_, _ = tx.ExecContext(r.Context(), `
		UPDATE bo_password_reset_tokens
		SET invalidated_at = NOW(), invalidated_reason = 'password-updated'
		WHERE bo_user_id = ?
			AND used_at IS NULL
			AND invalidated_at IS NULL
			AND expires_at > NOW()
	`, a.User.ID)

	_, _ = tx.ExecContext(r.Context(), `
		UPDATE bo_member_invitation_tokens
		SET invalidated_at = NOW(), invalidated_reason = 'password-updated'
		WHERE bo_user_id = ?
			AND used_at IS NULL
			AND invalidated_at IS NULL
			AND expires_at > NOW()
	`, a.User.ID)

	if err := tx.Commit(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error guardando password")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
	})
}

func (s *Server) handleBOSetActiveRestaurant(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req boActiveRestaurantRequest
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Invalid JSON",
		})
		return
	}
	if req.RestaurantID <= 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "restaurantId invalido",
		})
		return
	}

	restaurants, err := s.listUserRestaurants(r.Context(), a.User.ID, a.User.isSuperadmin)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo restaurantes")
		return
	}
	if !restaurantInList(restaurants, req.RestaurantID) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "No tienes acceso a ese restaurante",
		})
		return
	}

	if _, err := s.db.ExecContext(r.Context(), "UPDATE bo_sessions SET active_restaurant_id = ? WHERE id = ?", req.RestaurantID, a.SessionID); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error actualizando sesion")
		return
	}
	roleSlug, err := s.getBOUserRoleForRestaurant(r.Context(), a.User.ID, req.RestaurantID, a.User.isSuperadmin)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo rol")
		return
	}
	roleImportance, err := s.roleImportance(r.Context(), roleSlug)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo importancia de rol")
		return
	}
	sectionAccess, err := s.roleSections(r.Context(), roleSlug)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo permisos de rol")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":            true,
		"activeRestaurantId": req.RestaurantID,
		"role":               roleSlug,
		"roleImportance":     roleImportance,
		"sectionAccess":      sectionAccess,
	})
}

func (s *Server) ensureBootstrapAdmin(ctx context.Context) error {
	email := strings.ToLower(strings.TrimSpace(os.Getenv("BOOTSTRAP_ADMIN_EMAIL")))
	password := os.Getenv("BOOTSTRAP_ADMIN_PASSWORD")
	name := strings.TrimSpace(os.Getenv("BOOTSTRAP_ADMIN_NAME"))
	if name == "" {
		name = "Admin"
	}
	if email == "" || password == "" {
		return nil
	}

	var userID int
	err := s.db.QueryRowContext(ctx, "SELECT id FROM bo_users WHERE email = ? LIMIT 1", email).Scan(&userID)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}

		hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		res, err := s.db.ExecContext(ctx, `
			INSERT INTO bo_users (email, name, password_hash, is_superadmin)
			VALUES (?, ?, ?, 1)
		`, email, name, string(hash))
		if err != nil {
			return err
		}
		id, _ := res.LastInsertId()
		userID = int(id)
	}

	// Ensure at least restaurant #1 is assigned.
	_, _ = s.db.ExecContext(ctx, `
		INSERT IGNORE INTO bo_user_restaurants (user_id, restaurant_id, role)
		VALUES (?, 1, 'admin')
	`, userID)

	return nil
}

func (s *Server) listUserRestaurants(ctx context.Context, userID int, isSuperadmin bool) ([]boRestaurant, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if isSuperadmin {
		rows, err = s.db.QueryContext(ctx, "SELECT id, slug, name FROM restaurants ORDER BY name ASC, id ASC")
	} else {
		rows, err = s.db.QueryContext(ctx, `
			SELECT r.id, r.slug, r.name
			FROM restaurants r
			JOIN bo_user_restaurants ur ON ur.restaurant_id = r.id
			WHERE ur.user_id = ?
			ORDER BY r.name ASC, r.id ASC
		`, userID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []boRestaurant
	for rows.Next() {
		var rr boRestaurant
		if err := rows.Scan(&rr.ID, &rr.Slug, &rr.Name); err != nil {
			return nil, err
		}
		out = append(out, rr)
	}
	return out, nil
}

func restaurantInList(list []boRestaurant, id int) bool {
	for _, r := range list {
		if r.ID == id {
			return true
		}
	}
	return false
}

func (s *Server) getBOUserRoleForRestaurant(ctx context.Context, userID, restaurantID int, isSuperadmin bool) (string, error) {
	if isSuperadmin {
		return "root", nil
	}
	var raw sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT role
		FROM bo_user_restaurants
		WHERE user_id = ? AND restaurant_id = ?
		LIMIT 1
	`, userID, restaurantID).Scan(&raw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "admin", nil
		}
		return "", err
	}
	role := normalizeBORole(raw.String)
	if role == "" {
		role = "admin"
	}
	return role, nil
}

func newBOSessionToken() (token string, tokenSHA string, err error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", "", err
	}
	token = base64.RawURLEncoding.EncodeToString(b[:])
	tokenSHA = sha256Hex(token)
	return token, tokenSHA, nil
}

func boSessionTTL() time.Duration {
	days := 30
	if v := strings.TrimSpace(os.Getenv("BO_SESSION_TTL_DAYS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 365 {
			days = n
		}
	}
	return time.Duration(days) * 24 * time.Hour
}
