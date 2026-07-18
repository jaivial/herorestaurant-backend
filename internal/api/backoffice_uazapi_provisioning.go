package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"preactvillacarmen/internal/httpx"
)

type boMembersWhatsAppConnectRequest struct {
	Phone string `json:"phone"`
}

// errUAZAPINoCapacity is returned when the server pool has no active host with
// remaining capacity, so callers can surface a distinct "pool full" error.
var errUAZAPINoCapacity = errors.New("no hay servidores UAZAPI activos con capacidad")

type boMembersWhatsAppDisconnectRequest struct {
	DeleteInstance bool `json:"delete_instance"`
}

type uazapiServerRecord struct {
	ID         int64
	Name       string
	BaseURL    string
	AdminToken string
	Capacity   int
	UsedCount  int
}

type uazapiInstanceRecord struct {
	ID                 int64
	RestaurantID       int
	ServerID           int64
	ServerBaseURL      string
	InstanceName       string
	InstanceToken      string
	ProviderInstanceID string
	ConnectedPhone     string
	Status             string
	QRPayload          string
	PairCode           string
	UpdatedAt          string
}

func (s *Server) handleBOMembersWhatsAppConnect(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req boMembersWhatsAppConnectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeBOPremiumError(w, http.StatusBadRequest, "BAD_REQUEST", "JSON invalido")
		return
	}

	active, err := s.hasActiveRecurringFeature(r.Context(), a.ActiveRestaurantID, boPremiumWhatsAppFeatureKey)
	if err != nil {
		writeBOPremiumError(w, http.StatusInternalServerError, "WHATSAPP_SUBSCRIPTION_CHECK_FAILED", "No se pudo validar suscripcion")
		return
	}
	if !active {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"code":    "NEEDS_SUBSCRIPTION",
			"message": "Necesitas una suscripcion activa de WhatsApp Pack",
		})
		return
	}

	connection, err := s.provisionAndConnectRestaurantWhatsApp(r.Context(), a.ActiveRestaurantID, strings.TrimSpace(req.Phone))
	if err != nil {
		if errors.Is(err, errUAZAPINoCapacity) {
			writeBOPremiumError(w, http.StatusServiceUnavailable, "WHATSAPP_POOL_FULL", "No hay servidores de WhatsApp disponibles en este momento. Inténtalo más tarde.")
			return
		}
		writeBOPremiumError(w, http.StatusBadGateway, "WHATSAPP_CONNECT_FAILED", "No se pudo iniciar la conexion de WhatsApp")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":    true,
		"message":    whatsappConnectionMessage(connection),
		"connection": connection,
		"connected":  anyToBool(connection["connected"]),
	})
}

func (s *Server) handleBOMembersWhatsAppConnectionStatus(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	rec, found, err := s.loadRestaurantUAZAPIInstance(r.Context(), a.ActiveRestaurantID)
	if err != nil {
		writeBOPremiumError(w, http.StatusInternalServerError, "WHATSAPP_STATUS_FAILED", "No se pudo cargar la instancia de WhatsApp")
		return
	}
	if !found {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success":    true,
			"connected":  false,
			"message":    "No hay una instancia de WhatsApp provisionada",
			"connection": nil,
		})
		return
	}

	connection, err := s.refreshRestaurantUAZAPIConnectionStatus(r.Context(), a.ActiveRestaurantID)
	if err != nil {
		fallback := s.whatsappConnectionPayload(rec)
		fallback["warning"] = "No se pudo refrescar estado en este momento"
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success":    true,
			"connected":  anyToBool(fallback["connected"]),
			"message":    whatsappConnectionMessage(fallback),
			"connection": fallback,
		})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":    true,
		"connected":  anyToBool(connection["connected"]),
		"message":    whatsappConnectionMessage(connection),
		"connection": connection,
	})
}

func (s *Server) handleBOMembersWhatsAppDisconnect(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req boMembersWhatsAppDisconnectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeBOPremiumError(w, http.StatusBadRequest, "BAD_REQUEST", "JSON invalido")
		return
	}

	rec, found, err := s.loadRestaurantUAZAPIInstance(r.Context(), a.ActiveRestaurantID)
	if err != nil {
		writeBOPremiumError(w, http.StatusInternalServerError, "WHATSAPP_DISCONNECT_FAILED", "No se pudo cargar la instancia de WhatsApp")
		return
	}
	if !found {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success":   true,
			"message":   "No habia instancia activa para desconectar",
			"connected": false,
		})
		return
	}

	_, disconnectCode, _, disconnectErr := s.uazapiInstanceRequest(r.Context(), rec.ServerBaseURL, rec.InstanceToken, http.MethodPost, "/instance/disconnect", map[string]any{})
	if disconnectErr != nil || (disconnectCode < 200 || disconnectCode >= 300) {
		// Best effort: continue cleanup locally.
	}

	if req.DeleteInstance {
		_, deleteCode, _, _ := s.uazapiInstanceRequest(r.Context(), rec.ServerBaseURL, rec.InstanceToken, http.MethodDelete, "/instance", nil)
		if deleteCode >= 200 && deleteCode < 300 {
			// Best effort remote deletion succeeded.
		}
		if _, err := s.db.ExecContext(r.Context(), `DELETE FROM restaurant_uazapi_instances WHERE restaurant_id = ?`, a.ActiveRestaurantID); err != nil && !isSQLSchemaError(err) {
			writeBOPremiumError(w, http.StatusInternalServerError, "WHATSAPP_DISCONNECT_FAILED", "No se pudo limpiar la instancia local")
			return
		}
		_ = s.clearRestaurantUAZAPIIntegration(r.Context(), a.ActiveRestaurantID)
		_ = s.refreshUAZAPIServerUsedCount(r.Context(), rec.ServerID)
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success":   true,
			"message":   "Instancia de WhatsApp eliminada",
			"connected": false,
		})
		return
	}

	if err := s.updateRestaurantUAZAPIInstanceRuntime(r.Context(), a.ActiveRestaurantID, "disconnected", "", "", ""); err != nil {
		writeBOPremiumError(w, http.StatusInternalServerError, "WHATSAPP_DISCONNECT_FAILED", "No se pudo actualizar el estado de la instancia")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":   true,
		"message":   "WhatsApp desconectado",
		"connected": false,
	})
}

// botWebhookCallbackURL returns the public URL UAZAPI instances must call for
// inbound events, derived from BOT_PUBLIC_WEBHOOK_URL config.
func (s *Server) botWebhookCallbackURL() string {
	base := strings.TrimRight(strings.TrimSpace(s.cfg.BotPublicWebhookURL), "/")
	if base == "" {
		return ""
	}
	return base + "/bot/webhook"
}

// ensureUAZAPIInstanceWebhook (re)registers the tenant instance webhook so
// inbound WhatsApp events reach /bot/webhook. Best-effort and idempotent: it is
// safe to call on init, connect and on transition to connected. A failure is
// non-fatal (returned for logging) because a message-less instance can still be
// reconfigured later, but provisioning should not hard-fail on it.
func (s *Server) ensureUAZAPIInstanceWebhook(ctx context.Context, restaurantID int, baseURL string, instanceToken string) error {
	callback := s.botWebhookCallbackURL()
	if callback == "" {
		return errors.New("BOT_PUBLIC_WEBHOOK_URL no configurado")
	}
	err := botUazapiConfigureWebhook(ctx, baseURL, instanceToken, callback, []string{"messages", "connection"})
	s.recordUAZAPIWebhookState(ctx, restaurantID, callback, err == nil)
	return err
}

// recordUAZAPIWebhookState persists the last webhook registration outcome in the
// instance metadata so operators can see whether routing is wired.
func (s *Server) recordUAZAPIWebhookState(ctx context.Context, restaurantID int, callback string, applied bool) {
	meta, _ := json.Marshal(map[string]any{
		"webhook_url":       callback,
		"webhook_applied":   applied,
		"webhook_synced_at": time.Now().UTC().Format(time.RFC3339),
	})
	_, err := s.db.ExecContext(ctx, `
		UPDATE restaurant_uazapi_instances
		SET metadata_json = JSON_MERGE_PATCH(COALESCE(metadata_json, JSON_OBJECT()), CAST(? AS JSON)),
			updated_at = NOW()
		WHERE restaurant_id = ?
	`, string(meta), restaurantID)
	if err != nil && isSQLSchemaError(err) {
		return
	}
}

func (s *Server) provisionAndConnectRestaurantWhatsApp(ctx context.Context, restaurantID int, phone string) (map[string]any, error) {
	rec, err := s.ensureRestaurantUAZAPIInstance(ctx, restaurantID)
	if err != nil {
		return nil, err
	}

	// Defensive: ensure the inbound webhook is registered before pairing so the
	// very first messages after connect are routed to this tenant.
	if whErr := s.ensureUAZAPIInstanceWebhook(ctx, restaurantID, rec.ServerBaseURL, rec.InstanceToken); whErr != nil {
		log.Printf("[uazapi] restaurant=%d webhook register (connect) failed: %v", restaurantID, whErr)
	}

	if isUAZAPIConnected(rec.Status) {
		return s.whatsappConnectionPayload(rec), nil
	}

	reqPayload := map[string]any{}
	normalizedPhone := normalizeWhatsAppNumber(phone)
	if strings.TrimSpace(phone) != "" {
		if normalizedPhone == "" {
			return nil, errors.New("telefono invalido")
		}
		reqPayload["phone"] = normalizedPhone
	}

	respBody, statusCode, rawBody, err := s.uazapiInstanceRequest(ctx, rec.ServerBaseURL, rec.InstanceToken, http.MethodPost, "/instance/connect", reqPayload)
	if err != nil {
		return nil, err
	}
	if statusCode < 200 || statusCode >= 300 {
		return nil, fmt.Errorf("uazapi connect http %d: %s", statusCode, rawBody)
	}

	status := normalizeUAZAPIConnectionStatus(uazapiPickString(respBody, "status", "state", "connection_status", "connectionState"))
	qr := uazapiPickString(respBody, "qrcode", "qr", "qr_code", "qrCode", "base64", "base64_qr")
	pairCode := uazapiPickString(respBody, "pair_code", "pairCode", "paircode", "code")
	connectedPhone := uazapiPickString(respBody, "phone", "number", "connected_phone")
	if connectedPhone == "" {
		connectedPhone = normalizedPhone
	}
	if status == "" {
		if qr != "" || pairCode != "" {
			status = "pending"
		} else {
			status = "connecting"
		}
	}

	if err := s.updateRestaurantUAZAPIInstanceRuntime(ctx, restaurantID, status, connectedPhone, qr, pairCode); err != nil {
		return nil, err
	}

	connection, err := s.refreshRestaurantUAZAPIConnectionStatus(ctx, restaurantID)
	if err == nil {
		return connection, nil
	}

	updatedRec, found, loadErr := s.loadRestaurantUAZAPIInstance(ctx, restaurantID)
	if loadErr != nil {
		return nil, loadErr
	}
	if !found {
		return nil, errors.New("no se pudo cargar instancia provisionada")
	}
	return s.whatsappConnectionPayload(updatedRec), nil
}

func (s *Server) refreshRestaurantUAZAPIConnectionStatus(ctx context.Context, restaurantID int) (map[string]any, error) {
	rec, found, err := s.loadRestaurantUAZAPIInstance(ctx, restaurantID)
	if err != nil {
		return nil, err
	}
	if !found {
		return map[string]any{
			"connected": false,
			"status":    "not_configured",
		}, nil
	}

	respBody, statusCode, rawBody, err := s.uazapiInstanceRequest(ctx, rec.ServerBaseURL, rec.InstanceToken, http.MethodGet, "/instance/status", nil)
	if err != nil {
		return nil, err
	}
	if statusCode < 200 || statusCode >= 300 {
		return nil, fmt.Errorf("uazapi status http %d: %s", statusCode, rawBody)
	}

	status := normalizeUAZAPIConnectionStatus(uazapiPickString(respBody, "status", "state", "connection_status", "connectionState"))
	if status == "" {
		status = normalizeUAZAPIConnectionStatus(rec.Status)
	}
	qr := uazapiPickString(respBody, "qrcode", "qr", "qr_code", "qrCode", "base64", "base64_qr")
	pairCode := uazapiPickString(respBody, "pair_code", "pairCode", "paircode", "code")
	connectedPhone := uazapiPickString(respBody, "phone", "number", "connected_phone")
	if connectedPhone == "" {
		connectedPhone = rec.ConnectedPhone
	}

	if isUAZAPIConnected(status) {
		qr = ""
		pairCode = ""
		_ = s.syncRestaurantUAZAPIIntegration(ctx, restaurantID, rec.ServerBaseURL, rec.InstanceToken)
		if whErr := s.ensureUAZAPIInstanceWebhook(ctx, restaurantID, rec.ServerBaseURL, rec.InstanceToken); whErr != nil {
			log.Printf("[uazapi] restaurant=%d webhook register (connected) failed: %v", restaurantID, whErr)
		}
	}

	if err := s.updateRestaurantUAZAPIInstanceRuntime(ctx, restaurantID, status, connectedPhone, qr, pairCode); err != nil {
		return nil, err
	}

	updatedRec, found, err := s.loadRestaurantUAZAPIInstance(ctx, restaurantID)
	if err != nil {
		return nil, err
	}
	if !found {
		return map[string]any{
			"connected": false,
			"status":    "not_configured",
		}, nil
	}
	return s.whatsappConnectionPayload(updatedRec), nil
}

func (s *Server) ensureRestaurantUAZAPIInstance(ctx context.Context, restaurantID int) (uazapiInstanceRecord, error) {
	// Serialize provisioning so two concurrent requests can neither double-book a
	// server's capacity nor create two provider instances for the same
	// restaurant. ponytail: process mutex — single-instance only; swap for a DB
	// GET_LOCK if this backend ever runs multiple replicas.
	s.provisionMu.Lock()
	defer s.provisionMu.Unlock()

	rec, found, err := s.loadRestaurantUAZAPIInstance(ctx, restaurantID)
	if err != nil {
		return rec, err
	}
	if found && strings.TrimSpace(rec.ServerBaseURL) != "" && strings.TrimSpace(rec.InstanceToken) != "" {
		// Reuse the existing instance; reactivate it if it was suspended after a
		// lapsed subscription so re-subscribing reconnects the same token.
		if _, reErr := s.reactivateRestaurantUAZAPIInstance(ctx, restaurantID); reErr == nil {
			_ = s.refreshUAZAPIServerUsedCount(ctx, rec.ServerID)
			if refreshed, ok, lErr := s.loadRestaurantUAZAPIInstance(ctx, restaurantID); lErr == nil && ok {
				rec = refreshed
			}
		}
		return rec, nil
	}

	server, err := s.pickUAZAPIServer(ctx)
	if err != nil {
		return uazapiInstanceRecord{}, err
	}

	instanceName := fmt.Sprintf("nv-%d-%d", restaurantID, time.Now().UnixNano())
	respBody, statusCode, rawBody, err := s.uazapiAdminRequest(ctx, server.BaseURL, server.AdminToken, http.MethodPost, "/instance/init", map[string]any{
		"name":       instanceName,
		"systemName": "newvillacarmen",
	})
	if err != nil {
		return uazapiInstanceRecord{}, err
	}
	if statusCode < 200 || statusCode >= 300 {
		return uazapiInstanceRecord{}, fmt.Errorf("uazapi init http %d: %s", statusCode, rawBody)
	}

	instanceToken := uazapiPickString(respBody, "token", "instance_token", "instanceToken", "api_token", "apiToken")
	if instanceToken == "" {
		return uazapiInstanceRecord{}, errors.New("uazapi no devolvio token de instancia")
	}
	if providerName := uazapiPickString(respBody, "name", "instance_name", "instanceName"); providerName != "" {
		instanceName = providerName
	}
	providerInstanceID := uazapiPickString(respBody, "instance_id", "instanceId", "id")

	metadataRaw, _ := json.Marshal(map[string]any{
		"provisioned_at": time.Now().UTC().Format(time.RFC3339),
		"source":         "backoffice_premium",
	})

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO restaurant_uazapi_instances
			(restaurant_id, server_id, instance_name, instance_token, provider_instance_id, status, is_active, metadata_json, last_seen_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 'provisioned', 1, ?, NOW(), NOW(), NOW())
		ON DUPLICATE KEY UPDATE
			server_id = VALUES(server_id),
			instance_name = VALUES(instance_name),
			instance_token = VALUES(instance_token),
			provider_instance_id = VALUES(provider_instance_id),
			status = 'provisioned',
			is_active = 1,
			metadata_json = VALUES(metadata_json),
			updated_at = NOW()
	`, restaurantID, server.ID, instanceName, instanceToken, nullableString(providerInstanceID), nullableString(string(metadataRaw)))
	if err != nil {
		if isSQLSchemaError(err) {
			return uazapiInstanceRecord{}, errors.New("tablas de provisionamiento UAZAPI no disponibles")
		}
		return uazapiInstanceRecord{}, err
	}

	_ = s.refreshUAZAPIServerUsedCount(ctx, server.ID)
	_ = s.syncRestaurantUAZAPIIntegration(ctx, restaurantID, server.BaseURL, instanceToken)
	if whErr := s.ensureUAZAPIInstanceWebhook(ctx, restaurantID, server.BaseURL, instanceToken); whErr != nil {
		log.Printf("[uazapi] restaurant=%d webhook register failed: %v", restaurantID, whErr)
	}

	created, found, err := s.loadRestaurantUAZAPIInstance(ctx, restaurantID)
	if err != nil {
		return uazapiInstanceRecord{}, err
	}
	if !found {
		return uazapiInstanceRecord{}, errors.New("no se pudo cargar instancia recien provisionada")
	}
	return created, nil
}

func (s *Server) pickUAZAPIServer(ctx context.Context) (uazapiServerRecord, error) {
	var rec uazapiServerRecord
	err := s.db.QueryRowContext(ctx, `
		SELECT id, name, base_url, admin_token, capacity, used_count
		FROM uazapi_servers
		WHERE is_active = 1
		  AND (capacity <= 0 OR used_count < capacity)
		ORDER BY
			CASE
				WHEN capacity <= 0 THEN 1
				ELSE CAST(used_count AS DECIMAL(18,4)) / CAST(capacity AS DECIMAL(18,4))
			END ASC,
			priority ASC,
			id ASC
		LIMIT 1
	`).Scan(&rec.ID, &rec.Name, &rec.BaseURL, &rec.AdminToken, &rec.Capacity, &rec.UsedCount)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return rec, errUAZAPINoCapacity
		}
		if isSQLSchemaError(err) {
			return rec, errors.New("pool UAZAPI no configurado")
		}
		return rec, err
	}

	rec.BaseURL = strings.TrimRight(strings.TrimSpace(rec.BaseURL), "/")
	rec.AdminToken = strings.TrimSpace(rec.AdminToken)
	if rec.BaseURL == "" || rec.AdminToken == "" {
		return rec, errors.New("servidor UAZAPI invalido")
	}
	return rec, nil
}

func (s *Server) loadRestaurantUAZAPIInstance(ctx context.Context, restaurantID int) (uazapiInstanceRecord, bool, error) {
	var (
		rec                uazapiInstanceRecord
		providerInstanceID sql.NullString
		connectedPhone     sql.NullString
		qrPayload          sql.NullString
		pairCode           sql.NullString
		updatedAt          sql.NullString
	)
	rec.RestaurantID = restaurantID

	err := s.db.QueryRowContext(ctx, `
		SELECT
			i.id,
			i.restaurant_id,
			i.server_id,
			i.instance_name,
			i.instance_token,
			i.provider_instance_id,
			i.connected_phone,
			i.status,
			i.qr_payload,
			i.pair_code,
			DATE_FORMAT(i.updated_at, '%Y-%m-%dT%H:%i:%sZ') AS updated_at,
			s.base_url
		FROM restaurant_uazapi_instances i
		JOIN uazapi_servers s ON s.id = i.server_id
		WHERE i.restaurant_id = ?
		LIMIT 1
	`, restaurantID).Scan(
		&rec.ID,
		&rec.RestaurantID,
		&rec.ServerID,
		&rec.InstanceName,
		&rec.InstanceToken,
		&providerInstanceID,
		&connectedPhone,
		&rec.Status,
		&qrPayload,
		&pairCode,
		&updatedAt,
		&rec.ServerBaseURL,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return rec, false, nil
		}
		if isSQLSchemaError(err) {
			return rec, false, nil
		}
		return rec, false, err
	}

	rec.ProviderInstanceID = strings.TrimSpace(providerInstanceID.String)
	rec.ConnectedPhone = strings.TrimSpace(connectedPhone.String)
	rec.QRPayload = strings.TrimSpace(qrPayload.String)
	rec.PairCode = strings.TrimSpace(pairCode.String)
	rec.UpdatedAt = strings.TrimSpace(updatedAt.String)
	rec.ServerBaseURL = strings.TrimRight(strings.TrimSpace(rec.ServerBaseURL), "/")
	rec.InstanceToken = strings.TrimSpace(rec.InstanceToken)
	rec.Status = normalizeUAZAPIConnectionStatus(rec.Status)
	return rec, true, nil
}

func (s *Server) whatsappConnectionPayload(rec uazapiInstanceRecord) map[string]any {
	status := normalizeUAZAPIConnectionStatus(rec.Status)
	connected := isUAZAPIConnected(status)
	out := map[string]any{
		"status":               status,
		"connected":            connected,
		"instance_name":        rec.InstanceName,
		"provider_instance_id": emptyStringToNil(rec.ProviderInstanceID),
		"updated_at":           emptyStringToNil(rec.UpdatedAt),
		// Spec: these keys are always present (null when empty) so the frontend
		// can rely on the shape.
		"server_base_url": emptyStringToNil(rec.ServerBaseURL),
		"phone":           emptyStringToNil(rec.ConnectedPhone),
		"qr":              emptyStringToNil(rec.QRPayload),
		"pair_code":       emptyStringToNil(rec.PairCode),
	}
	return out
}

func (s *Server) updateRestaurantUAZAPIInstanceRuntime(ctx context.Context, restaurantID int, status string, connectedPhone string, qrPayload string, pairCode string) error {
	status = normalizeUAZAPIConnectionStatus(status)
	connectedPhone = strings.TrimSpace(connectedPhone)
	if connectedPhone != "" {
		connectedPhone = normalizeWhatsAppNumber(connectedPhone)
	}
	qrPayload = strings.TrimSpace(qrPayload)
	pairCode = strings.TrimSpace(pairCode)
	if isUAZAPIConnected(status) {
		qrPayload = ""
		pairCode = ""
	}

	_, err := s.db.ExecContext(ctx, `
		UPDATE restaurant_uazapi_instances
		SET
			status = ?,
			connected_phone = NULLIF(?, ''),
			qr_payload = ?,
			pair_code = ?,
			last_seen_at = NOW(),
			connected_at = CASE WHEN ? = 1 THEN COALESCE(connected_at, NOW()) ELSE connected_at END,
			is_active = 1,
			updated_at = NOW()
		WHERE restaurant_id = ?
	`, status, connectedPhone, nullableString(qrPayload), nullableString(pairCode), boolToTinyInt(isUAZAPIConnected(status)), restaurantID)
	if err != nil && isSQLSchemaError(err) {
		return errors.New("tablas de provisionamiento UAZAPI no disponibles")
	}
	return err
}

func (s *Server) refreshUAZAPIServerUsedCount(ctx context.Context, serverID int64) error {
	if serverID <= 0 {
		return nil
	}
	var used int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM restaurant_uazapi_instances
		WHERE server_id = ? AND is_active = 1
	`, serverID).Scan(&used); err != nil {
		if isSQLSchemaError(err) {
			return nil
		}
		return err
	}
	_, err := s.db.ExecContext(ctx, `UPDATE uazapi_servers SET used_count = ?, updated_at = NOW() WHERE id = ?`, used, serverID)
	if err != nil && isSQLSchemaError(err) {
		return nil
	}
	return err
}

func (s *Server) syncRestaurantUAZAPIIntegration(ctx context.Context, restaurantID int, baseURL string, instanceToken string) error {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	instanceToken = strings.TrimSpace(instanceToken)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO restaurant_integrations (restaurant_id, uazapi_url, uazapi_token)
		VALUES (?, ?, ?)
		ON DUPLICATE KEY UPDATE
			uazapi_url = VALUES(uazapi_url),
			uazapi_token = VALUES(uazapi_token),
			updated_at = NOW()
	`, restaurantID, nullableString(baseURL), nullableString(instanceToken))
	if err != nil && isSQLSchemaError(err) {
		return nil
	}
	return err
}

func (s *Server) clearRestaurantUAZAPIIntegration(ctx context.Context, restaurantID int) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO restaurant_integrations (restaurant_id, uazapi_url, uazapi_token)
		VALUES (?, NULL, NULL)
		ON DUPLICATE KEY UPDATE
			uazapi_url = NULL,
			uazapi_token = NULL,
			updated_at = NOW()
	`, restaurantID)
	if err != nil && isSQLSchemaError(err) {
		return nil
	}
	return err
}

func (s *Server) uazapiAdminRequest(ctx context.Context, baseURL string, adminToken string, method string, path string, payload any) (map[string]any, int, string, error) {
	headers := map[string]string{
		"admintoken": strings.TrimSpace(adminToken),
	}
	return s.uazapiJSONRequest(ctx, strings.TrimRight(strings.TrimSpace(baseURL), "/")+path, method, headers, payload)
}

func (s *Server) uazapiInstanceRequest(ctx context.Context, baseURL string, instanceToken string, method string, path string, payload any) (map[string]any, int, string, error) {
	headers := map[string]string{
		"token": strings.TrimSpace(instanceToken),
	}
	return s.uazapiJSONRequest(ctx, strings.TrimRight(strings.TrimSpace(baseURL), "/")+path, method, headers, payload)
}

func (s *Server) uazapiJSONRequest(ctx context.Context, endpoint string, method string, headers map[string]string, payload any) (map[string]any, int, string, error) {
	var bodyReader io.Reader
	if payload != nil {
		b, _ := json.Marshal(payload)
		bodyReader = bytes.NewReader(b)
	} else if method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch {
		bodyReader = bytes.NewReader([]byte("{}"))
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, bodyReader)
	if err != nil {
		return nil, 0, "", err
	}
	if bodyReader != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		req.Header.Set(k, v)
	}

	resp, err := (&http.Client{Timeout: 35 * time.Second}).Do(req)
	if err != nil {
		return nil, 0, "", err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	rawBody := strings.TrimSpace(string(raw))
	parsed := map[string]any{}
	if rawBody != "" {
		var anyPayload any
		if err := json.Unmarshal(raw, &anyPayload); err == nil {
			switch t := anyPayload.(type) {
			case map[string]any:
				parsed = t
			case []any:
				parsed["items"] = t
			default:
				parsed["value"] = t
			}
		} else {
			parsed["raw"] = rawBody
		}
	}

	return parsed, resp.StatusCode, rawBody, nil
}

func whatsappConnectionMessage(connection map[string]any) string {
	if anyToBool(connection["connected"]) {
		return "WhatsApp conectado y listo para enviar mensajes"
	}
	if firstStringFromMap(connection, "pair_code") != "" {
		return "Conexion iniciada. Usa el codigo de vinculacion en WhatsApp para completar el enlace"
	}
	if firstStringFromMap(connection, "qr") != "" {
		return "Conexion iniciada. Escanea el QR en WhatsApp para completar el enlace"
	}
	return "Conexion iniciada. Esperando vinculacion del dispositivo"
}

func uazapiPickString(node any, keys ...string) string {
	keySet := map[string]struct{}{}
	for _, k := range keys {
		keySet[strings.ToLower(strings.TrimSpace(k))] = struct{}{}
	}

	var walk func(any) string
	walk = func(current any) string {
		switch t := current.(type) {
		case map[string]any:
			for key, value := range t {
				if _, ok := keySet[strings.ToLower(strings.TrimSpace(key))]; ok {
					if raw := uazapiAnyToString(value); raw != "" {
						return raw
					}
				}
			}
			for _, value := range t {
				if raw := walk(value); raw != "" {
					return raw
				}
			}
		case []any:
			for _, value := range t {
				if raw := walk(value); raw != "" {
					return raw
				}
			}
		}
		return ""
	}

	return walk(node)
}

func uazapiAnyToString(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case []byte:
		return strings.TrimSpace(string(t))
	case bool:
		if t {
			return "true"
		}
		return "false"
	case int:
		return fmt.Sprintf("%d", t)
	case int64:
		return fmt.Sprintf("%d", t)
	case float64:
		return strings.TrimSpace(fmt.Sprintf("%.0f", t))
	default:
		return strings.TrimSpace(fmt.Sprint(t))
	}
}

func normalizeUAZAPIConnectionStatus(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	switch {
	case raw == "":
		return "pending"
	case strings.Contains(raw, "connected"), strings.Contains(raw, "online"), raw == "open", strings.Contains(raw, "ready"):
		return "connected"
	case strings.Contains(raw, "disconnect"), strings.Contains(raw, "offline"), strings.Contains(raw, "close"), strings.Contains(raw, "logout"):
		return "disconnected"
	case strings.Contains(raw, "connecting"):
		return "connecting"
	case strings.Contains(raw, "fail"), strings.Contains(raw, "error"):
		return "failed"
	case strings.Contains(raw, "qr"), strings.Contains(raw, "pair"):
		return "pending"
	default:
		return raw
	}
}

func isUAZAPIConnected(status string) bool {
	return normalizeUAZAPIConnectionStatus(status) == "connected"
}

// suspendRestaurantUAZAPIInstance disconnects the tenant instance at the
// provider and marks the local row inactive WITHOUT deleting it, so a later
// re-subscription can reconnect the same instance/token. Called when the
// whatsapp_pack entitlement lapses.
func (s *Server) suspendRestaurantUAZAPIInstance(ctx context.Context, restaurantID int) error {
	rec, found, err := s.loadRestaurantUAZAPIInstance(ctx, restaurantID)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	// Best-effort remote disconnect; ignore provider errors.
	_, _, _, _ = s.uazapiInstanceRequest(ctx, rec.ServerBaseURL, rec.InstanceToken, http.MethodPost, "/instance/disconnect", map[string]any{})

	_, err = s.db.ExecContext(ctx, `
		UPDATE restaurant_uazapi_instances
		SET status = 'suspended',
			is_active = 0,
			qr_payload = NULL,
			pair_code = NULL,
			updated_at = NOW()
		WHERE restaurant_id = ?
	`, restaurantID)
	if err != nil && isSQLSchemaError(err) {
		return nil
	}
	if err == nil {
		_ = s.refreshUAZAPIServerUsedCount(ctx, rec.ServerID)
		_ = s.clearRestaurantUAZAPIIntegration(ctx, restaurantID)
	}
	return err
}

// reactivateRestaurantUAZAPIInstance flips a suspended row back to active so it
// can be reconnected. Returns found=false if there is no row to reactivate.
func (s *Server) reactivateRestaurantUAZAPIInstance(ctx context.Context, restaurantID int) (bool, error) {
	res, err := s.db.ExecContext(ctx, `
		UPDATE restaurant_uazapi_instances
		SET is_active = 1,
			status = CASE WHEN status = 'suspended' THEN 'provisioned' ELSE status END,
			updated_at = NOW()
		WHERE restaurant_id = ?
	`, restaurantID)
	if err != nil {
		if isSQLSchemaError(err) {
			return false, nil
		}
		return false, err
	}
	affected, _ := res.RowsAffected()
	return affected > 0, nil
}
