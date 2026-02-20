package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"preactvillacarmen/internal/httpx"
)

const boUAZAPIServerMaxCapacity = 10000

const boUAZAPIServerSelectBase = `
SELECT id, name, base_url, admin_token, capacity, used_count, priority, is_active, metadata_json
FROM uazapi_servers`

type boUAZAPIServerView struct {
	ID               int64 `json:"id"`
	Name             string `json:"name"`
	BaseURL          string `json:"baseUrl"`
	AdminTokenMasked string `json:"adminTokenMasked"`
	Capacity         int    `json:"capacity"`
	Used             int    `json:"used"`
	Priority         int    `json:"priority"`
	IsActive         bool   `json:"isActive"`
	Metadata         any    `json:"metadata"`
}

type boUAZAPIServerRecord struct {
	ID         int64
	Name       string
	BaseURL    string
	AdminToken string
	Capacity   int
	UsedCount  int
	Priority   int
	IsActive   bool
	Metadata   any
}

func (s *Server) handleBOUAZAPIServersList(w http.ResponseWriter, r *http.Request) {
	servers, err := s.loadBOUAZAPIServers(r.Context())
	if err != nil {
		if isSQLSchemaError(err) {
			writeBOUAZAPIServerError(w, http.StatusServiceUnavailable, "UAZAPI_POOL_UNAVAILABLE", "Pool UAZAPI no disponible")
			return
		}
		writeBOUAZAPIServerError(w, http.StatusInternalServerError, "UAZAPI_POOL_READ_FAILED", "No se pudo listar servidores UAZAPI")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data": map[string]any{
			"servers": servers,
		},
	})
}

func (s *Server) handleBOUAZAPIServersCreate(w http.ResponseWriter, r *http.Request) {
	body, err := decodeBOUAZAPIServerBody(r)
	if err != nil {
		if errors.Is(err, io.EOF) {
			writeBOUAZAPIServerError(w, http.StatusBadRequest, "BAD_REQUEST", "JSON invalido")
			return
		}
		writeBOUAZAPIServerError(w, http.StatusBadRequest, "BAD_REQUEST", "JSON invalido")
		return
	}

	if unknown := firstUnknownBOUAZAPIServerField(body, map[string]struct{}{
		"name":        {},
		"baseUrl":     {},
		"base_url":    {},
		"adminToken":  {},
		"admin_token": {},
		"capacity":    {},
		"priority":    {},
		"isActive":    {},
		"is_active":   {},
		"metadata":    {},
	}); unknown != "" {
		writeBOUAZAPIServerError(w, http.StatusBadRequest, "BAD_REQUEST", "Campo no soportado: "+unknown)
		return
	}

	nameRaw, ok := firstBOUAZAPIServerField(body, "name")
	if !ok {
		writeBOUAZAPIServerError(w, http.StatusBadRequest, "BAD_REQUEST", "name requerido")
		return
	}
	var name string
	if err := json.Unmarshal(nameRaw, &name); err != nil {
		writeBOUAZAPIServerError(w, http.StatusBadRequest, "BAD_REQUEST", "name invalido")
		return
	}
	name = strings.TrimSpace(name)
	if name == "" {
		writeBOUAZAPIServerError(w, http.StatusBadRequest, "BAD_REQUEST", "name requerido")
		return
	}
	if len(name) > 128 {
		writeBOUAZAPIServerError(w, http.StatusBadRequest, "BAD_REQUEST", "name demasiado largo")
		return
	}

	baseURLRaw, ok := firstBOUAZAPIServerField(body, "baseUrl", "base_url")
	if !ok {
		writeBOUAZAPIServerError(w, http.StatusBadRequest, "BAD_REQUEST", "baseUrl requerido")
		return
	}
	var baseURLInput string
	if err := json.Unmarshal(baseURLRaw, &baseURLInput); err != nil {
		writeBOUAZAPIServerError(w, http.StatusBadRequest, "BAD_REQUEST", "baseUrl invalido")
		return
	}
	baseURL, err := normalizeBOUAZAPIServerBaseURL(baseURLInput)
	if err != nil {
		writeBOUAZAPIServerError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}

	adminTokenRaw, ok := firstBOUAZAPIServerField(body, "adminToken", "admin_token")
	if !ok {
		writeBOUAZAPIServerError(w, http.StatusBadRequest, "BAD_REQUEST", "adminToken requerido")
		return
	}
	var adminToken string
	if err := json.Unmarshal(adminTokenRaw, &adminToken); err != nil {
		writeBOUAZAPIServerError(w, http.StatusBadRequest, "BAD_REQUEST", "adminToken invalido")
		return
	}
	adminToken = strings.TrimSpace(adminToken)
	if adminToken == "" {
		writeBOUAZAPIServerError(w, http.StatusBadRequest, "BAD_REQUEST", "adminToken requerido")
		return
	}
	if len(adminToken) > 255 {
		writeBOUAZAPIServerError(w, http.StatusBadRequest, "BAD_REQUEST", "adminToken demasiado largo")
		return
	}

	capacityRaw, ok := firstBOUAZAPIServerField(body, "capacity")
	if !ok {
		writeBOUAZAPIServerError(w, http.StatusBadRequest, "BAD_REQUEST", "capacity requerido")
		return
	}
	var capacity int
	if err := json.Unmarshal(capacityRaw, &capacity); err != nil {
		writeBOUAZAPIServerError(w, http.StatusBadRequest, "BAD_REQUEST", "capacity invalido")
		return
	}
	if err := validateBOUAZAPIServerCapacity(capacity); err != nil {
		writeBOUAZAPIServerError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}

	priority := 100
	if priorityRaw, ok := firstBOUAZAPIServerField(body, "priority"); ok {
		if err := json.Unmarshal(priorityRaw, &priority); err != nil {
			writeBOUAZAPIServerError(w, http.StatusBadRequest, "BAD_REQUEST", "priority invalido")
			return
		}
	}

	isActive := true
	if isActiveRaw, ok := firstBOUAZAPIServerField(body, "isActive", "is_active"); ok {
		if err := json.Unmarshal(isActiveRaw, &isActive); err != nil {
			writeBOUAZAPIServerError(w, http.StatusBadRequest, "BAD_REQUEST", "isActive invalido")
			return
		}
	}

	var metadataJSON []byte
	if metadataRaw, ok := firstBOUAZAPIServerField(body, "metadata"); ok {
		metadataJSON, err = normalizeBOUAZAPIServerMetadata(metadataRaw)
		if err != nil {
			writeBOUAZAPIServerError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
			return
		}
	}

	var metadataValue any
	if metadataJSON != nil {
		metadataValue = metadataJSON
	}

	result, err := s.db.ExecContext(r.Context(), `
		INSERT INTO uazapi_servers
			(name, base_url, admin_token, capacity, priority, is_active, metadata_json, created_at, updated_at)
		VALUES
			(?, ?, ?, ?, ?, ?, ?, NOW(), NOW())
	`, name, baseURL, adminToken, capacity, priority, boolToTinyInt(isActive), metadataValue)
	if err != nil {
		if isSQLDuplicateError(err) {
			writeBOUAZAPIServerError(w, http.StatusConflict, "DUPLICATE_BASE_URL", "baseUrl ya existe")
			return
		}
		if isSQLSchemaError(err) {
			writeBOUAZAPIServerError(w, http.StatusServiceUnavailable, "UAZAPI_POOL_UNAVAILABLE", "Pool UAZAPI no disponible")
			return
		}
		writeBOUAZAPIServerError(w, http.StatusInternalServerError, "UAZAPI_SERVER_CREATE_FAILED", "No se pudo crear servidor UAZAPI")
		return
	}

	serverID, err := result.LastInsertId()
	if err != nil {
		writeBOUAZAPIServerError(w, http.StatusInternalServerError, "UAZAPI_SERVER_CREATE_FAILED", "No se pudo crear servidor UAZAPI")
		return
	}

	server, found, err := s.loadBOUAZAPIServerByID(r.Context(), serverID)
	if err != nil {
		if isSQLSchemaError(err) {
			writeBOUAZAPIServerError(w, http.StatusServiceUnavailable, "UAZAPI_POOL_UNAVAILABLE", "Pool UAZAPI no disponible")
			return
		}
		writeBOUAZAPIServerError(w, http.StatusInternalServerError, "UAZAPI_SERVER_READ_FAILED", "No se pudo cargar servidor UAZAPI")
		return
	}
	if !found {
		writeBOUAZAPIServerError(w, http.StatusInternalServerError, "UAZAPI_SERVER_READ_FAILED", "No se pudo cargar servidor UAZAPI")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": "Servidor UAZAPI creado",
		"data": map[string]any{
			"server": server,
		},
	})
}

func (s *Server) handleBOUAZAPIServersPatch(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(strings.TrimSpace(chi.URLParam(r, "id")), 10, 64)
	if err != nil || id <= 0 {
		writeBOUAZAPIServerError(w, http.StatusBadRequest, "BAD_REQUEST", "id invalido")
		return
	}

	body, err := decodeBOUAZAPIServerBody(r)
	if err != nil {
		if errors.Is(err, io.EOF) {
			writeBOUAZAPIServerError(w, http.StatusBadRequest, "BAD_REQUEST", "JSON invalido")
			return
		}
		writeBOUAZAPIServerError(w, http.StatusBadRequest, "BAD_REQUEST", "JSON invalido")
		return
	}
	if len(body) == 0 {
		writeBOUAZAPIServerError(w, http.StatusBadRequest, "BAD_REQUEST", "No hay campos para actualizar")
		return
	}

	if unknown := firstUnknownBOUAZAPIServerField(body, map[string]struct{}{
		"name":        {},
		"baseUrl":     {},
		"base_url":    {},
		"adminToken":  {},
		"admin_token": {},
		"capacity":    {},
		"priority":    {},
		"isActive":    {},
		"is_active":   {},
		"metadata":    {},
	}); unknown != "" {
		writeBOUAZAPIServerError(w, http.StatusBadRequest, "BAD_REQUEST", "Campo no soportado: "+unknown)
		return
	}

	_, found, err := s.loadBOUAZAPIServerByID(r.Context(), id)
	if err != nil {
		if isSQLSchemaError(err) {
			writeBOUAZAPIServerError(w, http.StatusServiceUnavailable, "UAZAPI_POOL_UNAVAILABLE", "Pool UAZAPI no disponible")
			return
		}
		writeBOUAZAPIServerError(w, http.StatusInternalServerError, "UAZAPI_SERVER_READ_FAILED", "No se pudo cargar servidor UAZAPI")
		return
	}
	if !found {
		writeBOUAZAPIServerError(w, http.StatusNotFound, "NOT_FOUND", "Servidor UAZAPI no encontrado")
		return
	}

	setParts := make([]string, 0, len(body)+1)
	args := make([]any, 0, len(body)+1)

	if nameRaw, ok := firstBOUAZAPIServerField(body, "name"); ok {
		var name string
		if err := json.Unmarshal(nameRaw, &name); err != nil {
			writeBOUAZAPIServerError(w, http.StatusBadRequest, "BAD_REQUEST", "name invalido")
			return
		}
		name = strings.TrimSpace(name)
		if name == "" {
			writeBOUAZAPIServerError(w, http.StatusBadRequest, "BAD_REQUEST", "name requerido")
			return
		}
		if len(name) > 128 {
			writeBOUAZAPIServerError(w, http.StatusBadRequest, "BAD_REQUEST", "name demasiado largo")
			return
		}
		setParts = append(setParts, "name = ?")
		args = append(args, name)
	}

	if baseURLRaw, ok := firstBOUAZAPIServerField(body, "baseUrl", "base_url"); ok {
		var baseURLInput string
		if err := json.Unmarshal(baseURLRaw, &baseURLInput); err != nil {
			writeBOUAZAPIServerError(w, http.StatusBadRequest, "BAD_REQUEST", "baseUrl invalido")
			return
		}
		baseURL, err := normalizeBOUAZAPIServerBaseURL(baseURLInput)
		if err != nil {
			writeBOUAZAPIServerError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
			return
		}
		setParts = append(setParts, "base_url = ?")
		args = append(args, baseURL)
	}

	if adminTokenRaw, ok := firstBOUAZAPIServerField(body, "adminToken", "admin_token"); ok {
		var adminToken string
		if err := json.Unmarshal(adminTokenRaw, &adminToken); err != nil {
			writeBOUAZAPIServerError(w, http.StatusBadRequest, "BAD_REQUEST", "adminToken invalido")
			return
		}
		adminToken = strings.TrimSpace(adminToken)
		if adminToken == "" {
			writeBOUAZAPIServerError(w, http.StatusBadRequest, "BAD_REQUEST", "adminToken requerido")
			return
		}
		if len(adminToken) > 255 {
			writeBOUAZAPIServerError(w, http.StatusBadRequest, "BAD_REQUEST", "adminToken demasiado largo")
			return
		}
		setParts = append(setParts, "admin_token = ?")
		args = append(args, adminToken)
	}

	if capacityRaw, ok := firstBOUAZAPIServerField(body, "capacity"); ok {
		var capacity int
		if err := json.Unmarshal(capacityRaw, &capacity); err != nil {
			writeBOUAZAPIServerError(w, http.StatusBadRequest, "BAD_REQUEST", "capacity invalido")
			return
		}
		if err := validateBOUAZAPIServerCapacity(capacity); err != nil {
			writeBOUAZAPIServerError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
			return
		}
		setParts = append(setParts, "capacity = ?")
		args = append(args, capacity)
	}

	if priorityRaw, ok := firstBOUAZAPIServerField(body, "priority"); ok {
		var priority int
		if err := json.Unmarshal(priorityRaw, &priority); err != nil {
			writeBOUAZAPIServerError(w, http.StatusBadRequest, "BAD_REQUEST", "priority invalido")
			return
		}
		setParts = append(setParts, "priority = ?")
		args = append(args, priority)
	}

	if isActiveRaw, ok := firstBOUAZAPIServerField(body, "isActive", "is_active"); ok {
		var isActive bool
		if err := json.Unmarshal(isActiveRaw, &isActive); err != nil {
			writeBOUAZAPIServerError(w, http.StatusBadRequest, "BAD_REQUEST", "isActive invalido")
			return
		}
		setParts = append(setParts, "is_active = ?")
		args = append(args, boolToTinyInt(isActive))
	}

	if metadataRaw, ok := firstBOUAZAPIServerField(body, "metadata"); ok {
		metadataJSON, err := normalizeBOUAZAPIServerMetadata(metadataRaw)
		if err != nil {
			writeBOUAZAPIServerError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
			return
		}
		if metadataJSON == nil {
			setParts = append(setParts, "metadata_json = NULL")
		} else {
			setParts = append(setParts, "metadata_json = ?")
			args = append(args, metadataJSON)
		}
	}

	if len(setParts) == 0 {
		writeBOUAZAPIServerError(w, http.StatusBadRequest, "BAD_REQUEST", "No hay campos para actualizar")
		return
	}

	query := "UPDATE uazapi_servers SET " + strings.Join(setParts, ", ") + ", updated_at = NOW() WHERE id = ?"
	args = append(args, id)

	if _, err := s.db.ExecContext(r.Context(), query, args...); err != nil {
		if isSQLDuplicateError(err) {
			writeBOUAZAPIServerError(w, http.StatusConflict, "DUPLICATE_BASE_URL", "baseUrl ya existe")
			return
		}
		if isSQLSchemaError(err) {
			writeBOUAZAPIServerError(w, http.StatusServiceUnavailable, "UAZAPI_POOL_UNAVAILABLE", "Pool UAZAPI no disponible")
			return
		}
		writeBOUAZAPIServerError(w, http.StatusInternalServerError, "UAZAPI_SERVER_UPDATE_FAILED", "No se pudo actualizar servidor UAZAPI")
		return
	}

	server, found, err := s.loadBOUAZAPIServerByID(r.Context(), id)
	if err != nil {
		if isSQLSchemaError(err) {
			writeBOUAZAPIServerError(w, http.StatusServiceUnavailable, "UAZAPI_POOL_UNAVAILABLE", "Pool UAZAPI no disponible")
			return
		}
		writeBOUAZAPIServerError(w, http.StatusInternalServerError, "UAZAPI_SERVER_READ_FAILED", "No se pudo cargar servidor UAZAPI")
		return
	}
	if !found {
		writeBOUAZAPIServerError(w, http.StatusNotFound, "NOT_FOUND", "Servidor UAZAPI no encontrado")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": "Servidor UAZAPI actualizado",
		"data": map[string]any{
			"server": server,
		},
	})
}

func (s *Server) loadBOUAZAPIServers(ctx context.Context) ([]boUAZAPIServerView, error) {
	rows, err := s.db.QueryContext(ctx, boUAZAPIServerSelectBase+` ORDER BY is_active DESC, priority ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]boUAZAPIServerView, 0)
	for rows.Next() {
		rec, err := scanBOUAZAPIServerRecord(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, mapBOUAZAPIServerRecordToView(rec))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Server) loadBOUAZAPIServerByID(ctx context.Context, id int64) (boUAZAPIServerView, bool, error) {
	row := s.db.QueryRowContext(ctx, boUAZAPIServerSelectBase+` WHERE id = ? LIMIT 1`, id)
	rec, err := scanBOUAZAPIServerRecord(row.Scan)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return boUAZAPIServerView{}, false, nil
		}
		return boUAZAPIServerView{}, false, err
	}
	return mapBOUAZAPIServerRecordToView(rec), true, nil
}

func scanBOUAZAPIServerRecord(scanFn func(dest ...any) error) (boUAZAPIServerRecord, error) {
	var rec boUAZAPIServerRecord
	var isActiveInt int
	var metadataRaw sql.NullString
	if err := scanFn(
		&rec.ID,
		&rec.Name,
		&rec.BaseURL,
		&rec.AdminToken,
		&rec.Capacity,
		&rec.UsedCount,
		&rec.Priority,
		&isActiveInt,
		&metadataRaw,
	); err != nil {
		return boUAZAPIServerRecord{}, err
	}

	rec.IsActive = isActiveInt != 0
	if metadataRaw.Valid {
		raw := strings.TrimSpace(metadataRaw.String)
		if raw != "" && raw != "null" {
			var parsed any
			if err := json.Unmarshal([]byte(raw), &parsed); err == nil {
				rec.Metadata = parsed
			}
		}
	}

	return rec, nil
}

func mapBOUAZAPIServerRecordToView(rec boUAZAPIServerRecord) boUAZAPIServerView {
	return boUAZAPIServerView{
		ID:               rec.ID,
		Name:             rec.Name,
		BaseURL:          rec.BaseURL,
		AdminTokenMasked: maskBOUAZAPIServerToken(rec.AdminToken),
		Capacity:         rec.Capacity,
		Used:             rec.UsedCount,
		Priority:         rec.Priority,
		IsActive:         rec.IsActive,
		Metadata:         rec.Metadata,
	}
}

func maskBOUAZAPIServerToken(raw string) string {
	token := strings.TrimSpace(raw)
	if token == "" {
		return ""
	}

	runes := []rune(token)
	switch {
	case len(runes) <= 2:
		return strings.Repeat("*", len(runes))
	case len(runes) <= 8:
		return string(runes[:1]) + strings.Repeat("*", len(runes)-2) + string(runes[len(runes)-1:])
	default:
		return string(runes[:3]) + strings.Repeat("*", len(runes)-6) + string(runes[len(runes)-3:])
	}
}

func normalizeBOUAZAPIServerBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("baseUrl requerido")
	}

	raw = strings.TrimRight(raw, "/")
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", errors.New("baseUrl invalido")
	}

	scheme := strings.ToLower(strings.TrimSpace(parsed.Scheme))
	if scheme != "http" && scheme != "https" {
		return "", errors.New("baseUrl debe usar http/https")
	}
	if strings.TrimSpace(parsed.Host) == "" {
		return "", errors.New("baseUrl invalido")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("baseUrl no puede incluir query/fragment")
	}

	parsed.Scheme = scheme
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return strings.TrimRight(parsed.String(), "/"), nil
}

func validateBOUAZAPIServerCapacity(capacity int) error {
	if capacity <= 0 {
		return errors.New("capacity debe ser mayor que 0")
	}
	if capacity > boUAZAPIServerMaxCapacity {
		return errors.New("capacity supera el maximo permitido (10000)")
	}
	return nil
}

func normalizeBOUAZAPIServerMetadata(raw json.RawMessage) ([]byte, error) {
	value := strings.TrimSpace(string(raw))
	if value == "" || value == "null" {
		return nil, nil
	}

	var metadata map[string]any
	if err := json.Unmarshal(raw, &metadata); err != nil {
		return nil, errors.New("metadata invalida")
	}
	if metadata == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return nil, errors.New("metadata invalida")
	}
	return encoded, nil
}

func decodeBOUAZAPIServerBody(r *http.Request) (map[string]json.RawMessage, error) {
	dec := json.NewDecoder(r.Body)
	var body map[string]json.RawMessage
	if err := dec.Decode(&body); err != nil {
		return nil, err
	}
	if body == nil {
		body = map[string]json.RawMessage{}
	}

	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, errors.New("json invalido")
		}
		return nil, err
	}
	return body, nil
}

func firstUnknownBOUAZAPIServerField(body map[string]json.RawMessage, allowed map[string]struct{}) string {
	for key := range body {
		if _, ok := allowed[key]; !ok {
			return key
		}
	}
	return ""
}

func firstBOUAZAPIServerField(body map[string]json.RawMessage, names ...string) (json.RawMessage, bool) {
	for _, name := range names {
		if raw, ok := body[name]; ok {
			return raw, true
		}
	}
	return nil, false
}

func writeBOUAZAPIServerError(w http.ResponseWriter, status int, code string, message string) {
	httpx.WriteJSON(w, status, map[string]any{
		"success": false,
		"code":    code,
		"message": message,
	})
}
