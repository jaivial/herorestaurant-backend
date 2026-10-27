package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Coordination id: campaign_tools_v1
//
// Forky custom tools of /app/campanas (registered in assistant_registry.go,
// section "campanas"). They are thin adapters over the shared campaign core
// (campaign_tools.go): reads answer directly; every write goes through the
// Forky confirmation flow of the editor tools (assistant_tool_flow.go), the
// irreversible actions in a chat waiting for the user's own answer
// (campaignHumanConfirmActions).

// campaignAssistantToolTimeouts bound each campaign tool: the design engine
// compiles Tailwind and the AI rewrite can take minutes on reasoning models
// (campaignAITimeout), far beyond the 5s default of the other Forky tools.
var campaignAssistantToolTimeouts = map[string]time.Duration{
	"campaign_get":    90 * time.Second,
	"campaign_save":   90 * time.Second,
	"campaign_design": campaignAITimeout + time.Minute,
	"campaign_image":  90 * time.Second,
	"campaign_send":   90 * time.Second,
	"campaign_delete": 30 * time.Second,
}

// assistantTurn identifies where a Forky tool runs: the chat session and the
// turn (one user message). Set by the assistant websocket only.
type assistantTurn struct {
	Session int64
	ID      string
}

type assistantTurnKey struct{}

func withAssistantTurn(ctx context.Context, session int64, id string) context.Context {
	return context.WithValue(ctx, assistantTurnKey{}, assistantTurn{Session: session, ID: id})
}

func assistantTurnFromContext(ctx context.Context) (assistantTurn, bool) {
	t, ok := ctx.Value(assistantTurnKey{}).(assistantTurn)
	return t, ok && t.ID != ""
}

// campaignHumanConfirmActions reach people or cannot be undone (a test to an
// address the model transcribed, the real send, resuming it, deleting the
// campaign). In a Forky chat they need the USER's yes, not the model's:
//
//   - the preview (confirmed=false) never hands a token to the model; the
//     server keeps the pending confirmation of that chat session instead
//     (what was previewed, exact arguments included);
//   - confirmed=true only works from a LATER user message than the preview,
//     and runs exactly what the user saw, whatever the model rewrites.
//
// Forky's history keeps only the text of past turns (never tool results), so
// a token could not survive to the user's answer anyway. Outside a Forky chat
// (no session/turn in the context, e.g. MCP) the regular token contract
// applies. The WhatsApp twin gets the same guarantee from "OK <código>".
var campaignHumanConfirmActions = map[string]bool{"test": true, "send": true, "resume": true, "delete": true}

// campaignArgsFingerprint is what a person confirms in an irreversible
// preview: the action, the campaign and who it reaches. Cosmetic differences
// between two calls of the model (an omitted default channel, spacing, case)
// do not make it a different preview; another target does.
func campaignArgsFingerprint(a campaignToolArgs) string {
	normalizeCampaignToolArgs(&a)
	channel := a.Channel
	if a.Action == "test" {
		channel, _ = campaignToolTestChannel(a)
	}
	return fmt.Sprintf("%s|%d|%s|%s|%s", a.Action, a.CampaignID, channel, strings.ToLower(a.Target), a.Ack)
}

// campaignClaimRe spots a reply that states a campaign action was carried out.
var campaignClaimRe = regexp.MustCompile(`(?i)(campa[ñn]a|prueba|dise[ñn]o|env[ií]o|email|correo|bot[oó]n|titular|bloque)[^\n]{0,90}(enviad[oa]s?|aplicad[oa]s?|guardad[oa]s?|eliminad[oa]s?|borrad[oa]s?|lanzad[oa]s?|cread[oa]s?|actualizad[oa]s?|deshech[oa]s?|cambiad[oa]s?|a[ñn]adid[oa]s?|programad[oa]s?)`)

// campaignUnbackedClaimNotice is appended (and persisted) when a Forky reply
// claims a campaign action in a turn where NO tool ran at all: MiniMax was
// seen narrating "prueba enviada ✅" after earlier tool turns without calling
// anything. A false claim of a send is worse than an extra line of text.
const campaignUnbackedClaimNotice = "\n\n⚠️ Aviso del sistema: en esta respuesta no se ha ejecutado ninguna herramienta de campañas, así que no se ha enviado, guardado ni cambiado nada. Pídelo de nuevo para hacerlo de verdad."

// campaignUnbackedClaim reports whether, in a turn about campaigns where no
// tool ran at all, the reply states a campaign action was carried out. Any
// tool run (even admin_call) means the claim may be real: it is not judged.
func campaignUnbackedClaim(userText, reply string, ranTools map[string]bool) bool {
	if len(ranTools) > 0 || !strings.Contains(strings.ToLower(userText), "campa") {
		return false
	}
	return campaignClaimRe.MatchString(reply)
}

// assistantCampaignGet is campaign_get: list or one campaign (+ include).
func (s *Server) assistantCampaignGet(ctx context.Context, rid int, input json.RawMessage) (string, error) {
	var a campaignToolArgs
	if err := json.Unmarshal(input, &a); err != nil {
		return "", errors.New("argumentos ilegibles")
	}
	a.Action = "get"
	out, _, err := s.campaignToolRun(ctx, rid, a, "forky")
	if err != nil {
		return "", err
	}
	out["tool"] = "campaign_get"
	return botJSON(out), nil
}

// campaignWriteFlow binds the campaign tools to the shared Forky
// confirmation flow (assistant_tool_flow.go).
var campaignWriteFlow = assistantWriteFlow[campaignToolArgs]{
	IDField:     "campaign_id",
	Of:          "de la campaña",
	Human:       campaignHumanConfirmActions,
	Action:      func(a campaignToolArgs) string { return a.Action },
	Target:      func(a campaignToolArgs) int64 { return a.CampaignID },
	Fingerprint: campaignArgsFingerprint,
	Preview:     (*Server).campaignToolPreview,
	Execute:     (*Server).assistantCampaignExecute,
	Log:         "campaign.tools",
}

// assistantCampaignWrite runs one campaign write through the Forky
// confirmation contract. mapAction turns the tool's own arguments into the
// core action (create/update, design_*, test/send/resume, delete).
func (s *Server) assistantCampaignWrite(ctx context.Context, rid int, tool string, input json.RawMessage, mapAction func(a *campaignToolArgs) error) (string, error) {
	var a campaignToolArgs
	if err := json.Unmarshal(input, &a); err != nil {
		return "", errors.New("argumentos ilegibles")
	}
	if err := mapAction(&a); err != nil {
		return "", err
	}
	return assistantFlowWrite(s, ctx, rid, tool, input, a, campaignWriteFlow)
}

func (s *Server) assistantCampaignExecute(ctx context.Context, rid int, tool string, a campaignToolArgs) (string, error) {
	out, summary, err := s.campaignToolRun(ctx, rid, a, "forky")
	if err != nil {
		return "", err
	}
	out["tool"] = tool
	out["summary"] = summary
	return botJSON(out), nil
}

func (s *Server) assistantCampaignSave(ctx context.Context, rid int, input json.RawMessage) (string, error) {
	return s.assistantCampaignWrite(ctx, rid, "campaign_save", input, func(a *campaignToolArgs) error {
		a.Action = "update"
		if a.CampaignID <= 0 {
			a.Action = "create"
		} else if strings.TrimSpace(a.DesignHTML) != "" {
			return errors.New("design_html solo sirve al crear: para cambiar el diseño de una campaña existente usa campaign_design")
		}
		return nil
	})
}

func (s *Server) assistantCampaignDesign(ctx context.Context, rid int, input json.RawMessage) (string, error) {
	return s.assistantCampaignWrite(ctx, rid, "campaign_design", input, func(a *campaignToolArgs) error {
		switch strings.ToLower(strings.TrimSpace(a.Action)) {
		case "edit":
			a.Action = "design_edit"
		case "ai":
			a.Action = "design_ai"
		case "undo":
			a.Action = "design_undo"
		default:
			return errors.New("action debe ser edit, ai o undo")
		}
		return nil
	})
}

// assistantCampaignImage is campaign_image: an image attached in this chat,
// uploaded to the restaurant's CDN and placed in the design.
func (s *Server) assistantCampaignImage(ctx context.Context, rid int, input json.RawMessage) (string, error) {
	return s.assistantCampaignWrite(ctx, rid, "campaign_image", input, func(a *campaignToolArgs) error {
		a.Action = "image"
		// The CDN URL and the WhatsApp ref are server-side: a Forky image
		// always comes from an attachment of the restaurant's own chat.
		a.ImageURL, a.ImageName, a.ImageRef = "", "", ""
		if strings.TrimSpace(a.Attachment) == "" {
			return errors.New("falta attachment: la ref de la imagen adjunta (aparece como «ref …» en la línea [Adjunto: …])")
		}
		return nil
	})
}

func (s *Server) assistantCampaignSend(ctx context.Context, rid int, input json.RawMessage) (string, error) {
	return s.assistantCampaignWrite(ctx, rid, "campaign_send", input, func(a *campaignToolArgs) error {
		switch strings.ToLower(strings.TrimSpace(a.Action)) {
		case "test", "send", "resume":
			a.Action = strings.ToLower(strings.TrimSpace(a.Action))
			return nil
		}
		return errors.New("action debe ser test, send o resume")
	})
}

func (s *Server) assistantCampaignDelete(ctx context.Context, rid int, input json.RawMessage) (string, error) {
	return s.assistantCampaignWrite(ctx, rid, "campaign_delete", input, func(a *campaignToolArgs) error {
		a.Action = "delete"
		return nil
	})
}
