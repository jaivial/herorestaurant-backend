package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// Coordination id: ads_html_editor_v1 (WhatsApp half)
//
// manage_ad: the web ads editor of /app/anuncios for the allowed (privileged)
// contacts and groups of the WhatsApp bot. ONE parameterised tool over the ad
// core (ad_tools.go), exactly like manage_campaign: reads answer at once and
// every write becomes a propose+OK proposal (op "ad") that only the requester
// applies with "OK <código>" in the same chat within the TTL.

func botToolDefsAd() []botToolDef {
	return []botToolDef{{
		Name: "manage_ad",
		Description: "Gestiona los anuncios de la web (avisos emergentes sobre la web del restaurante) igual que el editor /app/anuncios. " +
			"Lecturas inmediatas: action 'list' (anuncios) y 'get' (un anuncio y sus páginas: main = el anuncio o la página de tarjetas de un anuncio múltiple, y una página de detalle por tarjeta con su page y title; include layers = capas con ids e1, e2…, html). " +
			"Escrituras (NO se aplican: se envía al chat una propuesta con código y quien la pidió confirma con 'OK <código>'): " +
			"'create' (name obligatorio; kind single = individual o multiple = propuestas en tarjetas que abren cada una su página de detalle; nace inactivo), " +
			"'update' (name, starts_at+ends_at YYYY-MM-DD o always=true (sin fechas no aparece en la web), kind, max_width 280-1200, radius 0-48, background, overlay; en un anuncio múltiple page + step_starts_at+step_ends_at programan una tarjeta, o page + step_always=true para que siga las fechas del anuncio), 'convert' (pasa al nuevo editor un anuncio hecho con el editor anterior), " +
			"'publish' (active true lo publica en la web, false lo retira), 'design_ai' (instruction para el asistente de diseño con IA sobre la página page, main por defecto; element_id y model opcionales; en la página de tarjetas una tarjeta nueva crea su página de detalle), " +
			"'design_undo' (deshace el último cambio de diseño hecho desde un chat), 'image' (usa una IMAGEN ENVIADA a este chat: image_ref o vacío para la más reciente; page; placement replace = sustituye element_id o la primera imagen de la página, insert = la añade al final, none = solo la sube y devuelve la URL; alt opcional) y 'delete'. " +
			"Un anuncio activo se ve al momento en la web, también sus cambios de diseño. Propón UN cambio cada vez y espera al OK antes del siguiente. Para anuncios usa SIEMPRE esta herramienta, nunca panel_change ni admin_call.",
		InputSchema: botSchema(`{"type":"object","properties":{
			"action":{"type":"string","enum":["list","get","create","update","convert","publish","design_ai","design_undo","image","delete"]},
			"ad_id":{"type":"integer","description":"ID del anuncio (todas menos list y create)"},
			"include":{"type":"array","items":{"type":"string","enum":["layers","html"]}},
			"name":{"type":"string"},"kind":{"type":"string","enum":["single","multiple"]},
			"starts_at":{"type":"string"},"ends_at":{"type":"string"},"always":{"type":"boolean"},
			"max_width":{"type":"integer"},"radius":{"type":"integer"},"background":{"type":"string"},"overlay":{"type":"string"},
			"active":{"type":"boolean","description":"Solo publish"},
			"page":{"type":"string","description":"main o el page/título de un paso"},
			"step_starts_at":{"type":"string"},"step_ends_at":{"type":"string"},"step_always":{"type":"boolean"},
			"instruction":{"type":"string","description":"Solo design_ai"},
			"element_id":{"type":"string"},"model":{"type":"string"},
			"image_ref":{"type":"string","description":"Solo image: id de la imagen del chat (vacío = la más reciente)"},
			"placement":{"type":"string","enum":["replace","insert","none"]},"alt":{"type":"string"}
		},"required":["action"]}`),
	}}
}

// botToolManageAd executes manage_ad.
func (s *Server) botToolManageAd(ctx context.Context, restaurantID int, msg botWebhookMessage, input json.RawMessage) (string, error) {
	var a adToolArgs
	if err := json.Unmarshal(input, &a); err != nil {
		return botJSON(map[string]any{"error": "argumentos ilegibles"}), nil
	}
	normalizeAdToolArgs(&a)
	if a.Action == "" {
		return botJSON(map[string]any{"error": "falta action"}), nil
	}
	actor, origin, ok := s.botPrivilegedControlAuthorize(ctx, restaurantID, msg)
	if !ok {
		botCheckpoint("ads_html_editor_v1", "restaurant_id=%d denied=actor action=%s", restaurantID, a.Action)
		return botJSON(map[string]any{"error": "no autorizado para gestionar anuncios desde este chat"}), nil
	}
	// A Forky attachment never comes from WhatsApp; the CDN URL is server-side.
	a.Attachment, a.ImageURL, a.ImageName = "", "", ""
	if !adToolWriteActions[a.Action] {
		out, _, err := s.adToolRun(ctx, restaurantID, a, "whatsapp")
		if err != nil {
			return botJSON(map[string]any{"error": err.Error()}), nil
		}
		botCheckpoint("ads_html_editor_v1", "restaurant_id=%d read action=%s ad_id=%d", restaurantID, a.Action, a.AdID)
		out["tool"] = "manage_ad"
		return botJSON(out), nil
	}
	if a.Action == "image" {
		// The bytes of a chat image only live in this turn (or the provider):
		// upload now, so the proposal carries the CDN URL and the "OK" never
		// depends on them. The upload alone changes nothing visible.
		ad, err := s.adToolLoad(ctx, restaurantID, a.AdID)
		if err != nil {
			return botJSON(map[string]any{"error": err.Error()}), nil
		}
		probe := a
		probe.ImageURL = "https://example.invalid/vista-previa.jpg"
		if _, _, err := s.adToolPreview(ctx, restaurantID, probe); err != nil {
			return botJSON(map[string]any{"error": err.Error()}), nil
		}
		img, err := s.botChatImageSource(ctx, restaurantID, msg, a.ImageRef)
		if err != nil {
			return botJSON(map[string]any{"error": err.Error()}), nil
		}
		url, err := s.saveBOAdImage(ctx, restaurantID, ad.ID, img.Data, img.Name, img.ContentType, "design")
		if err != nil {
			return botJSON(map[string]any{"error": "no se pudo subir la imagen al CDN del restaurante"}), nil
		}
		a.ImageURL, a.ImageName = url, "imagen de WhatsApp"
		botCheckpoint("ads_html_editor_v1", "restaurant_id=%d chat_image_uploaded ad_id=%d placement=%s", restaurantID, ad.ID, a.Placement)
		if a.Placement == "none" {
			return botJSON(map[string]any{"ok": true, "tool": "manage_ad", "url": url, "status": "Imagen subida al CDN del restaurante; el anuncio no ha cambiado."}), nil
		}
	}
	_, text, err := s.adToolPreview(ctx, restaurantID, a)
	if err != nil {
		return botJSON(map[string]any{"error": err.Error()}), nil
	}
	if a.Action == "image" && a.ElementID != "" && a.Version == "" {
		// The element id addresses the design as it is NOW: an OK given
		// after another change is refused instead of editing another image.
		if ad, err := s.adToolLoad(ctx, restaurantID, a.AdID); err == nil {
			a.Version = adDesignFingerprint(ad.Design)
		}
	}
	code, err := botSpecialNewCode()
	if err != nil {
		return botJSON(map[string]any{"error": "no se pudo generar el código de confirmación"}), nil
	}
	preview := "Propuesta: " + text + ". Nada está aplicado todavía."
	dedup := fmt.Sprintf("ad:%s:%d", a.Action, a.AdID)
	out, err := s.botProposalPersist(ctx, restaurantID, msg, "ad", 0, a, dedup, actor, origin, "", "", preview, code)
	if err != nil {
		return botJSON(map[string]any{"error": "no se pudo guardar la propuesta"}), nil
	}
	botCheckpoint("ads_html_editor_v1", "restaurant_id=%d proposed action=%s ad_id=%d", restaurantID, a.Action, a.AdID)
	return out, nil
}

// botAdExecute applies an approved ad proposal. Dispatched by
// botSpecialAdminExecute for op "ad": the claim commits ONLY with a
// successful change, so a failed run leaves the code retryable.
func (s *Server) botAdExecute(ctx context.Context, tx *sql.Tx, rid int, p botSpecialProposalRow) (string, error) {
	var a adToolArgs
	if err := json.Unmarshal([]byte(p.Args), &a); err != nil {
		return "", errors.New("propuesta ilegible")
	}
	_, summary, err := s.adToolRun(ctx, rid, a, "whatsapp")
	if err != nil {
		botCheckpoint("ads_html_editor_v1", "restaurant_id=%d apply_failed action=%s ad_id=%d err=%v", rid, a.Action, a.AdID, err)
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", errors.New("error interno")
	}
	botCheckpoint("ads_html_editor_v1", "restaurant_id=%d applied action=%s ad_id=%d", rid, a.Action, a.AdID)
	return "Aplicado: " + summary, nil
}
