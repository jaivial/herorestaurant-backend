package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Coordination id: campaign_tools_v1
//
// Forky custom tools of /app/campanas (registered in assistant_registry.go,
// section "campanas"). They are thin adapters over the shared campaign core
// (campaign_tools.go): reads answer directly; every write follows the Forky
// confirmation contract — confirmed=false validates and returns the exact
// preview plus a single-use token bound to the arguments, confirmed=true with
// that token executes — except the irreversible actions in a chat, which wait
// for the user's own answer (campaignHumanConfirmActions).

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

// campaignHumanConfirmTTL leaves a person time to read the preview and answer
// (the default Forky token lives 2 minutes, enough only for a model).
const campaignHumanConfirmTTL = 10 * time.Minute

// campaignPendingConfirm is a preview waiting for the user's answer.
type campaignPendingConfirm struct {
	token   string
	input   json.RawMessage
	args    campaignToolArgs
	turn    string
	text    string
	expires time.Time
}

// campaignPending: "restaurant|session|tool|action" -> the previews of that
// action in that chat. Process memory on purpose: prod runs ONE backend
// container and a restart only asks for a fresh preview, it never lets
// anything through. Several replicas would need a shared store (the
// forky_confirmation_tokens table already is one).
var campaignPending sync.Map

func campaignPendingKey(rid int, session int64, tool, action string) string {
	return fmt.Sprintf("%d|%d|%s|%s", rid, session, tool, action)
}

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
	var confirm struct {
		Confirmed bool   `json:"confirmed"`
		Token     string `json:"confirmation_token"`
	}
	_ = json.Unmarshal(input, &confirm)
	turn, inChat := assistantTurnFromContext(ctx)
	if campaignHumanConfirmActions[a.Action] && inChat {
		return s.assistantCampaignHumanWrite(ctx, rid, tool, input, a, confirm.Confirmed, turn)
	}
	if inChat {
		return s.assistantCampaignChatWrite(ctx, rid, tool, input, a, confirm.Confirmed, confirm.Token, turn)
	}
	if !confirm.Confirmed {
		preview, text, err := s.campaignToolPreview(ctx, rid, a)
		if err != nil {
			return "", err
		}
		raw, err := s.assistantRequireConfirmation(rid, tool, input)
		if err != nil {
			return "", err
		}
		out := map[string]any{}
		_ = json.Unmarshal([]byte(raw), &out)
		out["tool"] = tool
		out["preview"] = preview
		out["preview_text"] = "Se va a " + text + "."
		out["instruction"] = "Explica al usuario exactamente lo que se va a hacer (preview_text) y pide su confirmación. Solo si confirma, repite la llamada con los MISMOS argumentos, confirmed=true y este confirmation_token."
		return botJSON(out), nil
	}
	if err := s.assistantConsumeConfirmation(confirm.Token, rid, tool, input); err != nil {
		return "", err
	}
	return s.assistantCampaignExecute(ctx, rid, tool, a)
}

// assistantCampaignChatWrite is the chat flow of the reversible writes
// (create/update, design changes, images). The preview still comes first and
// its token works as always, but a model that rewrites the arguments when it
// confirms (it was seen paraphrasing a long AI instruction, which voided the
// token and burned the turn) no longer fails: confirmed=true with the same
// action and campaign runs the LATEST preview of that action in this chat,
// which is exactly what was validated and described.
func (s *Server) assistantCampaignChatWrite(ctx context.Context, rid int, tool string, input json.RawMessage, a campaignToolArgs, confirmed bool, token string, turn assistantTurn) (string, error) {
	key := campaignPendingKey(rid, turn.Session, tool, a.Action)
	if !confirmed {
		preview, text, err := s.campaignToolPreview(ctx, rid, a)
		if err != nil {
			return "", err
		}
		if s.confirmationStore == nil {
			s.confirmationStore = newConfirmationStore(nil)
		}
		tok, err := s.confirmationStore.Issue("", strconv.Itoa(rid), tool, confirmationArguments(input), "", campaignHumanConfirmTTL)
		if err != nil {
			return "", err
		}
		var set campaignPendingSet
		if v, ok := campaignPending.Load(key); ok {
			set = v.(campaignPendingSet)
		}
		if set.cur != nil && set.cur.turn != turn.ID {
			set.shown = set.cur
		}
		set.cur = &campaignPendingConfirm{token: tok, input: input, args: a, turn: turn.ID, text: text, expires: time.Now().Add(campaignHumanConfirmTTL)}
		campaignPending.Store(key, set)
		return botJSON(map[string]any{
			"tool": tool, "requires_confirmation": true, "confirmation_token": tok, "expires_in_seconds": int(campaignHumanConfirmTTL.Seconds()),
			"preview": preview, "preview_text": "Se va a " + text + ".",
			"instruction": "Si el usuario ya pidió este cambio, confírmalo llamando a esta herramienta con la misma action y campaign_id y confirmed=true (el servidor recuerda esta vista previa y ejecuta exactamente lo que describe; el token es opcional). Si tienes dudas de lo que quiere, enséñale preview_text y pregúntale.",
		}), nil
	}
	// Exact confirmation (same arguments as a preview + its token).
	if strings.TrimSpace(token) != "" {
		if err := s.assistantConsumeConfirmation(token, rid, tool, input); err == nil {
			campaignPending.Delete(key)
			return s.assistantCampaignExecute(ctx, rid, tool, a)
		}
	}
	// Otherwise: the latest preview of this action in this chat.
	var set campaignPendingSet
	if v, ok := campaignPending.Load(key); ok {
		set = v.(campaignPendingSet)
	}
	pending := set.cur
	if pending == nil || time.Now().After(pending.expires) {
		pending = set.shown
	}
	if pending == nil || time.Now().After(pending.expires) {
		campaignPending.Delete(key)
		return "", errors.New("no hay ninguna vista previa pendiente de esta acción en este chat (o caducó): llama primero con confirmed=false")
	}
	if pending.args.CampaignID != a.CampaignID {
		return "", fmt.Errorf("la vista previa pendiente es de la campaña #%d, no de la #%d: pide una vista previa nueva", pending.args.CampaignID, a.CampaignID)
	}
	campaignPending.Delete(key)
	if err := s.assistantConsumeConfirmation(pending.token, rid, tool, pending.input); err != nil {
		return "", err
	}
	out, err := s.assistantCampaignExecute(ctx, rid, tool, pending.args)
	if err != nil || campaignSameArgs(pending.input, input) {
		return out, err
	}
	// The model confirmed with other arguments: say plainly what ran, so it
	// never believes a correction it made at confirm time was applied.
	slog.Default().Info("campaign.tools.confirm_previewed", "restaurant_id", rid, "tool", tool, "action", a.Action, "campaign_id", a.CampaignID)
	var result map[string]any
	if json.Unmarshal([]byte(out), &result) != nil {
		return out, nil
	}
	result["executed_preview"] = "Se ha ejecutado la vista previa: " + pending.text + "."
	result["note"] = "Los argumentos de tu confirmación no coincidían con la vista previa y NO se han usado: se ha ejecutado exactamente lo previsualizado. Si querías otro cambio, pide una vista previa nueva (confirmed=false) con los argumentos correctos."
	return botJSON(result), nil
}

// campaignSameArgs compares two tool inputs without the confirmation fields.
func campaignSameArgs(a, b json.RawMessage) bool {
	return confirmationArguments(a) == confirmationArguments(b)
}

// campaignPendingSet is the preview state of one action in one chat: the
// latest preview of an EARLIER turn (what the user had in front of them when
// they wrote their last message) and the latest preview of the current turn.
type campaignPendingSet struct {
	shown *campaignPendingConfirm
	cur   *campaignPendingConfirm
}

// seen returns the preview the user saw before the message of turn: the one
// their "sí" refers to.
func (p campaignPendingSet) seen(turn string) *campaignPendingConfirm {
	if p.cur != nil && p.cur.turn != turn {
		return p.cur
	}
	return p.shown
}

// assistantCampaignHumanWrite is the chat flow of the irreversible actions:
// preview -> the user answers in a later message -> run what they saw.
func (s *Server) assistantCampaignHumanWrite(ctx context.Context, rid int, tool string, input json.RawMessage, a campaignToolArgs, confirmed bool, turn assistantTurn) (string, error) {
	key := campaignPendingKey(rid, turn.Session, tool, a.Action)
	var set campaignPendingSet
	if v, ok := campaignPending.Load(key); ok {
		set = v.(campaignPendingSet)
	}
	seen := set.seen(turn.ID)
	if seen != nil && time.Now().After(seen.expires) {
		seen = nil
	}
	if !confirmed {
		preview, text, err := s.campaignToolPreview(ctx, rid, a)
		if err != nil {
			return "", err
		}
		// Asking again for the preview the user already saw (models do it on
		// the user's "sí") must not push the confirmation one more message away.
		if seen != nil && campaignArgsFingerprint(seen.args) == campaignArgsFingerprint(a) {
			return botJSON(map[string]any{
				"tool": tool, "pending_user_confirmation": true, "already_previewed": true,
				"preview": preview, "preview_text": "Se va a " + seen.text + ".",
				"instruction": "El usuario ya vio esta misma vista previa en un mensaje anterior. Si en su último mensaje ha dicho que sí, llama AHORA a esta herramienta con la misma action y campaign_id y confirmed=true. Si no, pregúntale si la confirma.",
			}), nil
		}
		if s.confirmationStore == nil {
			s.confirmationStore = newConfirmationStore(nil)
		}
		token, err := s.confirmationStore.Issue("", strconv.Itoa(rid), tool, confirmationArguments(input), "", campaignHumanConfirmTTL)
		if err != nil {
			return "", err
		}
		if set.cur != nil && set.cur.turn != turn.ID {
			set.shown = set.cur
		}
		set.cur = &campaignPendingConfirm{token: token, input: input, args: a, turn: turn.ID, text: text, expires: time.Now().Add(campaignHumanConfirmTTL)}
		campaignPending.Store(key, set)
		out := map[string]any{
			"tool": tool, "requires_confirmation": true, "pending_user_confirmation": true,
			"preview": preview, "preview_text": "Se va a " + text + ".", "expires_in_seconds": int(campaignHumanConfirmTTL.Seconds()),
			"instruction": "Necesita la confirmación EXPRESA del usuario: termina aquí tu respuesta mostrándole preview_text tal cual (con el destinatario o la audiencia exactos) y preguntándole si lo confirma. Cuando responda que sí en su siguiente mensaje, llama DIRECTAMENTE a esta herramienta con la misma action y campaign_id y confirmed=true (no pidas otra vista previa): el servidor ejecuta exactamente lo que vio el usuario. Si quiere cambiar algo, pide una vista previa nueva.",
		}
		if seen != nil {
			// The user's last message answered a DIFFERENT preview: that one
			// is still what a confirmed=true in this turn would run.
			out["previous_preview_text"] = "Se va a " + seen.text + "."
			out["instruction"] = "Esta vista previa es DISTINTA de la que el usuario vio en su mensaje anterior (previous_preview_text). Si el usuario acaba de confirmar aquella, llama ahora con confirmed=true y se ejecutará aquella, tal cual la vio. Si lo que quiere es esta nueva, termina tu respuesta enseñándole preview_text y espera su confirmación."
		}
		return botJSON(out), nil
	}
	if seen == nil {
		if set.cur != nil && set.cur.turn == turn.ID {
			return "", errors.New("el usuario todavía no ha confirmado: termina tu respuesta mostrándole la vista previa y espera a su respuesta; la confirmación solo vale en su siguiente mensaje")
		}
		campaignPending.Delete(key)
		return "", errors.New("no hay ninguna confirmación pendiente de esta acción en este chat (o caducó): pide la vista previa con confirmed=false y espera a que el usuario la confirme")
	}
	if seen.args.CampaignID != a.CampaignID {
		return "", fmt.Errorf("la confirmación pendiente es de la campaña #%d, no de la #%d: pide una vista previa nueva", seen.args.CampaignID, a.CampaignID)
	}
	campaignPending.Delete(key)
	if err := s.assistantConsumeConfirmation(seen.token, rid, tool, seen.input); err != nil {
		return "", err
	}
	// What runs is what the user saw: the previewed arguments, not the
	// arguments of this call.
	return s.assistantCampaignExecute(ctx, rid, tool, seen.args)
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
