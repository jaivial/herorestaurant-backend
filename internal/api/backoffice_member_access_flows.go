package api

import (
	"context"
	crand "crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"net/smtp"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"

	"preactvillacarmen/internal/httpx"
)

type boMemberCreateResult struct {
	Success      bool
	Message      string
	Member       boMember
	RoleSlug     string
	User         map[string]any
	Invitation   map[string]any
	Provisioning map[string]any
}

type boDeliveryAttempt struct {
	Channel string `json:"channel"`
	Target  string `json:"target"`
	Sent    bool   `json:"sent"`
	Error   string `json:"error,omitempty"`
}

type boInvitationTokenRequest struct {
	Token string `json:"token"`
}

type boInvitationOnboardingProfileRequest struct {
	FirstName *string `json:"firstName"`
	LastName  *string `json:"lastName"`
	PhotoURL  *string `json:"photoUrl"`
}

type boInvitationOnboardingPasswordRequest struct {
	Password        string `json:"password"`
	ConfirmPassword string `json:"confirmPassword"`
	PasswordRepeat  string `json:"passwordRepeat"`
}

type boPasswordResetConfirmRequest struct {
	Token           string `json:"token"`
	Password        string `json:"password"`
	ConfirmPassword string `json:"confirmPassword"`
	PasswordRepeat  string `json:"passwordRepeat"`
}

type boMemberAccessRecord struct {
	RestaurantID int
	MemberID     int
	BOUserID     int
	FirstName    string
	LastName     string
	Email        string
	Phone        string
	DNI          string
	PhotoURL     string
	Username     string
	RoleSlug     string
	RoleLabel    string
}

type boInvitationRecord struct {
	TokenID        int64
	TokenSHA       string
	OnboardingGUID string
	ExpiresAt      time.Time
	boMemberAccessRecord
}

type boPasswordResetRecord struct {
	TokenID   int64
	TokenSHA  string
	ExpiresAt time.Time
	boMemberAccessRecord
}

func invitationTokenTTL() time.Duration {
	const fallback = 7 * 24 * time.Hour
	if raw := strings.TrimSpace(os.Getenv("BO_INVITATION_TOKEN_TTL_HOURS")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 24*90 {
			return time.Duration(n) * time.Hour
		}
	}
	return fallback
}

func passwordResetTokenTTL() time.Duration {
	const fallback = 24 * time.Hour
	if raw := strings.TrimSpace(os.Getenv("BO_PASSWORD_RESET_TOKEN_TTL_HOURS")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 24*14 {
			return time.Duration(n) * time.Hour
		}
	}
	return fallback
}

func (s *Server) createBOMemberAndBootstrapAccess(ctx context.Context, a boAuth, req boMemberCreateRequest, r *http.Request) (boMemberCreateResult, error) {
	firstName := strings.TrimSpace(req.FirstName)
	lastName := strings.TrimSpace(req.LastName)
	if firstName == "" || lastName == "" {
		return boMemberCreateResult{Success: false, Message: "Nombre y apellidos son obligatorios"}, nil
	}

	weeklyHours := 40.0
	if req.WeeklyContractHours != nil {
		weeklyHours = *req.WeeklyContractHours
	}
	if weeklyHours < 0 {
		return boMemberCreateResult{Success: false, Message: "weeklyContractHours debe ser >= 0"}, nil
	}

	email := normalizeOptionalEmail(req.Email)
	dni := normalizeOptionalString(req.DNI)
	bank := normalizeOptionalString(req.BankAccount)
	phone := normalizeOptionalString(req.Phone)
	photo := normalizeOptionalString(req.PhotoURL)
	roleSlug := normalizeBORole(ptrToValue(req.RoleSlug))
	if roleSlug == "" {
		roleSlug = "admin"
	}

	hasAnyContact := email != "" || phone != ""
	username := normalizeUsername(ptrToValue(req.Username))
	temporaryPassword := strings.TrimSpace(ptrToValue(req.TemporaryPassword))
	manualCredentials := !hasAnyContact
	if manualCredentials {
		if username == "" {
			return boMemberCreateResult{Success: false, Message: "Sin email/telefono debes indicar username"}, nil
		}
		if temporaryPassword == "" {
			return boMemberCreateResult{Success: false, Message: "Sin email/telefono debes indicar password temporal"}, nil
		}
	}

	roleExists, err := s.boRoleExists(ctx, roleSlug)
	if err != nil {
		return boMemberCreateResult{}, err
	}
	if !roleExists {
		return boMemberCreateResult{Success: false, Message: "Rol invalido"}, nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return boMemberCreateResult{}, err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, `
		INSERT INTO restaurant_members
			(restaurant_id, first_name, last_name, email, dni, bank_account, phone, photo_url, is_active)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 1)
	`, a.ActiveRestaurantID, firstName, lastName, nullableString(email), nullableString(dni), nullableString(bank), nullableString(phone), nullableString(photo))
	if err != nil {
		return boMemberCreateResult{Success: false, Message: "No se pudo crear el miembro (email/usuario duplicado)"}, nil
	}

	memberID64, _ := res.LastInsertId()
	memberID := int(memberID64)

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO member_contracts (restaurant_member_id, restaurant_id, weekly_hours)
		VALUES (?, ?, ?)
		ON DUPLICATE KEY UPDATE
			weekly_hours = VALUES(weekly_hours)
	`, memberID, a.ActiveRestaurantID, weeklyHours); err != nil {
		return boMemberCreateResult{}, err
	}

	user, err := s.ensureBOUserForMemberTx(ctx, tx, boEnsureUserInput{
		RestaurantID:      a.ActiveRestaurantID,
		FirstName:         firstName,
		LastName:          lastName,
		Email:             email,
		Phone:             phone,
		Username:          username,
		TemporaryPassword: temporaryPassword,
		ManualCredentials: manualCredentials,
	})
	if err != nil {
		if errors.Is(err, errUsernameTaken) {
			return boMemberCreateResult{Success: false, Message: "El username ya esta en uso"}, nil
		}
		return boMemberCreateResult{}, err
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE restaurant_members
		SET bo_user_id = ?
		WHERE id = ? AND restaurant_id = ? AND is_active = 1
	`, user.UserID, memberID, a.ActiveRestaurantID); err != nil {
		return boMemberCreateResult{Success: false, Message: "No se pudo vincular usuario de backoffice"}, nil
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO bo_user_restaurants (user_id, restaurant_id, role)
		VALUES (?, ?, ?)
		ON DUPLICATE KEY UPDATE role = VALUES(role)
	`, user.UserID, a.ActiveRestaurantID, roleSlug); err != nil {
		return boMemberCreateResult{}, err
	}

	invitationToken := ""
	invitationExpiresAt := time.Time{}
	if hasAnyContact {
		invitationToken, invitationExpiresAt, err = s.createMemberInvitationTokenTx(ctx, tx, boCreateMemberInvitationInput{
			RestaurantID:     a.ActiveRestaurantID,
			MemberID:         memberID,
			BOUserID:         user.UserID,
			RoleSlug:         roleSlug,
			CreatedByUserID:  a.User.ID,
			InvalidateReason: "replaced",
		})
		if err != nil {
			return boMemberCreateResult{}, err
		}
	}

	if err := tx.Commit(); err != nil {
		return boMemberCreateResult{}, err
	}

	member, err := s.getBOMemberByID(ctx, a.ActiveRestaurantID, memberID)
	if err != nil {
		return boMemberCreateResult{}, err
	}

	delivery := []boDeliveryAttempt{}
	invitation := map[string]any{"created": false}
	if invitationToken != "" {
		inviteURL := buildBackofficeAbsoluteURL(r, "/invitacion/"+invitationToken)
		delivery = s.sendMemberInvitation(ctx, a.ActiveRestaurantID, email, phone, inviteURL)
		invitation = map[string]any{
			"created":   true,
			"expiresAt": invitationExpiresAt.Format(time.RFC3339),
			"delivery":  delivery,
		}
	}

	provisioning := map[string]any{
		"manualCredentials": manualCredentials,
		"hasContact":        hasAnyContact,
	}
	if manualCredentials {
		provisioning["mustChangePassword"] = true
	}

	userPayload := map[string]any{
		"id":                 user.UserID,
		"email":              user.Email,
		"username":           user.Username,
		"created":            user.Created,
		"mustChangePassword": user.MustChangePass,
	}

	return boMemberCreateResult{
		Success:      true,
		Member:       member,
		RoleSlug:     roleSlug,
		User:         userPayload,
		Invitation:   invitation,
		Provisioning: provisioning,
	}, nil
}

var errUsernameTaken = errors.New("username taken")

type boEnsureUserInput struct {
	RestaurantID      int
	FirstName         string
	LastName          string
	Email             string
	Phone             string
	Username          string
	TemporaryPassword string
	ManualCredentials bool
}

type boEnsuredUser struct {
	UserID         int
	Email          string
	Username       string
	Created        bool
	MustChangePass bool
}

func (s *Server) ensureBOUserForMemberTx(ctx context.Context, tx *sql.Tx, in boEnsureUserInput) (boEnsuredUser, error) {
	displayName := strings.TrimSpace(in.FirstName + " " + in.LastName)
	if displayName == "" {
		displayName = "Miembro"
	}
	normalizedEmail := strings.ToLower(strings.TrimSpace(in.Email))
	normalizedUsername := normalizeUsername(in.Username)

	if normalizedEmail != "" {
		var existing boEnsuredUser
		var usernameRaw sql.NullString
		err := tx.QueryRowContext(ctx, `
			SELECT id, email, username, must_change_password
			FROM bo_users
			WHERE LOWER(TRIM(email)) = LOWER(TRIM(?))
			LIMIT 1
		`, normalizedEmail).Scan(&existing.UserID, &existing.Email, &usernameRaw, &existing.MustChangePass)
		if err == nil {
			existing.Username = strings.TrimSpace(usernameRaw.String)
			if existing.Username == "" {
				cand := normalizedUsername
				if cand == "" {
					cand = usernameBaseFromIdentity(displayName, normalizedEmail)
				}
				if cand != "" {
					resolved, rerr := ensureUniqueUsernameTx(ctx, tx, cand)
					if rerr == nil {
						_, _ = tx.ExecContext(ctx, `UPDATE bo_users SET username = ? WHERE id = ?`, resolved, existing.UserID)
						existing.Username = resolved
					}
				}
			}
			existing.Created = false
			return existing, nil
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return boEnsuredUser{}, err
		}
	}

	if normalizedUsername == "" {
		normalizedUsername = usernameBaseFromIdentity(displayName, normalizedEmail)
	}
	if normalizedUsername == "" {
		normalizedUsername = "miembro"
	}

	resolvedUsername, err := ensureUniqueUsernameTx(ctx, tx, normalizedUsername)
	if err != nil {
		if errors.Is(err, errUsernameTaken) {
			return boEnsuredUser{}, errUsernameTaken
		}
		return boEnsuredUser{}, err
	}

	finalEmail := normalizedEmail
	if finalEmail == "" {
		finalEmail = syntheticEmailFromUsername(resolvedUsername)
	}

	passwordPlain := strings.TrimSpace(in.TemporaryPassword)
	mustChange := in.ManualCredentials
	if passwordPlain == "" {
		passwordPlain, _, err = newBOSessionToken()
		if err != nil {
			return boEnsuredUser{}, err
		}
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(passwordPlain), bcrypt.DefaultCost)
	if err != nil {
		return boEnsuredUser{}, err
	}

	res, err := tx.ExecContext(ctx, `
		INSERT INTO bo_users (email, username, name, password_hash, is_superadmin, must_change_password)
		VALUES (?, ?, ?, ?, 0, ?)
	`, finalEmail, resolvedUsername, displayName, string(hash), boolToIntFlag(mustChange))
	if err != nil {
		// Retry by resolving an already-created user (race on email).
		var existing boEnsuredUser
		var usernameRaw sql.NullString
		err2 := tx.QueryRowContext(ctx, `
			SELECT id, email, username, must_change_password
			FROM bo_users
			WHERE LOWER(TRIM(email)) = LOWER(TRIM(?))
			LIMIT 1
		`, finalEmail).Scan(&existing.UserID, &existing.Email, &usernameRaw, &existing.MustChangePass)
		if err2 != nil {
			return boEnsuredUser{}, err
		}
		existing.Username = strings.TrimSpace(usernameRaw.String)
		if existing.Username == "" {
			_, _ = tx.ExecContext(ctx, `UPDATE bo_users SET username = ? WHERE id = ?`, resolvedUsername, existing.UserID)
			existing.Username = resolvedUsername
		}
		existing.Created = false
		return existing, nil
	}

	lastID, _ := res.LastInsertId()
	return boEnsuredUser{
		UserID:         int(lastID),
		Email:          finalEmail,
		Username:       resolvedUsername,
		Created:        true,
		MustChangePass: mustChange,
	}, nil
}

func normalizeUsername(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return ""
	}
	var b strings.Builder
	prevSep := false
	for _, ch := range raw {
		isAZ := ch >= 'a' && ch <= 'z'
		is09 := ch >= '0' && ch <= '9'
		if isAZ || is09 {
			b.WriteRune(ch)
			prevSep = false
			continue
		}
		if ch == '_' || ch == '-' || ch == '.' || ch == ' ' {
			if !prevSep && b.Len() > 0 {
				b.WriteByte('_')
				prevSep = true
			}
		}
	}
	out := strings.Trim(b.String(), "_")
	if len(out) > 40 {
		out = out[:40]
		out = strings.Trim(out, "_")
	}
	if len(out) < 3 {
		return ""
	}
	return out
}

func usernameBaseFromIdentity(name string, email string) string {
	if email != "" {
		if before, _, ok := strings.Cut(email, "@"); ok {
			if v := normalizeUsername(before); v != "" {
				return v
			}
		}
	}
	if v := normalizeUsername(name); v != "" {
		return v
	}
	return "miembro"
}

func ensureUniqueUsernameTx(ctx context.Context, tx *sql.Tx, base string) (string, error) {
	base = normalizeUsername(base)
	if base == "" {
		base = "miembro"
	}
	if len(base) > 36 {
		base = base[:36]
	}

	for i := 0; i < 300; i++ {
		candidate := base
		if i > 0 {
			suffix := strconv.Itoa(i + 1)
			if len(base)+len(suffix) > 40 {
				candidate = base[:40-len(suffix)] + suffix
			} else {
				candidate = base + suffix
			}
		}
		var userID int
		err := tx.QueryRowContext(ctx, `
			SELECT id
			FROM bo_users
			WHERE LOWER(TRIM(COALESCE(username, ''))) = LOWER(TRIM(?))
			LIMIT 1
		`, candidate).Scan(&userID)
		if errors.Is(err, sql.ErrNoRows) {
			return candidate, nil
		}
		if err != nil {
			return "", err
		}
	}
	return "", errUsernameTaken
}

func syntheticEmailFromUsername(username string) string {
	username = normalizeUsername(username)
	if username == "" {
		username = "miembro"
	}
	return username + "@local.invalid"
}

func boolToIntFlag(v bool) int {
	if v {
		return 1
	}
	return 0
}

func (s *Server) boRoleExists(ctx context.Context, roleSlug string) (bool, error) {
	roleSlug = normalizeBORole(roleSlug)
	if roleSlug == "" {
		return false, nil
	}
	var ok int
	err := s.db.QueryRowContext(ctx, `
		SELECT 1
		FROM bo_roles
		WHERE slug = ? AND is_active = 1
		LIMIT 1
	`, roleSlug).Scan(&ok)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		_, has := defaultRolePermissions[roleSlug]
		return has, nil
	}
	return false, err
}

type boCreateMemberInvitationInput struct {
	RestaurantID     int
	MemberID         int
	BOUserID         int
	RoleSlug         string
	CreatedByUserID  int
	InvalidateReason string
}

func (s *Server) createMemberInvitationTokenTx(ctx context.Context, tx *sql.Tx, in boCreateMemberInvitationInput) (string, time.Time, error) {
	if in.InvalidateReason == "" {
		in.InvalidateReason = "replaced"
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE bo_member_invitation_tokens
		SET invalidated_at = NOW(), invalidated_reason = ?
		WHERE restaurant_id = ?
			AND member_id = ?
			AND used_at IS NULL
			AND invalidated_at IS NULL
			AND expires_at > NOW()
	`, in.InvalidateReason, in.RestaurantID, in.MemberID); err != nil {
		return "", time.Time{}, err
	}

	token, tokenSHA, err := newBOSessionToken()
	if err != nil {
		return "", time.Time{}, err
	}
	expiresAt := time.Now().Add(invitationTokenTTL())

	_, err = tx.ExecContext(ctx, `
		INSERT INTO bo_member_invitation_tokens
			(restaurant_id, member_id, bo_user_id, role_slug, token_sha256, expires_at, created_by_user_id)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, in.RestaurantID, in.MemberID, in.BOUserID, in.RoleSlug, tokenSHA, expiresAt, nullableInt(in.CreatedByUserID))
	if err != nil {
		return "", time.Time{}, err
	}
	return token, expiresAt, nil
}

func (s *Server) createPasswordResetTokenTx(ctx context.Context, tx *sql.Tx, restaurantID, memberID, boUserID, createdByUserID int) (string, time.Time, error) {
	if _, err := tx.ExecContext(ctx, `
		UPDATE bo_password_reset_tokens
		SET invalidated_at = NOW(), invalidated_reason = 'replaced'
		WHERE restaurant_id = ?
			AND member_id = ?
			AND used_at IS NULL
			AND invalidated_at IS NULL
			AND expires_at > NOW()
	`, restaurantID, memberID); err != nil {
		return "", time.Time{}, err
	}

	token, tokenSHA, err := newBOSessionToken()
	if err != nil {
		return "", time.Time{}, err
	}
	expiresAt := time.Now().Add(passwordResetTokenTTL())

	_, err = tx.ExecContext(ctx, `
		INSERT INTO bo_password_reset_tokens
			(restaurant_id, member_id, bo_user_id, token_sha256, expires_at, created_by_user_id)
		VALUES (?, ?, ?, ?, ?, ?)
	`, restaurantID, memberID, boUserID, tokenSHA, expiresAt, nullableInt(createdByUserID))
	if err != nil {
		return "", time.Time{}, err
	}
	return token, expiresAt, nil
}

func nullableInt(v int) any {
	if v <= 0 {
		return nil
	}
	return v
}

func buildBackofficeAbsoluteURL(r *http.Request, path string) string {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("BACKOFFICE_PUBLIC_BASE_URL")), "/")
	if base == "" {
		base = strings.TrimRight(strings.TrimSpace(os.Getenv("PUBLIC_BASE_URL")), "/")
	}
	if base == "" {
		scheme := "https"
		if r == nil || r.TLS == nil {
			scheme = "http"
		}
		host := ""
		if r != nil {
			host = strings.TrimSpace(firstForwardedHost(r))
			if host == "" {
				host = strings.TrimSpace(r.Host)
			}
		}
		if host == "" {
			host = "localhost:8080"
		}
		base = scheme + "://" + host
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return base + path
}

func (s *Server) sendMemberInvitation(ctx context.Context, restaurantID int, email, phone, invitationURL string) []boDeliveryAttempt {
	brand := s.restaurantNameFallback(ctx, restaurantID)
	subject := brand + " · Invitacion de acceso"
	body := "Hola,\n\nTe han invitado al backoffice de " + brand + ".\nCompleta tu alta en este enlace:\n" + invitationURL + "\n\nSi no esperabas esta invitacion, ignora este mensaje."
	waText := "Te han invitado al backoffice de " + brand + ". Completa tu alta aqui: " + invitationURL

	results := make([]boDeliveryAttempt, 0, 2)
	if strings.TrimSpace(email) != "" {
		err := sendSMTPMailBestEffort(email, subject, body)
		results = append(results, boDeliveryAttempt{Channel: "email", Target: email, Sent: err == nil, Error: errorString(err)})
	}
	if strings.TrimSpace(phone) != "" {
		err := s.sendWhatsAppMessage(ctx, restaurantID, phone, waText)
		results = append(results, boDeliveryAttempt{Channel: "whatsapp", Target: phone, Sent: err == nil, Error: errorString(err)})
	}
	return results
}

func (s *Server) sendMemberPasswordReset(ctx context.Context, restaurantID int, email, phone, resetURL string) []boDeliveryAttempt {
	brand := s.restaurantNameFallback(ctx, restaurantID)
	subject := brand + " · Restablecer password"
	body := "Hola,\n\nHas solicitado restablecer tu password en " + brand + ".\nUsa este enlace:\n" + resetURL + "\n\nSi no fuiste tu, ignora este mensaje."
	waText := "Restablece tu password de " + brand + ": " + resetURL

	results := make([]boDeliveryAttempt, 0, 2)
	if strings.TrimSpace(email) != "" {
		err := sendSMTPMailBestEffort(email, subject, body)
		results = append(results, boDeliveryAttempt{Channel: "email", Target: email, Sent: err == nil, Error: errorString(err)})
	}
	if strings.TrimSpace(phone) != "" {
		err := s.sendWhatsAppMessage(ctx, restaurantID, phone, waText)
		results = append(results, boDeliveryAttempt{Channel: "whatsapp", Target: phone, Sent: err == nil, Error: errorString(err)})
	}
	return results
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func sendSMTPMailBestEffort(to string, subject string, body string) error {
	to = strings.TrimSpace(to)
	if to == "" {
		return errors.New("destinatario vacio")
	}
	if _, err := mail.ParseAddress(to); err != nil {
		return errors.New("email invalido")
	}

	host := strings.TrimSpace(os.Getenv("SMTP_HOST"))
	port := strings.TrimSpace(os.Getenv("SMTP_PORT"))
	from := strings.TrimSpace(os.Getenv("SMTP_FROM"))
	user := strings.TrimSpace(os.Getenv("SMTP_USER"))
	pass := strings.TrimSpace(os.Getenv("SMTP_PASS"))
	if host == "" || port == "" || from == "" {
		return errors.New("smtp no configurado")
	}

	addr := host + ":" + port
	auth := smtp.Auth(nil)
	if user != "" && pass != "" {
		auth = smtp.PlainAuth("", user, pass, host)
	}

	msg := strings.Builder{}
	msg.WriteString("From: " + from + "\r\n")
	msg.WriteString("To: " + to + "\r\n")
	msg.WriteString("Subject: " + mimeSafeSubject(subject) + "\r\n")
	msg.WriteString("MIME-Version: 1.0\r\n")
	msg.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	msg.WriteString("\r\n")
	msg.WriteString(body)

	return smtp.SendMail(addr, auth, from, []string{to}, []byte(msg.String()))
}

func mimeSafeSubject(subject string) string {
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return "Mensaje"
	}
	return subject
}

func (s *Server) sendWhatsAppMessage(ctx context.Context, restaurantID int, phone string, text string) error {
	num := normalizeWhatsAppNumber(phone)
	if num == "" {
		return errors.New("telefono invalido")
	}
	uazURL, uazToken := s.uazapiBaseAndToken(ctx, restaurantID)
	if uazURL == "" {
		return errors.New("uazapi no configurado")
	}
	sendURL := strings.TrimRight(uazURL, "/") + "/send/text"
	if uazToken != "" {
		sendURL += "?token=" + url.QueryEscape(uazToken)
	}
	body, code, err := sendUazAPI(ctx, sendURL, map[string]any{
		"number": num,
		"text":   text,
	})
	if err != nil {
		return err
	}
	if code != http.StatusOK && code != http.StatusCreated {
		return fmt.Errorf("uazapi http %d: %s", code, body)
	}
	return nil
}

func normalizeWhatsAppNumber(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var b strings.Builder
	for _, ch := range raw {
		if ch >= '0' && ch <= '9' {
			b.WriteRune(ch)
		}
	}
	digits := b.String()
	if digits == "" {
		return ""
	}
	for strings.HasPrefix(digits, "00") {
		digits = strings.TrimPrefix(digits, "00")
	}
	if len(digits) == 9 {
		digits = "34" + digits
	}
	if len(digits) < 10 || len(digits) > 15 {
		return ""
	}
	return digits
}

func (s *Server) restaurantNameFallback(ctx context.Context, restaurantID int) string {
	if branding, err := s.loadRestaurantBranding(ctx, restaurantID); err == nil {
		if brand := strings.TrimSpace(branding.BrandName); brand != "" {
			return brand
		}
	}
	var name string
	if err := s.db.QueryRowContext(ctx, `SELECT name FROM restaurants WHERE id = ? LIMIT 1`, restaurantID).Scan(&name); err == nil {
		if n := strings.TrimSpace(name); n != "" {
			return n
		}
	}
	return "Villacarmen"
}

func (s *Server) loadMemberAccessRecord(ctx context.Context, restaurantID int, memberID int) (boMemberAccessRecord, error) {
	var rec boMemberAccessRecord
	rec.RestaurantID = restaurantID
	rec.MemberID = memberID

	var (
		boUserID  sql.NullInt64
		email     sql.NullString
		phone     sql.NullString
		dni       sql.NullString
		photoURL  sql.NullString
		username  sql.NullString
		roleSlug  sql.NullString
		roleLabel sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT
			m.id,
			m.bo_user_id,
			m.first_name,
			m.last_name,
			m.email,
			m.phone,
			m.dni,
			m.photo_url,
			u.username,
			ur.role,
			br.label
		FROM restaurant_members m
		LEFT JOIN bo_users u ON u.id = m.bo_user_id
		LEFT JOIN bo_user_restaurants ur ON ur.user_id = m.bo_user_id AND ur.restaurant_id = m.restaurant_id
		LEFT JOIN bo_roles br ON br.slug = ur.role
		WHERE m.id = ? AND m.restaurant_id = ? AND m.is_active = 1
		LIMIT 1
	`, memberID, restaurantID).Scan(
		&rec.MemberID,
		&boUserID,
		&rec.FirstName,
		&rec.LastName,
		&email,
		&phone,
		&dni,
		&photoURL,
		&username,
		&roleSlug,
		&roleLabel,
	)
	if err != nil {
		return boMemberAccessRecord{}, err
	}
	rec.BOUserID = int(boUserID.Int64)
	rec.Email = strings.TrimSpace(email.String)
	rec.Phone = strings.TrimSpace(phone.String)
	rec.DNI = strings.TrimSpace(dni.String)
	rec.PhotoURL = strings.TrimSpace(photoURL.String)
	rec.Username = strings.TrimSpace(username.String)
	rec.RoleSlug = normalizeBORole(roleSlug.String)
	if rec.RoleSlug == "" {
		rec.RoleSlug = "admin"
	}
	rec.RoleLabel = strings.TrimSpace(roleLabel.String)
	if rec.RoleLabel == "" {
		rec.RoleLabel = defaultRoleLabel(rec.RoleSlug)
	}
	return rec, nil
}

func (s *Server) handleBOMemberInvitationResend(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	memberID, err := parseBOIDParam(r, "id")
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "id invalido"})
		return
	}

	rec, err := s.loadMemberAccessRecord(r.Context(), a.ActiveRestaurantID, memberID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"success": false, "message": "Miembro no encontrado"})
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo miembro")
		return
	}

	if strings.TrimSpace(rec.Email) == "" && strings.TrimSpace(rec.Phone) == "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "El miembro no tiene email ni telefono para reenviar invitacion"})
		return
	}

	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error iniciando transaccion")
		return
	}
	defer func() { _ = tx.Rollback() }()

	userID := rec.BOUserID
	username := rec.Username
	email := rec.Email
	if userID <= 0 {
		user, uerr := s.ensureBOUserForMemberTx(r.Context(), tx, boEnsureUserInput{
			RestaurantID:      a.ActiveRestaurantID,
			FirstName:         rec.FirstName,
			LastName:          rec.LastName,
			Email:             rec.Email,
			Phone:             rec.Phone,
			Username:          rec.Username,
			ManualCredentials: false,
		})
		if uerr != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error creando usuario backoffice")
			return
		}
		userID = user.UserID
		username = user.Username
		email = user.Email
		if _, err := tx.ExecContext(r.Context(), `
			UPDATE restaurant_members
			SET bo_user_id = ?
			WHERE id = ? AND restaurant_id = ?
		`, userID, rec.MemberID, a.ActiveRestaurantID); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error vinculando usuario")
			return
		}
	}

	if _, err := tx.ExecContext(r.Context(), `
		INSERT INTO bo_user_restaurants (user_id, restaurant_id, role)
		VALUES (?, ?, ?)
		ON DUPLICATE KEY UPDATE role = VALUES(role)
	`, userID, a.ActiveRestaurantID, rec.RoleSlug); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error asignando rol")
		return
	}

	token, expiresAt, err := s.createMemberInvitationTokenTx(r.Context(), tx, boCreateMemberInvitationInput{
		RestaurantID:     a.ActiveRestaurantID,
		MemberID:         rec.MemberID,
		BOUserID:         userID,
		RoleSlug:         rec.RoleSlug,
		CreatedByUserID:  a.User.ID,
		InvalidateReason: "resend",
	})
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error creando invitacion")
		return
	}

	if err := tx.Commit(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error guardando invitacion")
		return
	}

	invURL := buildBackofficeAbsoluteURL(r, "/invitacion/"+token)
	delivery := s.sendMemberInvitation(r.Context(), a.ActiveRestaurantID, email, rec.Phone, invURL)

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"member": map[string]any{
			"id":       rec.MemberID,
			"boUserId": userID,
			"username": username,
		},
		"invitation": map[string]any{
			"expiresAt": expiresAt.Format(time.RFC3339),
			"delivery":  delivery,
		},
	})
}

func (s *Server) handleBOMemberPasswordResetSend(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	memberID, err := parseBOIDParam(r, "id")
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "id invalido"})
		return
	}

	rec, err := s.loadMemberAccessRecord(r.Context(), a.ActiveRestaurantID, memberID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"success": false, "message": "Miembro no encontrado"})
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo miembro")
		return
	}
	if strings.TrimSpace(rec.Email) == "" && strings.TrimSpace(rec.Phone) == "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "El miembro no tiene email ni telefono para restablecer password"})
		return
	}

	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error iniciando transaccion")
		return
	}
	defer func() { _ = tx.Rollback() }()

	userID := rec.BOUserID
	email := rec.Email
	if userID <= 0 {
		user, uerr := s.ensureBOUserForMemberTx(r.Context(), tx, boEnsureUserInput{
			RestaurantID:      a.ActiveRestaurantID,
			FirstName:         rec.FirstName,
			LastName:          rec.LastName,
			Email:             rec.Email,
			Phone:             rec.Phone,
			Username:          rec.Username,
			ManualCredentials: false,
		})
		if uerr != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error creando usuario backoffice")
			return
		}
		userID = user.UserID
		email = user.Email
		if _, err := tx.ExecContext(r.Context(), `
			UPDATE restaurant_members
			SET bo_user_id = ?
			WHERE id = ? AND restaurant_id = ?
		`, userID, rec.MemberID, a.ActiveRestaurantID); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error vinculando usuario")
			return
		}
	}

	token, expiresAt, err := s.createPasswordResetTokenTx(r.Context(), tx, a.ActiveRestaurantID, rec.MemberID, userID, a.User.ID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error creando token de reset")
		return
	}
	if err := tx.Commit(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error guardando token de reset")
		return
	}

	resetURL := buildBackofficeAbsoluteURL(r, "/reset-password/"+token)
	delivery := s.sendMemberPasswordReset(r.Context(), a.ActiveRestaurantID, email, rec.Phone, resetURL)

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"reset": map[string]any{
			"expiresAt": expiresAt.Format(time.RFC3339),
			"delivery":  delivery,
		},
	})
}

func (s *Server) fetchInvitationByToken(ctx context.Context, token string) (boInvitationRecord, error) {
	tokenSHA := sha256Hex(strings.TrimSpace(token))
	if tokenSHA == "" {
		return boInvitationRecord{}, sql.ErrNoRows
	}
	var rec boInvitationRecord
	var (
		email    sql.NullString
		phone    sql.NullString
		dni      sql.NullString
		photoURL sql.NullString
		username sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT
			t.id,
			t.token_sha256,
			t.onboarding_guid,
			t.expires_at,
			t.restaurant_id,
			t.member_id,
			t.bo_user_id,
			m.first_name,
			m.last_name,
			m.email,
			m.phone,
			m.dni,
			m.photo_url,
			u.username,
			t.role_slug,
			COALESCE(br.label, t.role_slug)
		FROM bo_member_invitation_tokens t
		JOIN restaurant_members m ON m.id = t.member_id AND m.restaurant_id = t.restaurant_id AND m.is_active = 1
		JOIN bo_users u ON u.id = t.bo_user_id
		LEFT JOIN bo_roles br ON br.slug = t.role_slug
		WHERE t.token_sha256 = ?
			AND t.used_at IS NULL
			AND t.invalidated_at IS NULL
			AND t.expires_at > NOW()
		LIMIT 1
	`, tokenSHA).Scan(
		&rec.TokenID,
		&rec.TokenSHA,
		&rec.OnboardingGUID,
		&rec.ExpiresAt,
		&rec.RestaurantID,
		&rec.MemberID,
		&rec.BOUserID,
		&rec.FirstName,
		&rec.LastName,
		&email,
		&phone,
		&dni,
		&photoURL,
		&username,
		&rec.RoleSlug,
		&rec.RoleLabel,
	)
	if err != nil {
		return boInvitationRecord{}, err
	}
	rec.Email = strings.TrimSpace(email.String)
	rec.Phone = strings.TrimSpace(phone.String)
	rec.DNI = strings.TrimSpace(dni.String)
	rec.PhotoURL = strings.TrimSpace(photoURL.String)
	rec.Username = strings.TrimSpace(username.String)
	if rec.RoleSlug == "" {
		rec.RoleSlug = "admin"
	}
	if rec.RoleLabel == "" {
		rec.RoleLabel = defaultRoleLabel(rec.RoleSlug)
	}
	return rec, nil
}

func (s *Server) fetchInvitationByGUID(ctx context.Context, guid string) (boInvitationRecord, error) {
	guid = strings.TrimSpace(guid)
	if guid == "" {
		return boInvitationRecord{}, sql.ErrNoRows
	}
	var rec boInvitationRecord
	var (
		email    sql.NullString
		phone    sql.NullString
		dni      sql.NullString
		photoURL sql.NullString
		username sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT
			t.id,
			t.token_sha256,
			t.onboarding_guid,
			t.expires_at,
			t.restaurant_id,
			t.member_id,
			t.bo_user_id,
			m.first_name,
			m.last_name,
			m.email,
			m.phone,
			m.dni,
			m.photo_url,
			u.username,
			t.role_slug,
			COALESCE(br.label, t.role_slug)
		FROM bo_member_invitation_tokens t
		JOIN restaurant_members m ON m.id = t.member_id AND m.restaurant_id = t.restaurant_id AND m.is_active = 1
		JOIN bo_users u ON u.id = t.bo_user_id
		LEFT JOIN bo_roles br ON br.slug = t.role_slug
		WHERE t.onboarding_guid = ?
			AND t.used_at IS NULL
			AND t.invalidated_at IS NULL
			AND t.expires_at > NOW()
		LIMIT 1
	`, guid).Scan(
		&rec.TokenID,
		&rec.TokenSHA,
		&rec.OnboardingGUID,
		&rec.ExpiresAt,
		&rec.RestaurantID,
		&rec.MemberID,
		&rec.BOUserID,
		&rec.FirstName,
		&rec.LastName,
		&email,
		&phone,
		&dni,
		&photoURL,
		&username,
		&rec.RoleSlug,
		&rec.RoleLabel,
	)
	if err != nil {
		return boInvitationRecord{}, err
	}
	rec.Email = strings.TrimSpace(email.String)
	rec.Phone = strings.TrimSpace(phone.String)
	rec.DNI = strings.TrimSpace(dni.String)
	rec.PhotoURL = strings.TrimSpace(photoURL.String)
	rec.Username = strings.TrimSpace(username.String)
	if rec.RoleSlug == "" {
		rec.RoleSlug = "admin"
	}
	if rec.RoleLabel == "" {
		rec.RoleLabel = defaultRoleLabel(rec.RoleSlug)
	}
	return rec, nil
}

func (s *Server) ensureInvitationGUID(ctx context.Context, invitationID int64, current string) (string, error) {
	current = strings.TrimSpace(current)
	if current != "" {
		return current, nil
	}
	guid, err := newUUIDv4()
	if err != nil {
		return "", err
	}
	_, err = s.db.ExecContext(ctx, `
		UPDATE bo_member_invitation_tokens
		SET onboarding_guid = ?
		WHERE id = ? AND onboarding_guid IS NULL
	`, guid, invitationID)
	if err != nil {
		return "", err
	}
	var stored sql.NullString
	if err := s.db.QueryRowContext(ctx, `
		SELECT onboarding_guid
		FROM bo_member_invitation_tokens
		WHERE id = ?
		LIMIT 1
	`, invitationID).Scan(&stored); err != nil {
		return "", err
	}
	if strings.TrimSpace(stored.String) == "" {
		return "", errors.New("no se pudo asignar guid de onboarding")
	}
	return strings.TrimSpace(stored.String), nil
}

func newUUIDv4() (string, error) {
	var b [16]byte
	if _, err := crand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

func (s *Server) handleBOInvitationValidate(w http.ResponseWriter, r *http.Request) {
	var req boInvitationTokenRequest
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid JSON"})
		return
	}
	token := strings.TrimSpace(req.Token)
	if token == "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Token requerido"})
		return
	}
	rec, err := s.fetchInvitationByToken(r.Context(), token)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invitacion invalida o expirada"})
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "Error validando invitacion")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"invitation": map[string]any{
			"memberId":          rec.MemberID,
			"firstName":         rec.FirstName,
			"lastName":          rec.LastName,
			"email":             nullableString(rec.Email),
			"dni":               nullableString(rec.DNI),
			"phone":             nullableString(rec.Phone),
			"photoUrl":          nullableString(rec.PhotoURL),
			"roleSlug":          rec.RoleSlug,
			"roleLabel":         rec.RoleLabel,
			"expiresAt":         rec.ExpiresAt.Format(time.RFC3339),
			"hasOnboardingGuid": strings.TrimSpace(rec.OnboardingGUID) != "",
		},
	})
}

func (s *Server) handleBOInvitationOnboardingStart(w http.ResponseWriter, r *http.Request) {
	var req boInvitationTokenRequest
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid JSON"})
		return
	}
	token := strings.TrimSpace(req.Token)
	if token == "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Token requerido"})
		return
	}
	rec, err := s.fetchInvitationByToken(r.Context(), token)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invitacion invalida o expirada"})
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "Error preparando onboarding")
		return
	}
	guid, err := s.ensureInvitationGUID(r.Context(), rec.TokenID, rec.OnboardingGUID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error creando onboarding")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":        true,
		"onboardingGuid": guid,
		"member": map[string]any{
			"id":        rec.MemberID,
			"firstName": rec.FirstName,
			"lastName":  rec.LastName,
			"email":     nullableString(rec.Email),
			"dni":       nullableString(rec.DNI),
			"phone":     nullableString(rec.Phone),
			"photoUrl":  nullableString(rec.PhotoURL),
			"username":  nullableString(rec.Username),
			"roleSlug":  rec.RoleSlug,
			"roleLabel": rec.RoleLabel,
		},
	})
}

func (s *Server) handleBOInvitationOnboardingGet(w http.ResponseWriter, r *http.Request) {
	guid := strings.TrimSpace(chi.URLParam(r, "guid"))
	if guid == "" {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "guid invalido"})
		return
	}
	rec, err := s.fetchInvitationByGUID(r.Context(), guid)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Onboarding invalido o expirado"})
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo onboarding")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"member": map[string]any{
			"id":        rec.MemberID,
			"firstName": rec.FirstName,
			"lastName":  rec.LastName,
			"email":     nullableString(rec.Email),
			"dni":       nullableString(rec.DNI),
			"phone":     nullableString(rec.Phone),
			"photoUrl":  nullableString(rec.PhotoURL),
			"username":  nullableString(rec.Username),
			"roleSlug":  rec.RoleSlug,
			"roleLabel": rec.RoleLabel,
		},
		"expiresAt": rec.ExpiresAt.Format(time.RFC3339),
	})
}

func (s *Server) handleBOInvitationOnboardingProfile(w http.ResponseWriter, r *http.Request) {
	guid := strings.TrimSpace(chi.URLParam(r, "guid"))
	if guid == "" {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "guid invalido"})
		return
	}
	rec, err := s.fetchInvitationByGUID(r.Context(), guid)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Onboarding invalido o expirado"})
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo onboarding")
		return
	}

	var req boInvitationOnboardingProfileRequest
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid JSON"})
		return
	}

	firstName := strings.TrimSpace(rec.FirstName)
	if req.FirstName != nil {
		firstName = strings.TrimSpace(*req.FirstName)
	}
	lastName := strings.TrimSpace(rec.LastName)
	if req.LastName != nil {
		lastName = strings.TrimSpace(*req.LastName)
	}
	if firstName == "" || lastName == "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Nombre y apellidos son obligatorios"})
		return
	}
	photoURL := strings.TrimSpace(rec.PhotoURL)
	if req.PhotoURL != nil {
		photoURL = strings.TrimSpace(*req.PhotoURL)
	}

	_, err = s.db.ExecContext(r.Context(), `
		UPDATE restaurant_members
		SET first_name = ?, last_name = ?, photo_url = ?
		WHERE id = ? AND restaurant_id = ? AND is_active = 1
	`, firstName, lastName, nullableString(photoURL), rec.MemberID, rec.RestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error guardando perfil")
		return
	}

	member, err := s.getBOMemberByID(r.Context(), rec.RestaurantID, rec.MemberID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo perfil")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"member":  member,
	})
}

func (s *Server) handleBOInvitationOnboardingAvatar(w http.ResponseWriter, r *http.Request) {
	guid := strings.TrimSpace(chi.URLParam(r, "guid"))
	if guid == "" {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "guid invalido"})
		return
	}
	rec, err := s.fetchInvitationByGUID(r.Context(), guid)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Onboarding invalido o expirado"})
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo onboarding")
		return
	}

	if !s.bunnyMembersConfigured() {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Storage de avatar no configurado en servidor"})
		return
	}
	if err := r.ParseMultipartForm(boMemberAvatarReadLimit); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "No se pudo procesar el fichero"})
		return
	}

	f, _, err := r.FormFile("avatar")
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Selecciona una imagen"})
		return
	}
	defer f.Close()

	raw, err := io.ReadAll(io.LimitReader(f, boMemberAvatarReadLimit+1))
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "No se pudo leer la imagen")
		return
	}
	if len(raw) == 0 {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "La imagen esta vacia"})
		return
	}
	if len(raw) > boMemberAvatarReadLimit {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Imagen demasiado grande"})
		return
	}
	contentType := strings.ToLower(strings.TrimSpace(http.DetectContentType(raw)))
	if !strings.HasPrefix(contentType, "image/webp") {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Formato invalido: sube una imagen WEBP"})
		return
	}
	if len(raw) > boMemberAvatarMaxBytes {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "La imagen debe pesar como maximo 200KB"})
		return
	}

	objectPath := buildAvatarObjectPath(rec.RestaurantID, rec.MemberID, rec.BOUserID)
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := s.bunnyMembersPut(ctx, objectPath, raw, "image/webp"); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "No se pudo subir el avatar")
		return
	}
	avatarURL := s.bunnyMembersPullURL(objectPath)
	_, err = s.db.ExecContext(r.Context(), `
		UPDATE restaurant_members
		SET photo_url = ?
		WHERE id = ? AND restaurant_id = ? AND is_active = 1
	`, avatarURL, rec.MemberID, rec.RestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "No se pudo guardar el avatar")
		return
	}
	member, err := s.getBOMemberByID(r.Context(), rec.RestaurantID, rec.MemberID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo perfil")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":   true,
		"avatarUrl": avatarURL,
		"member":    member,
	})
}

func buildAvatarObjectPath(restaurantID int, memberID int, boUserID int) string {
	targetID := memberID
	if boUserID > 0 {
		targetID = boUserID
	}
	return "images/avatars/" + strconv.Itoa(restaurantID) + "/user_" + strconv.Itoa(targetID) + ".webp"
}

func (s *Server) handleBOInvitationOnboardingPassword(w http.ResponseWriter, r *http.Request) {
	guid := strings.TrimSpace(chi.URLParam(r, "guid"))
	if guid == "" {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "guid invalido"})
		return
	}
	rec, err := s.fetchInvitationByGUID(r.Context(), guid)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Onboarding invalido o expirado"})
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo onboarding")
		return
	}

	var req boInvitationOnboardingPasswordRequest
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid JSON"})
		return
	}
	password := strings.TrimSpace(req.Password)
	confirm := strings.TrimSpace(req.ConfirmPassword)
	if confirm == "" {
		confirm = strings.TrimSpace(req.PasswordRepeat)
	}
	if password == "" || confirm == "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Password requerida"})
		return
	}
	if password != confirm {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Las passwords no coinciden"})
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "No se pudo guardar password")
		return
	}

	ua := strings.TrimSpace(r.Header.Get("User-Agent"))
	if len(ua) > 250 {
		ua = ua[:250]
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
	`, string(hash), rec.BOUserID); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "No se pudo actualizar password")
		return
	}

	res, err := tx.ExecContext(r.Context(), `
		UPDATE bo_member_invitation_tokens
		SET used_at = NOW(), used_ip = ?, used_user_agent = ?
		WHERE id = ?
			AND used_at IS NULL
			AND invalidated_at IS NULL
			AND expires_at > NOW()
	`, clientIP(r), ua, rec.TokenID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "No se pudo finalizar onboarding")
		return
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Onboarding invalido o expirado"})
		return
	}

	_, _ = tx.ExecContext(r.Context(), `
		UPDATE bo_password_reset_tokens
		SET invalidated_at = NOW(), invalidated_reason = 'password-updated'
		WHERE bo_user_id = ?
			AND used_at IS NULL
			AND invalidated_at IS NULL
			AND expires_at > NOW()
	`, rec.BOUserID)

	if err := tx.Commit(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error finalizando onboarding")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"next":    "/login",
	})
}

func (s *Server) fetchPasswordResetByToken(ctx context.Context, token string) (boPasswordResetRecord, error) {
	tokenSHA := sha256Hex(strings.TrimSpace(token))
	if tokenSHA == "" {
		return boPasswordResetRecord{}, sql.ErrNoRows
	}
	var rec boPasswordResetRecord
	var (
		email    sql.NullString
		phone    sql.NullString
		dni      sql.NullString
		photoURL sql.NullString
		username sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT
			t.id,
			t.token_sha256,
			t.expires_at,
			t.restaurant_id,
			t.member_id,
			t.bo_user_id,
			m.first_name,
			m.last_name,
			m.email,
			m.phone,
			m.dni,
			m.photo_url,
			u.username,
			ur.role,
			COALESCE(br.label, ur.role)
		FROM bo_password_reset_tokens t
		JOIN restaurant_members m ON m.id = t.member_id AND m.restaurant_id = t.restaurant_id AND m.is_active = 1
		JOIN bo_users u ON u.id = t.bo_user_id
		LEFT JOIN bo_user_restaurants ur ON ur.user_id = t.bo_user_id AND ur.restaurant_id = t.restaurant_id
		LEFT JOIN bo_roles br ON br.slug = ur.role
		WHERE t.token_sha256 = ?
			AND t.used_at IS NULL
			AND t.invalidated_at IS NULL
			AND t.expires_at > NOW()
		LIMIT 1
	`, tokenSHA).Scan(
		&rec.TokenID,
		&rec.TokenSHA,
		&rec.ExpiresAt,
		&rec.RestaurantID,
		&rec.MemberID,
		&rec.BOUserID,
		&rec.FirstName,
		&rec.LastName,
		&email,
		&phone,
		&dni,
		&photoURL,
		&username,
		&rec.RoleSlug,
		&rec.RoleLabel,
	)
	if err != nil {
		return boPasswordResetRecord{}, err
	}
	rec.Email = strings.TrimSpace(email.String)
	rec.Phone = strings.TrimSpace(phone.String)
	rec.DNI = strings.TrimSpace(dni.String)
	rec.PhotoURL = strings.TrimSpace(photoURL.String)
	rec.Username = strings.TrimSpace(username.String)
	rec.RoleSlug = normalizeBORole(rec.RoleSlug)
	if rec.RoleSlug == "" {
		rec.RoleSlug = "admin"
	}
	if rec.RoleLabel == "" {
		rec.RoleLabel = defaultRoleLabel(rec.RoleSlug)
	}
	return rec, nil
}

func (s *Server) handleBOPasswordResetValidate(w http.ResponseWriter, r *http.Request) {
	var req boInvitationTokenRequest
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid JSON"})
		return
	}
	token := strings.TrimSpace(req.Token)
	if token == "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Token requerido"})
		return
	}
	rec, err := s.fetchPasswordResetByToken(r.Context(), token)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Token de reset invalido o expirado"})
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "Error validando token")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"reset": map[string]any{
			"memberId":  rec.MemberID,
			"firstName": rec.FirstName,
			"lastName":  rec.LastName,
			"email":     nullableString(rec.Email),
			"username":  nullableString(rec.Username),
			"expiresAt": rec.ExpiresAt.Format(time.RFC3339),
		},
	})
}

func (s *Server) handleBOPasswordResetConfirm(w http.ResponseWriter, r *http.Request) {
	var req boPasswordResetConfirmRequest
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid JSON"})
		return
	}
	token := strings.TrimSpace(req.Token)
	if token == "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Token requerido"})
		return
	}
	rec, err := s.fetchPasswordResetByToken(r.Context(), token)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Token de reset invalido o expirado"})
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "Error validando token")
		return
	}

	password := strings.TrimSpace(req.Password)
	confirm := strings.TrimSpace(req.ConfirmPassword)
	if confirm == "" {
		confirm = strings.TrimSpace(req.PasswordRepeat)
	}
	if password == "" || confirm == "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Password requerida"})
		return
	}
	if password != confirm {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Las passwords no coinciden"})
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "No se pudo guardar password")
		return
	}

	ua := strings.TrimSpace(r.Header.Get("User-Agent"))
	if len(ua) > 250 {
		ua = ua[:250]
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
	`, string(hash), rec.BOUserID); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "No se pudo actualizar password")
		return
	}

	res, err := tx.ExecContext(r.Context(), `
		UPDATE bo_password_reset_tokens
		SET used_at = NOW(), used_ip = ?, used_user_agent = ?
		WHERE id = ?
			AND used_at IS NULL
			AND invalidated_at IS NULL
			AND expires_at > NOW()
	`, clientIP(r), ua, rec.TokenID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "No se pudo finalizar reset")
		return
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Token de reset invalido o expirado"})
		return
	}

	_, _ = tx.ExecContext(r.Context(), `
		UPDATE bo_member_invitation_tokens
		SET invalidated_at = NOW(), invalidated_reason = 'password-reset'
		WHERE bo_user_id = ?
			AND used_at IS NULL
			AND invalidated_at IS NULL
			AND expires_at > NOW()
	`, rec.BOUserID)

	if err := tx.Commit(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error finalizando reset")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"next":    "/login",
	})
}
