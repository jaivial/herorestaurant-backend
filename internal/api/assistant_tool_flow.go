package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Coordination ids: campaign_tools_v1, ads_html_editor_v1
//
// The Forky confirmation flow of the editor tools (campaigns, web ads), one
// implementation for every family of tools. Every write follows the Forky
// confirmation contract — confirmed=false validates and returns the exact
// preview plus a single-use token bound to the arguments, confirmed=true with
// that token executes — with two chat refinements:
//
//   - reversible writes (assistantFlowChatWrite): confirmed=true with the same
//     action and target runs the LATEST preview of that action in this chat,
//     even if the model rewrote the arguments when confirming;
//   - actions that reach people or cannot be undone (assistantFlowHumanWrite):
//     the preview never hands a token to the model; only a LATER user message
//     confirms, and what runs is exactly what the user saw.
//
// Outside a Forky chat (no session/turn in the context, e.g. MCP) the regular
// token contract applies.

// assistantWriteFlow describes one family of write tools.
type assistantWriteFlow[A any] struct {
	// IDField is the argument naming the target ("campaign_id", "ad_id").
	IDField string
	// Of names a target in messages: "de la campaña", "del anuncio".
	Of string
	// Human are the actions that need the user's own yes in a chat.
	Human       map[string]bool
	Action      func(A) string
	Target      func(A) int64
	Fingerprint func(A) string
	Preview     func(s *Server, ctx context.Context, rid int, a A) (map[string]any, string, error)
	Execute     func(s *Server, ctx context.Context, rid int, tool string, a A) (string, error)
	// Log prefix of the flow events ("campaign.tools", "ad.tools").
	Log string
}

// assistantHumanConfirmTTL leaves a person time to read the preview and
// answer (the default Forky token lives 2 minutes, enough only for a model).
const assistantHumanConfirmTTL = 10 * time.Minute

// assistantPendingConfirm is a preview waiting for its confirmation.
type assistantPendingConfirm[A any] struct {
	token   string
	input   json.RawMessage
	args    A
	turn    string
	text    string
	expires time.Time
}

// assistantPendingSet is the preview state of one action in one chat: the
// latest preview of an EARLIER turn (what the user had in front of them when
// they wrote their last message) and the latest preview of the current turn.
type assistantPendingSet[A any] struct {
	shown *assistantPendingConfirm[A]
	cur   *assistantPendingConfirm[A]
}

// seen returns the preview the user saw before the message of turn: the one
// their "sí" refers to.
func (p assistantPendingSet[A]) seen(turn string) *assistantPendingConfirm[A] {
	if p.cur != nil && p.cur.turn != turn {
		return p.cur
	}
	return p.shown
}

// assistantPending: "restaurant|session|tool|action" -> the previews of that
// action in that chat. Process memory on purpose: prod runs ONE backend
// container and a restart only asks for a fresh preview, it never lets
// anything through. Several replicas would need a shared store (the
// forky_confirmation_tokens table already is one).
var assistantPending sync.Map

func assistantPendingKey(rid int, session int64, tool, action string) string {
	return fmt.Sprintf("%d|%d|%s|%s", rid, session, tool, action)
}

func assistantPendingLoad[A any](key string) assistantPendingSet[A] {
	if v, ok := assistantPending.Load(key); ok {
		if set, ok := v.(assistantPendingSet[A]); ok {
			return set
		}
	}
	return assistantPendingSet[A]{}
}

// assistantSameArgs compares two tool inputs without the confirmation fields.
func assistantSameArgs(a, b json.RawMessage) bool {
	return confirmationArguments(a) == confirmationArguments(b)
}

// assistantFlowWrite runs one write of a tool family through the contract.
func assistantFlowWrite[A any](s *Server, ctx context.Context, rid int, tool string, input json.RawMessage, a A, f assistantWriteFlow[A]) (string, error) {
	var confirm struct {
		Confirmed bool   `json:"confirmed"`
		Token     string `json:"confirmation_token"`
	}
	_ = json.Unmarshal(input, &confirm)
	turn, inChat := assistantTurnFromContext(ctx)
	if f.Human[f.Action(a)] && inChat {
		return assistantFlowHumanWrite(s, ctx, rid, tool, input, a, confirm.Confirmed, turn, f)
	}
	if inChat {
		return assistantFlowChatWrite(s, ctx, rid, tool, input, a, confirm.Confirmed, confirm.Token, turn, f)
	}
	if !confirm.Confirmed {
		preview, text, err := f.Preview(s, ctx, rid, a)
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
	return f.Execute(s, ctx, rid, tool, a)
}

// assistantFlowChatWrite is the chat flow of the reversible writes. The
// preview still comes first and its token works as always, but a model that
// rewrites the arguments when it confirms (it was seen paraphrasing a long AI
// instruction, which voided the token and burned the turn) no longer fails:
// confirmed=true with the same action and target runs the LATEST preview of
// that action in this chat, which is exactly what was validated and described.
func assistantFlowChatWrite[A any](s *Server, ctx context.Context, rid int, tool string, input json.RawMessage, a A, confirmed bool, token string, turn assistantTurn, f assistantWriteFlow[A]) (string, error) {
	key := assistantPendingKey(rid, turn.Session, tool, f.Action(a))
	if !confirmed {
		preview, text, err := f.Preview(s, ctx, rid, a)
		if err != nil {
			return "", err
		}
		if s.confirmationStore == nil {
			s.confirmationStore = newConfirmationStore(nil)
		}
		tok, err := s.confirmationStore.Issue("", strconv.Itoa(rid), tool, confirmationArguments(input), "", assistantHumanConfirmTTL)
		if err != nil {
			return "", err
		}
		set := assistantPendingLoad[A](key)
		if set.cur != nil && set.cur.turn != turn.ID {
			set.shown = set.cur
		}
		set.cur = &assistantPendingConfirm[A]{token: tok, input: input, args: a, turn: turn.ID, text: text, expires: time.Now().Add(assistantHumanConfirmTTL)}
		assistantPending.Store(key, set)
		return botJSON(map[string]any{
			"tool": tool, "requires_confirmation": true, "confirmation_token": tok, "expires_in_seconds": int(assistantHumanConfirmTTL.Seconds()),
			"preview": preview, "preview_text": "Se va a " + text + ".",
			"instruction": "Si el usuario ya pidió este cambio, confírmalo llamando a esta herramienta con la misma action y " + f.IDField + " y confirmed=true (el servidor recuerda esta vista previa y ejecuta exactamente lo que describe; el token es opcional). Si tienes dudas de lo que quiere, enséñale preview_text y pregúntale.",
		}), nil
	}
	// Exact confirmation (same arguments as a preview + its token).
	if strings.TrimSpace(token) != "" {
		if err := s.assistantConsumeConfirmation(token, rid, tool, input); err == nil {
			assistantPending.Delete(key)
			return f.Execute(s, ctx, rid, tool, a)
		}
	}
	// Otherwise: the latest preview of this action in this chat.
	set := assistantPendingLoad[A](key)
	pending := set.cur
	if pending == nil || time.Now().After(pending.expires) {
		pending = set.shown
	}
	if pending == nil || time.Now().After(pending.expires) {
		assistantPending.Delete(key)
		return "", errors.New("no hay ninguna vista previa pendiente de esta acción en este chat (o caducó): llama primero con confirmed=false")
	}
	if f.Target(pending.args) != f.Target(a) {
		return "", fmt.Errorf("la vista previa pendiente es %s #%d, no %s #%d: pide una vista previa nueva", f.Of, f.Target(pending.args), f.Of, f.Target(a))
	}
	assistantPending.Delete(key)
	if err := s.assistantConsumeConfirmation(pending.token, rid, tool, pending.input); err != nil {
		return "", err
	}
	out, err := f.Execute(s, ctx, rid, tool, pending.args)
	if err != nil || assistantSameArgs(pending.input, input) {
		return out, err
	}
	// The model confirmed with other arguments: say plainly what ran, so it
	// never believes a correction it made at confirm time was applied.
	slog.Default().Info(f.Log+".confirm_previewed", "restaurant_id", rid, "tool", tool, "action", f.Action(a), f.IDField, f.Target(a))
	var result map[string]any
	if json.Unmarshal([]byte(out), &result) != nil {
		return out, nil
	}
	result["executed_preview"] = "Se ha ejecutado la vista previa: " + pending.text + "."
	result["note"] = "Los argumentos de tu confirmación no coincidían con la vista previa y NO se han usado: se ha ejecutado exactamente lo previsualizado. Si querías otro cambio, pide una vista previa nueva (confirmed=false) con los argumentos correctos."
	return botJSON(result), nil
}

// assistantFlowHumanWrite is the chat flow of the actions that reach people
// or cannot be undone: preview -> the user answers in a later message -> run
// what they saw.
func assistantFlowHumanWrite[A any](s *Server, ctx context.Context, rid int, tool string, input json.RawMessage, a A, confirmed bool, turn assistantTurn, f assistantWriteFlow[A]) (string, error) {
	key := assistantPendingKey(rid, turn.Session, tool, f.Action(a))
	set := assistantPendingLoad[A](key)
	seen := set.seen(turn.ID)
	if seen != nil && time.Now().After(seen.expires) {
		seen = nil
	}
	if !confirmed {
		preview, text, err := f.Preview(s, ctx, rid, a)
		if err != nil {
			return "", err
		}
		// Asking again for the preview the user already saw (models do it on
		// the user's "sí") must not push the confirmation one more message away.
		if seen != nil && f.Fingerprint(seen.args) == f.Fingerprint(a) {
			return botJSON(map[string]any{
				"tool": tool, "pending_user_confirmation": true, "already_previewed": true,
				"preview": preview, "preview_text": "Se va a " + seen.text + ".",
				"instruction": "El usuario ya vio esta misma vista previa en un mensaje anterior. Si en su último mensaje ha dicho que sí, llama AHORA a esta herramienta con la misma action y " + f.IDField + " y confirmed=true. Si no, pregúntale si la confirma.",
			}), nil
		}
		if s.confirmationStore == nil {
			s.confirmationStore = newConfirmationStore(nil)
		}
		token, err := s.confirmationStore.Issue("", strconv.Itoa(rid), tool, confirmationArguments(input), "", assistantHumanConfirmTTL)
		if err != nil {
			return "", err
		}
		if set.cur != nil && set.cur.turn != turn.ID {
			set.shown = set.cur
		}
		set.cur = &assistantPendingConfirm[A]{token: token, input: input, args: a, turn: turn.ID, text: text, expires: time.Now().Add(assistantHumanConfirmTTL)}
		assistantPending.Store(key, set)
		out := map[string]any{
			"tool": tool, "requires_confirmation": true, "pending_user_confirmation": true,
			"preview": preview, "preview_text": "Se va a " + text + ".", "expires_in_seconds": int(assistantHumanConfirmTTL.Seconds()),
			"instruction": "Necesita la confirmación EXPRESA del usuario: termina aquí tu respuesta mostrándole preview_text tal cual (con el destinatario o la audiencia exactos) y preguntándole si lo confirma. Cuando responda que sí en su siguiente mensaje, llama DIRECTAMENTE a esta herramienta con la misma action y " + f.IDField + " y confirmed=true (no pidas otra vista previa): el servidor ejecuta exactamente lo que vio el usuario. Si quiere cambiar algo, pide una vista previa nueva.",
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
		assistantPending.Delete(key)
		return "", errors.New("no hay ninguna confirmación pendiente de esta acción en este chat (o caducó): pide la vista previa con confirmed=false y espera a que el usuario la confirme")
	}
	if f.Target(seen.args) != f.Target(a) {
		return "", fmt.Errorf("la confirmación pendiente es %s #%d, no %s #%d: pide una vista previa nueva", f.Of, f.Target(seen.args), f.Of, f.Target(a))
	}
	assistantPending.Delete(key)
	if err := s.assistantConsumeConfirmation(seen.token, rid, tool, seen.input); err != nil {
		return "", err
	}
	// What runs is what the user saw: the previewed arguments, not the
	// arguments of this call.
	return f.Execute(s, ctx, rid, tool, seen.args)
}
