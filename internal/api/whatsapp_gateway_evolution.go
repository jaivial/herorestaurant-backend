package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
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
	// ownJIDs are the JIDs/digits that address the bot itself, so a group
	// mention can be recognised. Empty for parsing gateways built without a
	// server (unit tests), where the raw "@<digits>" fallback still applies.
	// Coordination id: wa_bot_group_mention_v1
	ownJIDs []string
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
	resp, code, err := g.request(ctx, http.MethodPost, path, payload)
	if err != nil {
		return err
	}
	if code < 200 || code >= 300 {
		return fmt.Errorf("evolution http %d for %s", code, path)
	}
	// Remember the sent message id so its fromMe webhook echo is not taken for
	// a manual staff message (wa_bot_human_handoff_v1).
	if key, ok := resp["key"].(map[string]any); ok {
		if id, ok := key["id"].(string); ok {
			botRememberOutboundID(id)
		}
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

// SendPoll sends a WhatsApp poll (Evolution /message/sendPoll). Both payload
// shapes Evolution has shipped are included (pollMessage object and flat
// keys), so either server version accepts it. Coordination id: wa_bot_poll_v1
func (g *evolutionGateway) SendPoll(ctx context.Context, to, question string, options []string, maxChoices int) error {
	if maxChoices < 1 {
		maxChoices = 1
	}
	if len(options) > 1 && maxChoices > len(options) {
		maxChoices = len(options)
	}
	return g.post(ctx, g.msgPath("sendPoll"), map[string]any{
		"number":          to,
		"pollMessage":     map[string]any{"name": question, "selectableCount": maxChoices, "values": options},
		"name":            question,
		"selectableCount": maxChoices,
		"values":          options,
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
	// Evolution wraps the title as "*<title>*": strip every bold marker so a
	// title with inner *bold* does not break WhatsApp's formatting.
	return strings.ReplaceAll(title, "*", ""), description
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
	// Evolution builds the vCard as TEL;waid=<wuid>:<phoneNumber>. A leading "+"
	// or spaces/formatting (e.g. waid=+34638857294 observed in prod 2026-09-17
	// for sender 34679042882) produces a broken contact card that the customer
	// cannot tap to chat. Normalize to digits-only (E.164 without "+") so the
	// waid is always callable; the display name/organization still carry the
	// brand. digitsOnly lives in restaurant_profile.go and is shared by every
	// WhatsApp phone comparison.
	normalized := digitsOnly(c.Phone)
	if normalized == "" {
		normalized = c.Phone
	}
	return g.post(ctx, g.msgPath("sendContact"), map[string]any{
		"number": to,
		"contact": []map[string]any{{
			"fullName": c.FullName, "wuid": normalized, "phoneNumber": normalized, "organization": c.Organization,
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
	// also accepted unless it is not a user-facing code at all: Evolution's
	// raw `2@...` QR protocol payload, or a wa.me deep link wrapping it
	// (e.g. "https://wa.me/settings/linked_devices#2@...") — pairing that
	// value overflows pair_code and the whole connect request fails.
	if pair == "" {
		if code := pick(qrcode, "code"); looksLikePairingCode(code) {
			pair = code
		}
	}
	if pair == "" {
		if code := pick(resp, "code"); looksLikePairingCode(code) {
			pair = code
		}
	}
	phone := pick(resp, "number", "phone", "owner", "wuid")
	if phone == "" {
		phone = pick(instance, "number", "phone", "owner", "wuid")
	}
	return waConnState{Status: normalizeUAZAPIConnectionStatus(state), ConnectedPhone: phone, QR: qr, PairCode: formatEvolutionPairingCode(pair)}
}

// normalizeQRImage repairs provider QRs whose dark modules are encoded as
// fully transparent pixels instead of opaque black (observed in the
// baileys-qr-fix fork's canvas output): over the backoffice panel such modules
// take the background color and the code renders invisible and unscannable.
// Fully opaque QRs pass through untouched; transparent ones are re-encoded as
// plain black-on-white using alpha as the module mask. Applied at the instance
// runtime store so gateway, webhook and poller QRs all get repaired.
func normalizeQRImage(qr string) string {
	qr = strings.TrimSpace(qr)
	if qr == "" {
		return ""
	}
	prefix, payload := "", qr
	if strings.HasPrefix(qr, "data:") {
		idx := strings.Index(qr, ",")
		if idx < 0 {
			return qr
		}
		prefix, payload = qr[:idx+1], qr[idx+1:]
	}
	raw, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return qr
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		return qr
	}
	bounds := img.Bounds()
	hasTransparency := false
	for y := bounds.Min.Y; y < bounds.Max.Y && !hasTransparency; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			if _, _, _, a := img.At(x, y).RGBA(); a < 0xFFFF {
				hasTransparency = true
				break
			}
		}
	}
	if !hasTransparency {
		return qr
	}
	out := image.NewRGBA(bounds)
	white := color.RGBA{R: 255, G: 255, B: 255, A: 255}
	black := color.RGBA{A: 255}
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			if _, _, _, a := img.At(x, y).RGBA(); a < 0x8000 {
				out.Set(x, y, black)
			} else {
				out.Set(x, y, white)
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, out); err != nil {
		return qr
	}
	enc := base64.StdEncoding.EncodeToString(buf.Bytes())
	if prefix != "" {
		return prefix + enc
	}
	return enc
}

// looksLikePairingCode accepts only short, opaque, user-facing linking codes.
// Anything URL-shaped ("https://...", "wa.me"), whitespace-bearing, or QR
// protocol payload ("2@...") is not a code a human can type into WhatsApp.
func looksLikePairingCode(code string) bool {
	code = strings.TrimSpace(code)
	if len(code) < 4 || len(code) > 32 {
		return false
	}
	if strings.ContainsAny(code, " \t/#:?&") || strings.Contains(code, "://") || strings.HasPrefix(code, "2@") {
		return false
	}
	return true
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

// errWebhookOwnedElsewhere means the provider instance already delivers to a
// different backend host. Coordination id: wa_webhook_ownership_v1.
var errWebhookOwnedElsewhere = errors.New("evolution webhook pertenece a otro backend; no se sobrescribe")

type webhookClaimKey struct{}

// withWebhookClaim marks an explicit operator connect: only that path may take
// the webhook over from another host; background refreshes never do.
func withWebhookClaim(ctx context.Context) context.Context {
	return context.WithValue(ctx, webhookClaimKey{}, true)
}

func webhookClaimed(ctx context.Context) bool {
	v, _ := ctx.Value(webhookClaimKey{}).(bool)
	return v
}

func webhookHost(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Host)
}

func (g *evolutionGateway) RegisterWebhook(ctx context.Context, callbackURL string, events []string) error {
	// Coordination id: wa_webhook_ownership_v1 - dev and prod share the same
	// Evolution server and, before this guard, both re-registered the webhook
	// on every status refresh: whichever ran last stole the customers'
	// messages (on 2026-09-23 the dev backend, with its own empty DB and
	// transcript, answered a real customer without her booking or the
	// confirmation it had just been sent). Never overwrite another host.
	if resp, code, err := g.request(ctx, http.MethodGet, "/webhook/find/"+g.instanceName, nil); err == nil && code >= 200 && code < 300 {
		current, _ := resp["url"].(string)
		if cur, want := webhookHost(current), webhookHost(callbackURL); cur != "" && want != "" && cur != want && !webhookClaimed(ctx) {
			log.Printf("[whatsapp][obs][CP-WEBHOOK-OWNERSHIP-CONFLICT] instance=%s current_host=%s own_host=%s", g.instanceName, cur, want)
			return errWebhookOwnedElsewhere
		}
	}
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

// evoUpsertData is the messages.upsert payload normalized once for both 1:1
// and group messages. Coordination id: wa_bot_group_mention_v1
type evoUpsertData struct {
	Key struct {
		RemoteJid    string `json:"remoteJid"`
		RemoteJidAlt string `json:"remoteJidAlt"`
		FromMe       bool   `json:"fromMe"`
		ID           string `json:"id"`
		// In a group the sender is the participant, not the chat.
		Participant     string `json:"participant"`
		ParticipantAlt  string `json:"participantAlt"`
		ParticipantJidA string `json:"participantJidAlt"`
	} `json:"key"`
	PushName    string `json:"pushName"`
	MessageType string `json:"messageType"`
	Message     struct {
		Conversation    string `json:"conversation"`
		ExtendedTextMsg struct {
			Text string `json:"text"`
			// The provider nests the context INSIDE the extended text
			// message: who was mentioned and who wrote the quoted
			// message live there.
			ContextInfo evoMessageContext `json:"contextInfo"`
		} `json:"extendedTextMessage"`
		ListResponseMsg struct {
			Title             string `json:"title"`
			SingleSelectReply struct {
				SelectedRowID string `json:"selectedRowId"`
			} `json:"singleSelectReply"`
		} `json:"listResponseMessage"`
		AudioMessage json.RawMessage `json:"audioMessage"`
		// Base64 of the media, present because the webhook is registered
		// with webhookBase64=true (wa_bot_audio_transcription_v1).
		Base64 string `json:"base64"`
		// wa_bot_privileged_group_v1: documents (with inline base64) and the
		// image caption. Only the document fields reach waInbound today.
		DocumentMessage *evoDocumentMessage `json:"documentMessage"`
		ImageMessage    struct {
			Caption  string `json:"caption"`
			Mimetype string `json:"mimetype"`
		} `json:"imageMessage"`
		PtvMessage      json.RawMessage `json:"ptvMessage"`
		ReactionMessage json.RawMessage `json:"reactionMessage"`
		ProtocolMessage json.RawMessage `json:"protocolMessage"`
		// Baileys reports the rendered mentions in mentionText and the JID
		// that was mentioned in contextInfo.mentionedJid.
		MentionText []string `json:"mentionText"`
		// Some provider versions put the context next to the content
		// instead of inside it: both are read.
		ContextInfo evoMessageContext `json:"contextInfo"`
	} `json:"message"`
}

// evoMessageContext is the context of a message: who was mentioned and who
// wrote the message that this one quotes.
type evoMessageContext struct {
	// MentionedJIDs holds the mentioned members, in any of the forms the
	// provider writes them.
	MentionedJIDs evoMentionedJIDs `json:"mentionedJid"`
	// QuotedParticipant is the author of the quoted message: on a reply
	// ("quoting" in the WhatsApp UI) it is who wrote the quoted text, which
	// is how a quote of the bot is recognised.
	QuotedParticipant string `json:"participant"`
}

// merge fills the empty fields of the context with the ones of a fallback
// context, so both places where the provider can put it are read.
func (c evoMessageContext) merge(fallback evoMessageContext) evoMessageContext {
	if len(c.MentionedJIDs) == 0 {
		c.MentionedJIDs = fallback.MentionedJIDs
	}
	if c.QuotedParticipant == "" {
		c.QuotedParticipant = fallback.QuotedParticipant
	}
	return c
}

// evoMentionedJIDs reads the mentioned members of a message. The provider
// writes them as a single value or as a list depending on the version, and a
// field that does not fit the shape MUST NOT break the whole payload: a
// message that fails to parse is a message the bot never sees.
type evoMentionedJIDs []string

func (m *evoMentionedJIDs) UnmarshalJSON(data []byte) error {
	raw := strings.TrimSpace(string(data))
	if raw == "" || raw == "null" {
		*m = nil
		return nil
	}
	if strings.HasPrefix(raw, "[") {
		var list []string
		if err := json.Unmarshal(data, &list); err != nil {
			// An unreadable mention is ignored, not fatal: the message
			// still has to reach the bot.
			*m = nil
			return nil
		}
		*m = list
		return nil
	}
	var one string
	if err := json.Unmarshal(data, &one); err != nil {
		*m = nil
		return nil
	}
	*m = []string{one}
	return nil
}

func (g *evolutionGateway) ParseInboundMessage(body []byte) (waInbound, bool) {
	var env evoEnvelope
	if err := json.Unmarshal(body, &env); err != nil || !strings.EqualFold(env.Event, "messages.upsert") {
		return waInbound{}, false
	}
	var d evoUpsertData
	if err := json.Unmarshal(env.Data, &d); err != nil {
		return waInbound{}, false
	}
	jid := strings.TrimSpace(d.Key.RemoteJid)

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

	isAudio := d.Message.AudioMessage != nil || d.Message.PtvMessage != nil
	ignored := botIsIgnoredMessageType(d.MessageType) || d.Message.ReactionMessage != nil || d.Message.ProtocolMessage != nil

	// Coordination id: wa_bot_privileged_group_v1 - an inline document (the
	// webhook is registered with webhookBase64=true) is kept only when bounded
	// (~11 MB), the same cap as the audio payload.
	documentB64, documentName := botDocumentPayload(d.Message.DocumentMessage, d.Message.Base64)
	// Coordination id: wa_bot_attachment_read_v1 - the inline image payload is
	// kept under the same bounded cap as the other media.
	imageB64, imageMime := botImagePayload(strings.Contains(strings.ToLower(d.MessageType), "image"), d.Message.ImageMessage.Mimetype, d.Message.Base64)

	// Coordination id: wa_bot_group_mention_v1 - a group message is a
	// management turn, not a customer one. It is only served when the bot is
	// explicitly mentioned; replies go to the group JID and the transcript is
	// keyed by the group so members share one thread.
	if botGroupIsGroupJID(jid) {
		// The context is read from both places the provider can put it.
		ctxInfo := d.Message.ExtendedTextMsg.ContextInfo.merge(d.Message.ContextInfo)
		return parseEvolutionGroupMessage(evoGroupInput{
			Instance:  strings.TrimSpace(env.Instance),
			GroupJID:  jid,
			MessageID: d.Key.ID,
			FromMe:    d.Key.FromMe,
			// Keep every participant field: the phone JID may be in any of
			// them when Baileys addresses the member by opaque LID.
			Participants: []string{d.Key.Participant, d.Key.ParticipantJidA, d.Key.ParticipantAlt},
			PushName:     pushName,
			MessageType:  d.MessageType,
			Text:         text,
			MentionText:  d.Message.MentionText,
			MentionedJID: ctxInfo.MentionedJIDs,
			// The author of the quoted message: a reply to the bot is the
			// other way of addressing it when the mention chip is not
			// rendered (wa_bot_group_mention_v1).
			QuotedParticipant: ctxInfo.QuotedParticipant,
			AudioB64:          botAudioBase64(isAudio, d.Message.Base64),
			IsAudio:           isAudio,
			DocumentB64:       documentB64,
			DocumentName:      documentName,
			ImageB64:          imageB64,
			ImageMime:         imageMime,
			Ignored:           ignored,
			OwnJIDs:           g.ownJIDs,
		})
	}

	// The identity of the conversation comes in two forms: WhatsApp's opaque
	// @lid and the phone-number JID. The provider lists a chat by one of them
	// and delivers the message addressed to the other, so BOTH are kept: the
	// phone is what identifies the customer and what the reply goes to, and
	// the other form is what the privileged gate has to match.
	// Coordination id: wa_bot_privileged_contacts_v1
	chatJIDAlt := ""
	if !strings.HasSuffix(jid, "@s.whatsapp.net") {
		// Recent Baileys versions address inbound 1:1 messages by WhatsApp's
		// opaque LID and preserve the phone-number JID in remoteJidAlt. The bot
		// needs the phone number both to identify the customer and to reply.
		if strings.HasSuffix(jid, "@lid") {
			chatJIDAlt = strings.TrimSpace(jid)
			jid = strings.TrimSpace(d.Key.RemoteJidAlt)
		}
	} else if alt := strings.TrimSpace(d.Key.RemoteJidAlt); alt != "" && alt != jid {
		// The other way round: the chat is addressed by the phone number and
		// the opaque LID is the alternative identity.
		chatJIDAlt = alt
	}
	if jid == "" || !strings.HasSuffix(jid, "@s.whatsapp.net") { // drop status / unresolved LIDs
		return waInbound{}, false
	}
	return waInbound{
		Sender:     strings.TrimSuffix(jid, "@s.whatsapp.net"),
		Text:       strings.TrimSpace(text),
		PushName:   sanitizeBotPushName(pushName),
		MessageID:  d.Key.ID,
		FromMe:     d.Key.FromMe,
		SessionRef: strings.TrimSpace(env.Instance),
		IsAudio:    isAudio,
		Ignored:    ignored,
		MediaKind:  botMediaKindLabel(d.MessageType),
		AudioB64:   botAudioBase64(isAudio, d.Message.Base64),
		// wa_bot_privileged_group_v1: populated on the 1:1 path too, but the
		// downstream customer behavior is unchanged (extraction only happens
		// for privileged group turns).
		DocumentB64:  documentB64,
		DocumentName: documentName,
		ImageB64:     imageB64,
		ImageMime:    imageMime,
		ChatJID:      jid,
		ChatJIDAlt:   chatJIDAlt,
	}, true
}

// evoDocumentMessage is the inline documentMessage payload we care about
// (wa_bot_privileged_group_v1): the file name and nothing else.
type evoDocumentMessage struct {
	FileName string `json:"fileName"`
	Caption  string `json:"caption"`
}

// botDocumentPayload keeps the inline document base64 and its file name only
// when a documentMessage carried a bounded (~11 MB) payload.
func botDocumentPayload(doc *evoDocumentMessage, b64 string) (string, string) {
	if doc == nil || b64 == "" || len(b64) > botDocumentMaxBytes {
		return "", ""
	}
	name := strings.TrimSpace(doc.FileName)
	return strings.TrimSpace(b64), name
}

// botAudioBase64 keeps the inline media only for voice notes (bounded ~8 MB).
func botAudioBase64(isAudio bool, b64 string) string {
	if !isAudio || len(b64) > 11<<20 {
		return ""
	}
	return strings.TrimSpace(b64)
}

// botImagePayload keeps the inline media only for images (bounded ~11 MB).
// Coordination id: wa_bot_attachment_read_v1
func botImagePayload(isImage bool, mime, b64 string) (string, string) {
	if !isImage || b64 == "" || len(b64) > 11<<20 {
		return "", ""
	}
	return strings.TrimSpace(b64), strings.TrimSpace(mime)
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
			Base64      string `json:"base64"`
			Code        string `json:"code"`
			PairingCode string `json:"pairingCode"`
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
	// Coordination id: wa_pair_code_fresh_v1 - Evolution's QRCODE_UPDATED
	// nests the phone-linking code under qrcode.pairingCode. Every reconnected
	// socket issues a new one; reading it keeps the panel code valid instead
	// of showing the previous (now rejected) code.
	if out.PairCode == "" && d.QRCode.PairingCode != "" {
		out.PairCode = formatEvolutionPairingCode(d.QRCode.PairingCode)
	}
	if out.Status == "" && qr != "" {
		out.Status = "pending"
	}
	if out.SessionRef == "" {
		return waConnEvent{}, false
	}
	return out, true
}

// SendMenuImage is SendMenu with a header image (Evolution thumbnailUrl), so the
// image, text and buttons arrive as a single message.
func (g *evolutionGateway) SendMenuImage(ctx context.Context, to, text, imageURL string, choices []string) error {
	buttons := make([]map[string]any, 0, len(choices))
	for i, c := range choices {
		label, target, hasTarget := strings.Cut(c, "|")
		label = strings.TrimSpace(label)
		target = strings.TrimSpace(target)
		if hasTarget && isHTTPURL(target) {
			buttons = append(buttons, map[string]any{"type": "url", "displayText": label, "url": target})
			continue
		}
		buttons = append(buttons, map[string]any{"type": "reply", "displayText": firstNonEmpty(label, strings.TrimSpace(c)), "id": fmt.Sprintf("opt_%d", i)})
	}
	title, description := splitMenuTitleAndDescription(text)
	return g.post(ctx, g.msgPath("sendButtons"), map[string]any{
		"number": to, "title": title, "description": description, "buttons": buttons, "thumbnailUrl": imageURL,
	})
}
