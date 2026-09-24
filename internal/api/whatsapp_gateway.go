package api

import (
	"context"
	"strings"
)

// WhatsAppGateway isolates the provider-varying operations behind the
// multi-tenant WhatsApp bot so UAZAPI can be swapped for another gateway
// (Evolution API) without touching callers. `to` is always plain E.164 digits;
// each implementation formats the provider-specific chat id / jid.
//
// Inbound parsing methods are stateless (ignore credentials) so they can be
// called on a provider-only gateway obtained via gatewayForProvider.
type WhatsAppGateway interface {
	// Outbound sends.
	SendText(ctx context.Context, to, text string) error
	SendMenu(ctx context.Context, to, text string, choices []string) error
	SendMedia(ctx context.Context, to string, m waMedia) error
	SendLocation(ctx context.Context, to string, loc waLocation) error
	SendContact(ctx context.Context, to string, c waContact) error

	// Instance lifecycle (credential-bound).
	Provision(ctx context.Context, instanceName string) (waProvision, error)
	Connect(ctx context.Context, phone string) (waConnState, error)
	Status(ctx context.Context) (waConnState, error)
	Disconnect(ctx context.Context) error
	Delete(ctx context.Context) error
	RegisterWebhook(ctx context.Context, callbackURL string, events []string) error

	// Inbound parsing (stateless).
	ParseInboundMessage(body []byte) (waInbound, bool)
	ParseConnectionEvent(body []byte) (waConnEvent, bool)
}

// Normalized, provider-agnostic value types.

type waMedia struct {
	Kind     string // "image" | "document"
	URL      string
	Caption  string
	Filename string
}

type waLocation struct {
	Lat, Lng float64
	Name     string
	Address  string
}

type waContact struct {
	FullName     string
	Phone        string
	Organization string
}

// waConnState is the normalized connection state (Status uses the canonical
// values from normalizeUAZAPIConnectionStatus).
type waConnState struct {
	Status         string
	ConnectedPhone string
	QR             string
	PairCode       string
}

// waProvision is what a freshly provisioned instance yields. SessionRef is the
// per-instance credential/id stored in restaurant_uazapi_instances.instance_token;
// ProviderInstanceID is the provider-side instance name/id.
type waProvision struct {
	SessionRef         string
	ProviderInstanceID string
	InstanceName       string
}

// waInbound is a normalized inbound chat message.
type waInbound struct {
	Sender     string
	Text       string
	PushName   string
	MessageID  string
	FromMe     bool
	SessionRef string // instance token (uazapi) or instance name (evolution) for tenant routing
	IsAudio    bool   // voice note; the bot cannot transcribe it
	// Ignored marks non-conversational events (reactions, edits, deletes, poll
	// votes) that must never reach the bot pipeline nor trigger a fallback.
	// Coordination id: wa_bot_ignore_non_conversational_v1
	Ignored bool
	// MediaKind is a human label for a non-text message ("una tarjeta de
	// contacto", "una imagen", ...), used to record staff-sent media.
	MediaKind string
}

// botIgnoredMessageTypes are provider message types that are not a customer
// turn: answering them (e.g. a 👍 reaction) makes the bot look broken.
var botIgnoredMessageTypes = map[string]bool{
	"reactionmessage": true, "protocolmessage": true, "pollupdatemessage": true,
	"editedmessage": true, "senderkeydistributionmessage": true, "keepinchatmessage": true,
	"pininchatmessage": true, "encreactionmessage": true,
}

// botIsIgnoredMessageType reports whether a provider messageType is a
// non-conversational event (matches Evolution and UAZAPI spellings).
func botIsIgnoredMessageType(messageType string) bool {
	t := strings.ToLower(strings.TrimSpace(messageType))
	return botIgnoredMessageTypes[t] || strings.Contains(t, "reaction")
}

// botMediaKindLabel maps a provider messageType to a Spanish label for the
// conversation transcript. Empty for text/unknown types.
func botMediaKindLabel(messageType string) string {
	t := strings.ToLower(messageType)
	switch {
	case strings.Contains(t, "contact"):
		return "una tarjeta de contacto"
	case strings.Contains(t, "image"):
		return "una imagen"
	case strings.Contains(t, "video") || strings.Contains(t, "ptv"):
		return "un vídeo"
	case strings.Contains(t, "audio"):
		return "un audio"
	case strings.Contains(t, "document"):
		return "un documento"
	case strings.Contains(t, "sticker"):
		return "un sticker"
	case strings.Contains(t, "location"):
		return "una ubicación"
	case t == "":
		return ""
	}
	return "un archivo"
}

// waConnEvent is a normalized connection lifecycle event.
type waConnEvent struct {
	SessionRef     string
	Owner          string
	Status         string
	ConnectedPhone string
	QR             string
	PairCode       string
}

// botGatewayFor returns a credentialed gateway for a restaurant's active
// instance, dispatching on the provisioned server's provider. Falls back to the
// legacy UAZAPI credential chokepoint (restaurant_integrations / env) when the
// restaurant has no provisioned instance row.
func (s *Server) botGatewayFor(ctx context.Context, restaurantID int) (WhatsAppGateway, bool) {
	if rec, found, err := s.loadRestaurantUAZAPIInstance(ctx, restaurantID); err == nil && found {
		return s.gatewayForInstance(rec), true
	}
	base, token := s.uazapiBaseAndToken(ctx, restaurantID)
	if base == "" {
		return nil, false
	}
	return &uazapiGateway{s: s, baseURL: base, instanceToken: token}, true
}

// gatewayForInstance builds a credentialed gateway from a provisioned instance
// record (provider-aware).
func (s *Server) gatewayForInstance(rec uazapiInstanceRecord) WhatsAppGateway {
	if rec.Provider == "evolution" {
		return &evolutionGateway{s: s, baseURL: rec.ServerBaseURL, apiKey: rec.ServerAdminToken, instanceName: rec.ProviderInstanceID}
	}
	return &uazapiGateway{s: s, baseURL: rec.ServerBaseURL, adminToken: rec.ServerAdminToken, instanceToken: rec.InstanceToken}
}

// gatewayForServer builds an admin-capable gateway used at provision time
// (before an instance row exists).
func (s *Server) gatewayForServer(server uazapiServerRecord, instanceName string) WhatsAppGateway {
	if server.Provider == "evolution" {
		return &evolutionGateway{s: s, baseURL: server.BaseURL, apiKey: server.AdminToken, instanceName: instanceName}
	}
	return &uazapiGateway{s: s, baseURL: server.BaseURL, adminToken: server.AdminToken}
}

// gatewayForProvider returns a stateless gateway usable only for inbound
// parsing / degradation helpers.
func (s *Server) gatewayForProvider(provider string) WhatsAppGateway {
	if provider == "evolution" {
		return &evolutionGateway{s: s}
	}
	return &uazapiGateway{s: s}
}
