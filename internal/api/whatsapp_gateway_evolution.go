package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// evolutionGateway implements WhatsAppGateway against a self-hosted Evolution
// API (baileys or Cloud API connector). A single apiKey (the server pool's
// global key, stored in uazapi_servers.admin_token) authenticates every call;
// the instance is addressed by instanceName in the path.
type evolutionGateway struct {
	s            *Server
	baseURL      string
	apiKey       string
	instanceName string
	cloudAPI     bool // instance uses Evolution's WHATSAPP-BUSINESS (Cloud API) connector
}

var _ WhatsAppGateway = (*evolutionGateway)(nil)

func (g *evolutionGateway) request(ctx context.Context, method, path string, payload any) (map[string]any, int, error) {
	baseURL := strings.TrimRight(g.baseURL, "/")
	// Evolution's restricted CORS middleware validates Origin even for
	// server-to-server requests. The staging deployment allows its own API
	// origin (127.0.0.1:8108), so derive the origin from the configured URL
	// instead of relying on the default empty Origin sent by net/http.
	header := map[string]string{"apikey": g.apiKey}
	if parsed, err := url.Parse(baseURL); err == nil && parsed.Scheme != "" && parsed.Host != "" {
		header["Origin"] = parsed.Scheme + "://" + parsed.Host
	}
	resp, code, _, err := g.s.uazapiJSONRequest(ctx, baseURL+path, method, header, payload)
	return resp, code, err
}

// post issues a message send and treats non-2xx as an error.
func (g *evolutionGateway) post(ctx context.Context, path string, payload any) error {
	_, code, err := g.request(ctx, http.MethodPost, path, payload)
	if err != nil {
		return err
	}
	if code < 200 || code >= 300 {
		return fmt.Errorf("evolution http %d for %s", code, path)
	}
	return nil
}

func (g *evolutionGateway) msgPath(op string) string {
	return "/message/" + op + "/" + g.instanceName
}

func (g *evolutionGateway) SendText(ctx context.Context, to, text string) error {
	return g.post(ctx, g.msgPath("sendText"), map[string]any{"number": to, "text": text})
}

// SendMenu renders choices as native buttons. Evolution 2.3.7's Baileys
// sendList route fails at runtime; sendButtons works for both connectors.
//
// Evolution builds the interactive body as "*<title>*\n\n<description>", so
// title and description must not both carry the full text or the recipient
// sees it twice. We split the message at the first line break: the first line
// becomes the bolded header, the rest the body.
//
// Choices follow the UAZAPI convention: a plain string is a reply button, while
// "label|https://..." is a call-to-action URL button. Mapping the latter to a
// reply button would silently drop the link, which is the whole point of the
// booking confirmation buttons.
func (g *evolutionGateway) SendMenu(ctx context.Context, to, text string, choices []string) error {
	buttons := make([]map[string]any, 0, len(choices))
	for i, c := range choices {
		label, target, hasTarget := strings.Cut(c, "|")
		label = strings.TrimSpace(label)
		target = strings.TrimSpace(target)
		if hasTarget && isHTTPURL(target) {
			buttons = append(buttons, map[string]any{"type": "url", "displayText": label, "url": target})
			continue
		}
		if label == "" {
			label = strings.TrimSpace(c)
		}
		buttons = append(buttons, map[string]any{"type": "reply", "displayText": label, "id": fmt.Sprintf("opt_%d", i)})
	}
	title, description := splitMenuTitleAndDescription(text)
	return g.post(ctx, g.msgPath("sendButtons"), map[string]any{
		"number": to, "title": title, "description": description, "buttons": buttons,
	})
}

// splitMenuTitleAndDescription splits a menu message into the short bolded
// header (first line) and the body (the rest). Callers commonly wrap the header
// in *...* markdown, which Evolution would otherwise double-nest, so it is
// stripped here.
func splitMenuTitleAndDescription(text string) (title, description string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", ""
	}
	title = text
	if idx := strings.IndexByte(text, '\n'); idx >= 0 {
		title = strings.TrimSpace(text[:idx])
		description = strings.TrimSpace(text[idx+1:])
	}
	return strings.Trim(title, "*"), description
}

func isHTTPURL(v string) bool {
	parsed, err := url.Parse(v)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != ""
}

func (g *evolutionGateway) SendMedia(ctx context.Context, to string, m waMedia) error {
	payload := map[string]any{"number": to, "mediatype": m.Kind, "media": m.URL}
	if m.Caption != "" {
		payload["caption"] = m.Caption
	}
	if m.Filename != "" {
		payload["fileName"] = m.Filename
	}
	return g.post(ctx, g.msgPath("sendMedia"), payload)
}

func (g *evolutionGateway) SendLocation(ctx context.Context, to string, loc waLocation) error {
	// Evolution requires coordinates; fall back to a text line when we only have
	// an address (restaurants often store address, not lat/lng).
	if loc.Lat == 0 && loc.Lng == 0 {
		return g.SendText(ctx, to, "📍 "+loc.Address)
	}
	return g.post(ctx, g.msgPath("sendLocation"), map[string]any{
		"number": to, "latitude": loc.Lat, "longitude": loc.Lng, "name": loc.Name, "address": loc.Address,
	})
}

func (g *evolutionGateway) SendContact(ctx context.Context, to string, c waContact) error {
	return g.post(ctx, g.msgPath("sendContact"), map[string]any{
		"number": to,
		"contact": []map[string]any{{
			"fullName": c.FullName, "wuid": c.Phone, "phoneNumber": c.Phone, "organization": c.Organization,
		}},
	})
}

func (g *evolutionGateway) Provision(ctx context.Context, instanceName string) (waProvision, error) {
	integration := "WHATSAPP-BAILEYS"
	if g.cloudAPI {
		integration = "WHATSAPP-BUSINESS"
	}
	resp, code, err := g.request(ctx, http.MethodPost, "/instance/create", map[string]any{
		"instanceName": instanceName, "qrcode": false, "integration": integration,
	})
	if err != nil {
		return waProvision{}, err
	}
	if code < 200 || code >= 300 {
		return waProvision{}, fmt.Errorf("evolution create http %d", code)
	}
	hash := uazapiPickString(resp, "hash", "apikey", "token")
	if hash == "" {
		// Some versions nest the token; instanceName still routes, so tolerate it.
		hash = instanceName
	}
	return waProvision{SessionRef: hash, ProviderInstanceID: instanceName, InstanceName: instanceName}, nil
}

func (g *evolutionGateway) evoConnState(resp map[string]any) waConnState {
	pick := func(m map[string]any, keys ...string) string {
		for _, key := range keys {
			if value, ok := m[key].(string); ok && strings.TrimSpace(value) != "" {
				return strings.TrimSpace(value)
			}
		}
		return ""
	}
	instance, _ := resp["instance"].(map[string]any)
	state := pick(resp, "state", "status", "connectionStatus")
	if state == "" {
		state = pick(instance, "state", "status", "connectionStatus")
	}
	qrcode, _ := resp["qrcode"].(map[string]any)
	qr := pick(resp, "base64")
	if qr == "" {
		qr = pick(qrcode, "base64")
	}
	// `code` is sometimes Evolution's raw QR protocol payload, whereas
	// pairingCode/pairCode are user-facing numeric linking codes. Preserve a
	// user-facing code even when a QR is also returned, but never expose raw
	// `code` alongside a renderable QR.
	pair := pick(resp, "pairingCode", "pairCode", "pair_code")
	if pair == "" {
		pair = pick(qrcode, "pairingCode", "pairCode", "pair_code")
	}
	// A nested qrcode.code is a provider pairing code. A top-level code is
	// also accepted unless it is Evolution's raw `2@...` QR protocol payload.
	if pair == "" {
		pair = pick(qrcode, "code")
	}
	if pair == "" {
		topCode := pick(resp, "code")
		if !strings.HasPrefix(topCode, "2@") {
			pair = topCode
		}
	}
	phone := pick(resp, "number", "phone", "owner", "wuid")
	if phone == "" {
		phone = pick(instance, "number", "phone", "owner", "wuid")
	}
	return waConnState{Status: normalizeUAZAPIConnectionStatus(state), ConnectedPhone: phone, QR: qr, PairCode: formatEvolutionPairingCode(pair)}
}

// Evolution returns an eight-character code without punctuation, while its
// own UI (and WhatsApp's linking screen) presents it as XXXX-XXXX.
func formatEvolutionPairingCode(code string) string {
	clean := strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(code), "-", ""), " ", "")
	if len(clean) == 8 && !strings.Contains(clean, "@") {
		return clean[:4] + "-" + clean[4:]
	}
	return strings.TrimSpace(code)
}

func (g *evolutionGateway) Connect(ctx context.Context, phone string) (waConnState, error) {
	path := "/instance/connect/" + url.PathEscape(g.instanceName)
	if phone != "" {
		q := url.Values{}
		q.Set("number", phone)
		path += "?" + q.Encode()
	}
	resp, code, err := g.request(ctx, http.MethodGet, path, nil)
	if err != nil {
		return waConnState{}, err
	}
	if code < 200 || code >= 300 {
		return waConnState{}, fmt.Errorf("evolution connect http %d", code)
	}
	return g.evoConnState(resp), nil
}

func (g *evolutionGateway) Status(ctx context.Context) (waConnState, error) {
	resp, _, err := g.request(ctx, http.MethodGet, "/instance/connectionState/"+g.instanceName, nil)
	if err != nil {
		return waConnState{}, err
	}
	return g.evoConnState(resp), nil
}

func (g *evolutionGateway) Disconnect(ctx context.Context) error {
	_, _, err := g.request(ctx, http.MethodDelete, "/instance/logout/"+g.instanceName, nil)
	return err
}

func (g *evolutionGateway) Delete(ctx context.Context) error {
	_, _, err := g.request(ctx, http.MethodDelete, "/instance/delete/"+g.instanceName, nil)
	return err
}

func (g *evolutionGateway) RegisterWebhook(ctx context.Context, callbackURL string, events []string) error {
	evoEvents := []string{"MESSAGES_UPSERT", "CONNECTION_UPDATE", "QRCODE_UPDATED"}
	return g.post(ctx, "/webhook/set/"+g.instanceName, map[string]any{
		"webhook": map[string]any{
			"enabled": true, "url": callbackURL, "events": evoEvents,
			"webhookByEvents": false, "webhookBase64": true,
		},
	})
}

// --- inbound parsing -------------------------------------------------------

type evoEnvelope struct {
	Event    string          `json:"event"`
	Instance string          `json:"instance"`
	Data     json.RawMessage `json:"data"`
}

func (g *evolutionGateway) ParseInboundMessage(body []byte) (waInbound, bool) {
	var env evoEnvelope
	if err := json.Unmarshal(body, &env); err != nil || !strings.EqualFold(env.Event, "messages.upsert") {
		return waInbound{}, false
	}
	var d struct {
		Key struct {
			RemoteJid string `json:"remoteJid"`
			FromMe    bool   `json:"fromMe"`
			ID        string `json:"id"`
		} `json:"key"`
		PushName string `json:"pushName"`
		Message  struct {
			Conversation    string `json:"conversation"`
			ExtendedTextMsg struct {
				Text string `json:"text"`
			} `json:"extendedTextMessage"`
			ListResponseMsg struct {
				Title             string `json:"title"`
				SingleSelectReply struct {
					SelectedRowID string `json:"selectedRowId"`
				} `json:"singleSelectReply"`
			} `json:"listResponseMessage"`
			AudioMessage json.RawMessage `json:"audioMessage"`
			PtvMessage   json.RawMessage `json:"ptvMessage"`
		} `json:"message"`
	}
	if err := json.Unmarshal(env.Data, &d); err != nil {
		return waInbound{}, false
	}
	jid := strings.TrimSpace(d.Key.RemoteJid)
	if jid == "" || !strings.HasSuffix(jid, "@s.whatsapp.net") { // drop groups (@g.us) / status
		return waInbound{}, false
	}
	text := d.Message.Conversation
	if text == "" {
		text = d.Message.ExtendedTextMsg.Text
	}
	if text == "" && d.Message.ListResponseMsg.SingleSelectReply.SelectedRowID != "" {
		// List reply: prefer the human title, fall back to the row id.
		text = d.Message.ListResponseMsg.Title
		if text == "" {
			text = d.Message.ListResponseMsg.SingleSelectReply.SelectedRowID
		}
	}
	pushName := strings.TrimSpace(d.PushName)
	if pushName == "" {
		pushName = "Cliente"
	}
	return waInbound{
		Sender:     strings.TrimSuffix(jid, "@s.whatsapp.net"),
		Text:       strings.TrimSpace(text),
		PushName:   sanitizeBotPushName(pushName),
		MessageID:  d.Key.ID,
		FromMe:     d.Key.FromMe,
		SessionRef: strings.TrimSpace(env.Instance),
		IsAudio:    d.Message.AudioMessage != nil || d.Message.PtvMessage != nil,
	}, true
}

func (g *evolutionGateway) ParseConnectionEvent(body []byte) (waConnEvent, bool) {
	var env evoEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return waConnEvent{}, false
	}
	ev := strings.ToLower(env.Event)
	if !strings.Contains(ev, "connection") && !strings.Contains(ev, "qrcode") {
		return waConnEvent{}, false
	}
	var d struct {
		State      string `json:"state"`
		Status     string `json:"status"`
		StatusCode int    `json:"statusCode"`
		QRCode     struct {
			Base64 string `json:"base64"`
			Code   string `json:"code"`
		} `json:"qrcode"`
	}
	_ = json.Unmarshal(env.Data, &d)
	var raw map[string]any
	_ = json.Unmarshal(env.Data, &raw)
	status := d.State
	if status == "" {
		status = d.Status
	}
	// Evolution may omit state/status on a failure event and only provide the
	// numeric WhatsApp status code. Preserve the terminal failure so the UI does
	// not remain stuck in "connecting" after a 401 logout.
	if status == "" && d.StatusCode != 0 {
		status = fmt.Sprintf("failed_%d", d.StatusCode)
	}
	qr := d.QRCode.Base64
	out := waConnEvent{
		SessionRef:     strings.TrimSpace(env.Instance),
		Status:         normalizeUAZAPIConnectionStatus(status),
		ConnectedPhone: uazapiPickString(raw, "number", "phone", "owner", "wuid"),
		QR:             qr,
		PairCode:       formatEvolutionPairingCode(uazapiPickString(raw, "pairingCode", "pairCode")),
	}
	if out.Status == "" && qr != "" {
		out.Status = "pending"
	}
	if out.SessionRef == "" {
		return waConnEvent{}, false
	}
	return out, true
}
