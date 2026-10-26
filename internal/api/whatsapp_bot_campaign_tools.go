package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// Coordination id: campaign_tools_v1 (WhatsApp half)
//
// manage_campaign: the campaign editor of /app/campanas for the allowed
// (privileged) contacts and groups of the WhatsApp bot. ONE parameterised tool
// (minimum common multiple, like manage_bot_access and panel_change) over the
// shared campaign core (campaign_tools.go):
//
//   - reads (list / get + include) answer at once;
//   - every write becomes a propose+OK proposal (op "campaign"): the exact
//     preview goes to the chat with a code and only the requester, in the same
//     chat and within the TTL, applies it with "OK <código>". Tenants with
//     privileged direct writes apply it immediately (botProposalPersist).
//
// It lives in the privileged control catalog, so it is advertised only on
// privileged turns and refused server-side everywhere else. It is NOT in an
// intent group (wa_bot_intent_catalog_v1): an edit conversation goes on with
// follow-ups that never say "campaña" ("pon el botón en verde"), and pruning
// it there would cut the conversation in half.

func botToolDefsCampaign() []botToolDef {
	return []botToolDef{{
		Name: "manage_campaign",
		Description: "Gestiona las campañas de marketing (email/WhatsApp) igual que el editor /app/campanas. " +
			"Lecturas inmediatas: action 'list' (campañas) y 'get' (una campaña: ajustes, estado, flujo y resumen del diseño con su version; include: layers = capas con ids e1, e2… para design_edit, html, whatsapp_text, audience = recuento sin datos personales, deliveries = registro de envíos agregado, ai_models, blocks = catálogo de bloques y grupos de estilo). " +
			"Escrituras (NO se aplican: se envía al chat una propuesta con código y quien la pidió confirma con 'OK <código>'): " +
			"'create' (name obligatorio; arranca con el diseño de la plantilla de marca o con design_html), 'update' (name, subject, channels email/whatsapp, audience bookings|manual, audience_days, manual_recipients, email_per_minute 1-5, whatsapp_per_minute 1), " +
			"'design_edit' (ops en orden sobre los ids de la última lectura, con version: add_block {block,target?,position?}, insert_html {html,target?,position?}, set_text {id,text}, set_html {id,html}, set_attrs {id,href?,src?,alt?}, set_style {id,scope all|md,styles{fontSize,fontWeight,textAlign,textColor,bgColor,lineHeight,paddingX,paddingY,marginTop,marginBottom,radius,maxWidth,width}}, set_classes {id,classes|add|remove}, set_visibility {id,all|mobile|desktop}, move {id,direction up|down}, duplicate {id}, remove {id}, replace_element {id,html}, replace_html {html}; HTML con clases de Tailwind v3 y el enlace {{unsubscribe_url}} en el pie), " +
			"'design_ai' (instruction para el asistente de diseño con IA; element_id y model opcionales), 'design_undo' (deshace el último cambio de diseño hecho desde un chat), " +
			"'image' (usa en el email una IMAGEN ENVIADA a este chat: la sube al CDN del restaurante en formato apto para email; image_ref = la de la nota de la imagen o vacío para la más reciente; element_id = la imagen <img> que sustituye, o se añade un bloque de imagen después de insert_after; placement=none solo la sube y devuelve la URL al momento; alt opcional), " +
			"'test' (prueba a target: email o teléfono), 'send' (envío real a toda la audiencia; no se puede deshacer), 'resume' (reanuda envíos pausados; ack obligatorio) y 'delete'. " +
			"Propón UN cambio cada vez y espera al OK antes del siguiente. Para campañas usa SIEMPRE esta herramienta, nunca panel_change ni admin_call: solo ella recompila el email que se envía.",
		InputSchema: botSchema(`{"type":"object","properties":{
			"action":{"type":"string","enum":["list","get","create","update","design_edit","design_ai","design_undo","image","test","send","resume","delete"]},
			"campaign_id":{"type":"integer","description":"ID de la campaña (todas menos list y create)"},
			"include":{"type":"array","items":{"type":"string","enum":["layers","html","whatsapp_text","audience","deliveries","ai_models","blocks"]}},
			"name":{"type":"string"},"subject":{"type":"string"},
			"channels":{"type":"array","items":{"type":"string","enum":["email","whatsapp"]}},
			"audience":{"type":"string","enum":["bookings","manual"]},"audience_days":{"type":"integer"},
			"manual_recipients":{"type":"array","items":{"type":"string"}},
			"email_per_minute":{"type":"integer"},"whatsapp_per_minute":{"type":"integer"},
			"design_html":{"type":"string","description":"Solo create: diseño inicial"},
			"ops":{"type":"array","items":{"type":"object"},"description":"Solo design_edit"},
			"version":{"type":"string","description":"version del diseño leída con get"},
			"instruction":{"type":"string","description":"Solo design_ai"},
			"element_id":{"type":"string"},"model":{"type":"string"},
			"image_ref":{"type":"string","description":"Solo image: id de la imagen del chat (vacío = la más reciente)"},
			"insert_after":{"type":"string"},"placement":{"type":"string","enum":["replace","insert","none"]},"alt":{"type":"string"},
			"channel":{"type":"string","enum":["email","whatsapp"]},"target":{"type":"string"},"ack":{"type":"string"}
		},"required":["action"]}`),
	}}
}

// botToolManageCampaign executes manage_campaign.
func (s *Server) botToolManageCampaign(ctx context.Context, restaurantID int, msg botWebhookMessage, input json.RawMessage) (string, error) {
	var a campaignToolArgs
	if err := json.Unmarshal(input, &a); err != nil {
		return botJSON(map[string]any{"error": "argumentos ilegibles"}), nil
	}
	normalizeCampaignToolArgs(&a)
	if a.Action == "" {
		return botJSON(map[string]any{"error": "falta action"}), nil
	}
	actor, origin, ok := s.botPrivilegedControlAuthorize(ctx, restaurantID, msg)
	if !ok {
		botCheckpoint("campaign_tools_v1", "restaurant_id=%d denied=actor action=%s", restaurantID, a.Action)
		return botJSON(map[string]any{"error": "no autorizado para gestionar campañas desde este chat"}), nil
	}
	if !campaignToolWriteActions[a.Action] {
		out, _, err := s.campaignToolRun(ctx, restaurantID, a, "whatsapp")
		if err != nil {
			return botJSON(map[string]any{"error": err.Error()}), nil
		}
		botCheckpoint("campaign_tools_v1", "restaurant_id=%d read action=%s campaign_id=%d", restaurantID, a.Action, a.CampaignID)
		out["tool"] = "manage_campaign"
		return botJSON(out), nil
	}
	if a.Action == "image" {
		// The bytes of a chat image only live in this turn (or the provider):
		// upload now, so the proposal carries the CDN URL and the "OK" never
		// depends on them. The upload alone changes nothing visible.
		a.Attachment, a.ImageURL, a.ImageName = "", "", ""
		c, err := s.campaignToolLoad(ctx, restaurantID, a.CampaignID)
		if err != nil {
			return botJSON(map[string]any{"error": err.Error()}), nil
		}
		placement, err := campaignImagePlacement(a)
		if err != nil {
			return botJSON(map[string]any{"error": err.Error()}), nil
		}
		img, err := s.botChatImageSource(ctx, restaurantID, msg, a.ImageRef)
		if err != nil {
			return botJSON(map[string]any{"error": err.Error()}), nil
		}
		url, err := s.campaignToolUploadImage(ctx, restaurantID, c.ID, img)
		if err != nil {
			return botJSON(map[string]any{"error": err.Error()}), nil
		}
		a.ImageURL, a.ImageName = url, "imagen de WhatsApp"
		botCheckpoint("campaign_tools_v1", "restaurant_id=%d chat_image_uploaded campaign_id=%d placement=%s", restaurantID, c.ID, placement)
		if placement == "none" {
			return botJSON(map[string]any{"ok": true, "tool": "manage_campaign", "url": url, "status": "Imagen subida al CDN del restaurante; el diseño no ha cambiado."}), nil
		}
	}
	_, text, err := s.campaignToolPreview(ctx, restaurantID, a)
	if err != nil {
		return botJSON(map[string]any{"error": err.Error()}), nil
	}
	if (a.Action == "design_edit" || a.Action == "image") && a.Version == "" {
		// The ops address the ids of the design as it is NOW: bind the
		// proposal to this version so an OK given after another change is
		// refused instead of editing the wrong element.
		if c, err := s.campaignToolLoad(ctx, restaurantID, a.CampaignID); err == nil {
			a.Version = campaignDesignVersion(c.BodyHTML)
		}
	}
	code, err := botSpecialNewCode()
	if err != nil {
		return botJSON(map[string]any{"error": "no se pudo generar el código de confirmación"}), nil
	}
	preview := "Propuesta: " + text + ". Nada está aplicado todavía."
	dedup := fmt.Sprintf("campaign:%s:%d", a.Action, a.CampaignID)
	out, err := s.botProposalPersist(ctx, restaurantID, msg, "campaign", 0, a, dedup, actor, origin, "", "", preview, code)
	if err != nil {
		return botJSON(map[string]any{"error": "no se pudo guardar la propuesta"}), nil
	}
	botCheckpoint("campaign_tools_v1", "restaurant_id=%d proposed action=%s campaign_id=%d", restaurantID, a.Action, a.CampaignID)
	return out, nil
}

// botCampaignExecute applies an approved campaign proposal. Dispatched by
// botSpecialAdminExecute for op "campaign": the claim commits ONLY with a
// successful change, so a failed run leaves the code retryable.
func (s *Server) botCampaignExecute(ctx context.Context, tx *sql.Tx, rid int, p botSpecialProposalRow) (string, error) {
	var a campaignToolArgs
	if err := json.Unmarshal([]byte(p.Args), &a); err != nil {
		return "", errors.New("propuesta ilegible")
	}
	_, summary, err := s.campaignToolRun(ctx, rid, a, "whatsapp")
	if err != nil {
		botCheckpoint("campaign_tools_v1", "restaurant_id=%d apply_failed action=%s campaign_id=%d err=%v", rid, a.Action, a.CampaignID, err)
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", errors.New("error interno")
	}
	botCheckpoint("campaign_tools_v1", "restaurant_id=%d applied action=%s campaign_id=%d", rid, a.Action, a.CampaignID)
	return "Aplicado: " + summary, nil
}
