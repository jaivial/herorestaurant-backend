package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"preactvillacarmen/internal/httpx"
)

type memberWhatsAppVerificationRequest struct {
	Code string `json:"code"`
}

func verificationDigest(memberID int, phone, code string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%s:%s", memberID, normalizeWhatsAppNumber(phone), strings.TrimSpace(code))))
	return hex.EncodeToString(sum[:])
}
func newWhatsAppVerificationCode() (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", int((uint32(b[0])<<24|uint32(b[1])<<16|uint32(b[2])<<8|uint32(b[3]))%1000000)), nil
}

func (s *Server) memberWhatsAppEnabled(ctx context.Context, restaurantID int, phone string) bool {
	entitled, err := s.hasActiveRecurringFeature(ctx, restaurantID, boPremiumWhatsAppFeatureKey)
	if err != nil || !entitled {
		return false
	}
	number := normalizeWhatsAppNumber(phone)
	if number == "" {
		return false
	}
	var verified bool
	err = s.db.QueryRowContext(ctx, `SELECT whatsapp_verified_at IS NOT NULL FROM restaurant_members WHERE restaurant_id=? AND is_active=1 AND (whatsapp_number=? OR phone=?) LIMIT 1`, restaurantID, "+"+number, "+"+number).Scan(&verified)
	return err == nil && verified
}

func (s *Server) sendVerifiedMemberWhatsApp(ctx context.Context, restaurantID int, phone, text string) error {
	if !s.memberWhatsAppEnabled(ctx, restaurantID, phone) {
		return errors.New("whatsapp no verificado o suscripcion inactiva")
	}
	gateway, ok := s.botGatewayFor(ctx, restaurantID)
	if !ok {
		return errors.New("whatsapp no conectado")
	}
	return gateway.SendText(ctx, normalizeWhatsAppNumber(phone), text)
}
func (s *Server) handleBOMemberWhatsAppVerificationSend(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	active, err := s.hasActiveRecurringFeature(r.Context(), a.ActiveRestaurantID, boPremiumWhatsAppFeatureKey)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "No se pudo validar suscripcion")
		return
	}
	if !active {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "code": "NEEDS_SUBSCRIPTION", "message": "Necesitas una suscripcion activa de WhatsApp Pack"})
		return
	}
	id, err := parseBOIDParam(r, "id")
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "id invalido")
		return
	}
	var phone sql.NullString
	if err = s.db.QueryRowContext(r.Context(), `SELECT COALESCE(NULLIF(whatsapp_number,''), phone) FROM restaurant_members WHERE id=? AND restaurant_id=? AND is_active=1`, id, a.ActiveRestaurantID).Scan(&phone); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteError(w, http.StatusNotFound, "Miembro no encontrado")
		} else {
			httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo miembro")
		}
		return
	}
	number := normalizeWhatsAppNumber(phone.String)
	if number == "" {
		httpx.WriteError(w, http.StatusBadRequest, "Telefono invalido")
		return
	}
	code, err := newWhatsAppVerificationCode()
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "No se pudo generar codigo")
		return
	}
	expires := time.Now().Add(10 * time.Minute)
	digest := verificationDigest(id, number, code)
	_, err = s.db.ExecContext(r.Context(), `UPDATE restaurant_members SET whatsapp_verification_digest=?, whatsapp_verification_expires_at=?, whatsapp_verification_attempts=0 WHERE id=? AND restaurant_id=? AND is_active=1`, digest, expires, id, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "No se pudo guardar codigo")
		return
	}
	gateway, connected := s.botGatewayFor(r.Context(), a.ActiveRestaurantID)
	if !connected || gateway.SendText(r.Context(), number, "Tu codigo de verificacion de WhatsApp es: "+code+". Caduca en 10 minutos.") != nil {
		httpx.WriteJSON(w, http.StatusBadGateway, map[string]any{"success": false, "code": "WHATSAPP_SEND_FAILED", "message": "No se pudo enviar el codigo"})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "expiresAt": expires.Format(time.RFC3339)})
}
func (s *Server) handleBOMemberWhatsAppVerificationConfirm(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	id, err := parseBOIDParam(r, "id")
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "id invalido")
		return
	}
	var req memberWhatsAppVerificationRequest
	if err = readJSONBody(r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}
	code := strings.TrimSpace(req.Code)
	if len(code) != 6 {
		httpx.WriteError(w, http.StatusBadRequest, "Codigo invalido")
		return
	}
	var phone sql.NullString
	var digest sql.NullString
	var expires sql.NullTime
	var attempts int
	err = s.db.QueryRowContext(r.Context(), `SELECT COALESCE(NULLIF(whatsapp_number,''), phone), whatsapp_verification_digest, whatsapp_verification_expires_at, whatsapp_verification_attempts FROM restaurant_members WHERE id=? AND restaurant_id=? AND is_active=1`, id, a.ActiveRestaurantID).Scan(&phone, &digest, &expires, &attempts)
	if errors.Is(err, sql.ErrNoRows) {
		httpx.WriteError(w, http.StatusNotFound, "Miembro no encontrado")
		return
	}
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo miembro")
		return
	}
	if attempts >= 5 || !expires.Valid || time.Now().After(expires.Time) || digest.String != verificationDigest(id, phone.String, code) {
		_, _ = s.db.ExecContext(r.Context(), `UPDATE restaurant_members SET whatsapp_verification_attempts=whatsapp_verification_attempts+1 WHERE id=? AND restaurant_id=?`, id, a.ActiveRestaurantID)
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "code": "INVALID_CODE", "message": "Codigo invalido o caducado"})
		return
	}
	_, err = s.db.ExecContext(r.Context(), `UPDATE restaurant_members SET whatsapp_verified_at=NOW(), whatsapp_verification_digest=NULL, whatsapp_verification_expires_at=NULL, whatsapp_verification_attempts=0 WHERE id=? AND restaurant_id=?`, id, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "No se pudo verificar telefono")
		return
	}
	// Verification itself is the opt-in boundary: send a minimal welcome only
	// after the number is verified and the paid WhatsApp feature is active.
	if s.memberWhatsAppEnabled(r.Context(), a.ActiveRestaurantID, phone.String) {
		if gateway, connected := s.botGatewayFor(r.Context(), a.ActiveRestaurantID); connected {
			_ = gateway.SendText(r.Context(), normalizeWhatsAppNumber(phone.String), "Bienvenido/a. Tu WhatsApp ha sido verificado correctamente.")
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "verified": true})
}
