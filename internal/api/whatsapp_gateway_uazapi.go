package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

// uazapiGateway implements WhatsAppGateway by delegating to the existing UAZAPI
// code paths. It introduces NO wire-level change: sends still go through
// botUazapiSend (?token= query) and lifecycle through the header-auth helpers.
type uazapiGateway struct {
	s             *Server
	baseURL       string
	adminToken    string // server admin token (only used by Provision)
	instanceToken string // per-instance token (sends, connect, status, ...)
}

var _ WhatsAppGateway = (*uazapiGateway)(nil)

func (g *uazapiGateway) send(ctx context.Context, kind string, payload map[string]any) error {
	return botUazapiSend(ctx, g.baseURL, g.instanceToken, kind, payload)
}

func (g *uazapiGateway) SendText(ctx context.Context, to, text string) error {
	return g.send(ctx, "text", map[string]any{"number": to, "text": text})
}

func (g *uazapiGateway) SendMenu(ctx context.Context, to, text string, choices []string) error {
	return g.send(ctx, "menu", map[string]any{
		"number": to, "type": "button", "text": text, "choices": choices,
	})
}

func (g *uazapiGateway) SendMedia(ctx context.Context, to string, m waMedia) error {
	payload := map[string]any{"number": to, "type": m.Kind, "file": m.URL}
	if m.Caption != "" {
		payload["text"] = m.Caption
	}
	if m.Filename != "" {
		payload["docName"] = m.Filename
	}
	return g.send(ctx, "media", payload)
}

func (g *uazapiGateway) SendLocation(ctx context.Context, to string, loc waLocation) error {
	return g.send(ctx, "location", map[string]any{
		"number": to, "address": loc.Address, "name": loc.Name,
	})
}

func (g *uazapiGateway) SendContact(ctx context.Context, to string, c waContact) error {
	return g.send(ctx, "contact", map[string]any{
		"number": to, "fullName": c.FullName, "phoneNumber": c.Phone, "organization": c.Organization,
	})
}

func (g *uazapiGateway) Provision(ctx context.Context, instanceName string) (waProvision, error) {
	resp, code, raw, err := g.s.uazapiAdminRequest(ctx, g.baseURL, g.adminToken, http.MethodPost, "/instance/init", map[string]any{
		"name": instanceName, "systemName": "newvillacarmen",
	})
	if err != nil {
		return waProvision{}, err
	}
	if code < 200 || code >= 300 {
		return waProvision{}, errors.New("uazapi init http error: " + truncate(raw, 200))
	}
	token := uazapiPickString(resp, "token", "instance_token", "instanceToken", "api_token", "apiToken")
	if token == "" {
		return waProvision{}, errors.New("uazapi no devolvio token de instancia")
	}
	name := uazapiPickString(resp, "name", "instance_name", "instanceName")
	if name == "" {
		name = instanceName
	}
	return waProvision{
		SessionRef:         token,
		ProviderInstanceID: uazapiPickString(resp, "instance_id", "instanceId", "id"),
		InstanceName:       name,
	}, nil
}

func (g *uazapiGateway) connState(resp map[string]any) waConnState {
	instance, _ := resp["instance"].(map[string]any)
	pick := func(m map[string]any, keys ...string) string {
		for _, key := range keys {
			if value, ok := m[key]; ok {
				if raw, ok := value.(string); ok {
					return strings.TrimSpace(raw)
				}
			}
		}
		return ""
	}

	status := pick(instance, "status", "state", "connection_status", "connectionState")
	if status == "" {
		status = pick(resp, "status", "state", "connection_status", "connectionState")
	}
	phone := pick(instance, "phone", "number", "connected_phone")
	if phone == "" {
		phone = pick(resp, "phone", "number", "connected_phone")
	}
	if phone == "" {
		owner := pick(instance, "owner")
		if owner == "" {
			owner = pick(resp, "owner")
		}
		phone = strings.Split(strings.Split(owner, "@")[0], ":")[0]
	}
	qr := pick(instance, "qrcode", "qr", "qr_code", "qrCode", "base64", "base64_qr")
	if qr == "" {
		qr = pick(resp, "qrcode", "qr", "qr_code", "qrCode", "base64", "base64_qr")
	}
	pairCode := pick(instance, "pair_code", "pairCode", "paircode", "code")
	if pairCode == "" {
		pairCode = pick(resp, "pair_code", "pairCode", "paircode", "code")
	}
	return waConnState{
		Status:         normalizeUAZAPIConnectionStatus(status),
		ConnectedPhone: phone,
		QR:             qr,
		PairCode:       pairCode,
	}
}

func (g *uazapiGateway) Connect(ctx context.Context, phone string) (waConnState, error) {
	payload := map[string]any{}
	if phone != "" {
		payload["phone"] = phone
	}
	resp, code, raw, err := g.s.uazapiInstanceRequest(ctx, g.baseURL, g.instanceToken, http.MethodPost, "/instance/connect", payload)
	if err != nil {
		return waConnState{}, err
	}
	if code < 200 || code >= 300 {
		return waConnState{}, errors.New("uazapi connect http error: " + truncate(raw, 200))
	}
	return g.connState(resp), nil
}

func (g *uazapiGateway) Status(ctx context.Context) (waConnState, error) {
	resp, code, raw, err := g.s.uazapiInstanceRequest(ctx, g.baseURL, g.instanceToken, http.MethodGet, "/instance/status", nil)
	if err != nil {
		return waConnState{}, err
	}
	if code < 200 || code >= 300 {
		return waConnState{}, errors.New("uazapi status http error: " + truncate(raw, 200))
	}
	return g.connState(resp), nil
}

func (g *uazapiGateway) Disconnect(ctx context.Context) error {
	_, _, _, err := g.s.uazapiInstanceRequest(ctx, g.baseURL, g.instanceToken, http.MethodPost, "/instance/disconnect", map[string]any{})
	return err
}

func (g *uazapiGateway) Delete(ctx context.Context) error {
	_, _, _, err := g.s.uazapiInstanceRequest(ctx, g.baseURL, g.instanceToken, http.MethodDelete, "/instance", nil)
	return err
}

func (g *uazapiGateway) RegisterWebhook(ctx context.Context, callbackURL string, events []string) error {
	return botUazapiConfigureWebhook(ctx, g.baseURL, g.instanceToken, callbackURL, events)
}

func (g *uazapiGateway) ParseInboundMessage(body []byte) (waInbound, bool) {
	m, ok := parseBotWebhookMessage(body)
	if !ok {
		return waInbound{}, false
	}
	return waInbound{
		Sender: m.Sender, Text: m.Text, PushName: m.PushName,
		MessageID: m.MessageID, FromMe: m.FromMe, SessionRef: m.InstanceToken,
	}, true
}

func (g *uazapiGateway) ParseConnectionEvent(body []byte) (waConnEvent, bool) {
	e, ok := parseBotConnectionEvent(body)
	if !ok {
		return waConnEvent{}, false
	}
	return waConnEvent{
		SessionRef: e.InstanceToken, Owner: e.Owner, Status: e.Status,
		ConnectedPhone: e.ConnectedPhone, QR: e.QR, PairCode: e.PairCode,
	}, true
}
