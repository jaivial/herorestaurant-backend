package api

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"preactvillacarmen/internal/lib/specialmenuimage"
)

// Coordination id: campaign_tools_v1
//
// Everything /app/campanas/nueva and /app/campanas/{id} can do, as ONE
// parameterised operation set shared by the Forky tools
// (assistant_campaign_tools.go) and the WhatsApp bot tool of the privileged
// contacts and groups (whatsapp_bot_campaign_tools.go):
//
//	reads : list, get (+ include: layers, html, whatsapp_text, audience,
//	        deliveries, ai_models, blocks)
//	writes: create, update (Ajustes + header), design_edit (the builder ops:
//	        palette blocks, layers, inspector, raw HTML), design_ai (the
//	        builder AI assistant), design_undo, test, send, resume, delete
//
// Nothing is re-implemented: settings, sends, tests, audience and deletes are
// the backoffice handlers replayed in-process (assistantCallHandler) with an
// auth bound to the restaurant; the design runs the builder's own TypeScript
// in the backoffice server (campaign_design_engine.go); the AI is the builder
// assistant core (campaignAIRun). Confirmation is the caller's job: Forky
// tokens or the WhatsApp propose + "OK <código>" flow.
//
// Privacy: like campaigns_list / campaigns_read, the tools never hand
// recipient data to a model: audience and deliveries are counts, the manual
// list is a count, never the addresses.

// campaignToolArgs is THE payload of every campaign operation.
type campaignToolArgs struct {
	Action            string          `json:"action"`
	CampaignID        int64           `json:"campaign_id,omitempty"`
	Include           []string        `json:"include,omitempty"`
	Name              *string         `json:"name,omitempty"`
	Subject           *string         `json:"subject,omitempty"`
	Channels          *[]string       `json:"channels,omitempty"`
	Audience          *string         `json:"audience,omitempty"`
	AudienceDays      *int            `json:"audience_days,omitempty"`
	ManualRecipients  *[]string       `json:"manual_recipients,omitempty"`
	EmailPerMinute    *int            `json:"email_per_minute,omitempty"`
	WhatsAppPerMinute *int            `json:"whatsapp_per_minute,omitempty"`
	DesignHTML        string          `json:"design_html,omitempty"`
	Ops               json.RawMessage `json:"ops,omitempty"`
	Version           string          `json:"version,omitempty"`
	Instruction       string          `json:"instruction,omitempty"`
	ElementID         string          `json:"element_id,omitempty"`
	Model             string          `json:"model,omitempty"`
	Channel           string          `json:"channel,omitempty"`
	Target            string          `json:"target,omitempty"`
	Ack               string          `json:"ack,omitempty"`
	// action "image": an image the user attached in the chat, uploaded to the
	// restaurant's BunnyCDN and used in the design.
	Attachment  string  `json:"attachment,omitempty"`   // Forky: storage ref of the attachment (forky/<rid>/...)
	ImageRef    string  `json:"image_ref,omitempty"`    // WhatsApp: message id of the image in this chat
	ImageURL    string  `json:"image_url,omitempty"`    // set by the server once uploaded (never by the model)
	ImageName   string  `json:"image_name,omitempty"`   // set by the server: file name for the previews
	Alt         *string `json:"alt,omitempty"`          // alternative text of the image
	Placement   string  `json:"placement,omitempty"`    // replace | insert | none
	InsertAfter string  `json:"insert_after,omitempty"` // insert: element the new image block goes after
}

// campaignToolWriteActions are the mutations (confirmation required).
var campaignToolWriteActions = map[string]bool{
	"create": true, "update": true, "design_edit": true, "design_ai": true, "design_undo": true, "image": true,
	"test": true, "send": true, "resume": true, "delete": true,
}

var campaignToolIncludes = map[string]bool{
	"layers": true, "html": true, "whatsapp_text": true, "audience": true, "deliveries": true, "ai_models": true, "blocks": true,
}

const (
	campaignToolRevisionsKept  = 30
	campaignToolMaxInstruction = 4000
	campaignToolMaxWhatsApp    = 3000
)

func normalizeCampaignToolArgs(a *campaignToolArgs) {
	a.Action = strings.ToLower(strings.TrimSpace(a.Action))
	a.Version = strings.TrimSpace(a.Version)
	a.Instruction = strings.TrimSpace(a.Instruction)
	a.ElementID = strings.TrimSpace(a.ElementID)
	a.Model = strings.TrimSpace(a.Model)
	a.Channel = strings.ToLower(strings.TrimSpace(a.Channel))
	a.Target = strings.TrimSpace(a.Target)
	a.Ack = strings.TrimSpace(a.Ack)
	a.Attachment = strings.TrimSpace(a.Attachment)
	a.ImageRef = strings.TrimSpace(a.ImageRef)
	a.Placement = strings.ToLower(strings.TrimSpace(a.Placement))
	a.InsertAfter = strings.TrimSpace(a.InsertAfter)
}

// campaignToolCtx binds a backoffice auth to the restaurant so the campaign
// handlers can be replayed in-process. Forky keeps the user's own auth; the
// WhatsApp bot (no session) acts as the synthetic root of its privileged path.
func campaignToolCtx(ctx context.Context, rid int) context.Context {
	a, ok := boAuthFromContext(ctx)
	if !ok {
		a = boAuth{Role: "root", User: boUser{Role: "root"}}
	}
	a.ActiveRestaurantID = rid
	return withBOAuth(ctx, a)
}

// campaignToolHandler replays a campaign handler and decodes its JSON answer;
// a non-2xx or success=false answer becomes an error with its own message.
func (s *Server) campaignToolHandler(ctx context.Context, rid int, h http.HandlerFunc, in assistantHandlerInput) (map[string]any, error) {
	body, code, err := s.assistantCallHandler(campaignToolCtx(ctx, rid), h, in)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	_ = json.Unmarshal(body, &out)
	if ok, _ := out["success"].(bool); code >= 400 || !ok {
		msg, _ := out["message"].(string)
		if msg == "" {
			msg = fmt.Sprintf("la operación no se pudo completar (HTTP %d)", code)
		}
		return out, errors.New(msg)
	}
	return out, nil
}

func campaignIDParam(id int64) map[string]string {
	return map[string]string{"campaignId": strconv.FormatInt(id, 10)}
}

func (s *Server) campaignToolLoad(ctx context.Context, rid int, id int64) (boCampaign, error) {
	if id <= 0 {
		return boCampaign{}, errors.New("falta campaign_id")
	}
	c, err := s.loadBOCampaign(ctx, rid, id)
	if err != nil {
		return boCampaign{}, fmt.Errorf("la campaña #%d no existe en este restaurante", id)
	}
	return c, nil
}

// campaignDesignVersion fingerprints a stored design: an edit built on a read
// of another version is refused instead of landing on the wrong element.
func campaignDesignVersion(html string) string {
	if strings.TrimSpace(html) == "" {
		return "sin-diseno"
	}
	sum := sha256.Sum256([]byte(html))
	return hex.EncodeToString(sum[:])[:12]
}

func campaignStatusLabel(status string) string {
	switch status {
	case "draft":
		return "borrador"
	case "sending":
		return "enviándose"
	case "sent":
		return "enviada"
	}
	return status
}

func campaignEditorURL(id int64, tab string) string {
	return fmt.Sprintf("/app/campanas/%d?tab=%s", id, tab)
}

func campaignAudienceText(c boCampaign) string {
	if c.Audience == "manual" {
		return fmt.Sprintf("lista manual (%d contactos)", len(c.ManualRecipients))
	}
	return fmt.Sprintf("clientes con reserva en los últimos %d días", c.AudienceDays)
}

func campaignTitle(c boCampaign) string {
	return firstNonEmpty(strings.TrimSpace(c.Subject), strings.TrimSpace(c.Name), "Campaña")
}

func truncateRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	return string([]rune(s)[:max]) + "…"
}

// campaignToolView is a campaign as the tools show it: the editor header,
// Ajustes and Flujo, without any recipient data.
func campaignToolView(c boCampaign) map[string]any {
	view := map[string]any{
		"id": c.ID, "name": c.Name, "subject": c.Subject,
		"status": c.Status, "status_label": campaignStatusLabel(c.Status),
		"channels": c.Channels, "audience": c.Audience, "audience_text": campaignAudienceText(c),
		"manual_recipients_count": len(c.ManualRecipients),
		"email_per_minute":        c.EmailPerMinute, "whatsapp_per_minute": c.WhatsAppPerMin,
		"stats": c.Stats, "updated_at": c.UpdatedAt,
		"editor_url": campaignEditorURL(c.ID, "diseno"),
	}
	if c.Audience == "bookings" {
		view["audience_days"] = c.AudienceDays
	}
	if c.SentAt != "" {
		view["sent_at"] = c.SentAt
	}
	return view
}

// campaignToolFlow is the Flujo tab in words: the path a campaign follows.
func campaignToolFlow(c boCampaign, blocks int) []string {
	design := "sin diseño todavía (se crea con la primera edición)"
	if c.BodyHTML != "" {
		design = fmt.Sprintf("diseño HTML con %d bloques", blocks)
	}
	return []string{
		"Audiencia: " + campaignAudienceText(c),
		"Canales: " + strings.Join(c.Channels, " y "),
		"Mensaje: " + design,
		fmt.Sprintf("Ritmo: %d emails/min, %d WhatsApp/min", c.EmailPerMinute, c.WhatsAppPerMin),
		fmt.Sprintf("Envío: %s (%d/%d enviados, %d fallidos, %d pausados)", campaignStatusLabel(c.Status), c.Stats.Sent, c.Stats.Total, c.Stats.Failed, c.Stats.Paused),
	}
}

// ── Reads ──────────────────────────────────────────────────────────────────

func (s *Server) campaignToolList(ctx context.Context, rid int) ([]map[string]any, error) {
	out, err := s.campaignToolHandler(ctx, rid, s.handleBOCampaignsList, assistantHandlerInput{})
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(out["campaigns"])
	var list []boCampaign
	_ = json.Unmarshal(raw, &list)
	rows := make([]map[string]any, 0, len(list))
	for _, c := range list {
		rows = append(rows, map[string]any{
			"id": c.ID, "name": c.Name, "subject": c.Subject, "status": c.Status, "status_label": campaignStatusLabel(c.Status),
			"channels": c.Channels, "stats": c.Stats, "updated_at": c.UpdatedAt,
		})
	}
	return rows, nil
}

func (s *Server) campaignToolRead(ctx context.Context, rid int, a campaignToolArgs) (map[string]any, error) {
	for _, inc := range a.Include {
		if !campaignToolIncludes[inc] {
			return nil, fmt.Errorf("include desconocido: %s (layers, html, whatsapp_text, audience, deliveries, ai_models, blocks)", inc)
		}
	}
	has := func(name string) bool {
		for _, inc := range a.Include {
			if inc == name {
				return true
			}
		}
		return false
	}
	out := map[string]any{}
	if has("ai_models") {
		models, def := s.campaignAIModels(ctx, rid)
		out["ai_models"] = map[string]any{"models": models, "default": def}
		if len(models) == 0 {
			out["ai_models_note"] = "No hay ningún modelo de IA configurado: se configura en /app/config?content=ia."
		}
	}
	if has("blocks") {
		res, err := s.campaignDesignCall(ctx, campaignDesignRequest{Action: "blocks"})
		if err != nil {
			return nil, err
		}
		out["blocks"] = res.Blocks
		out["style_groups"] = res.StyleGroups
	}
	if a.CampaignID <= 0 || a.Action == "list" {
		list, err := s.campaignToolList(ctx, rid)
		if err != nil {
			return nil, err
		}
		out["campaigns"] = list
		return out, nil
	}
	c, err := s.campaignToolLoad(ctx, rid, a.CampaignID)
	if err != nil {
		return nil, err
	}
	out["campaign"] = campaignToolView(c)
	design := map[string]any{"has_design": c.BodyHTML != "", "version": campaignDesignVersion(c.BodyHTML)}
	blocks := 0
	// A campaign without a design yet is edited on the design the editor would
	// create on open (template or legacy message in the frame): the read shows
	// THAT design, so the ids of the first design_edit are readable too (the
	// creation is deterministic and the version is "sin-diseno" on both sides).
	current := c.BodyHTML
	if current == "" && (has("layers") || has("html")) {
		base, err := s.campaignToolBaseDesign(ctx, rid, c)
		if err != nil {
			design["error"] = err.Error()
		}
		current = base
	}
	if has("html") {
		design["html"] = current
	}
	if current != "" {
		res, err := s.campaignDesignCall(ctx, campaignDesignRequest{Action: "inspect", HTML: current, CoordID: c.CoordID})
		if err != nil {
			design["error"] = err.Error()
		} else {
			design["summary"] = res.Design
			var sum struct {
				Blocks []string `json:"blocks"`
			}
			_ = json.Unmarshal(res.Design, &sum)
			blocks = len(sum.Blocks)
			if has("layers") {
				design["layers"] = res.Layers
				design["layers_note"] = "Ids por orden de documento: valen hasta el próximo cambio del diseño; cada edición devuelve las capas renumeradas."
			}
		}
	}
	if c.BodyHTML == "" {
		design["note"] = "Campaña anterior al editor: todavía no tiene diseño guardado. La primera edición crea el diseño mostrado aquí (su mensaje dentro del marco de marca), con estos mismos ids."
	}
	out["design"] = design
	out["flow"] = campaignToolFlow(c, blocks)
	if has("whatsapp_text") {
		out["whatsapp_text"] = truncateRunes(c.BodyMarkdown, campaignToolMaxWhatsApp)
	}
	if has("audience") {
		aud, err := s.campaignToolAudience(ctx, rid, c.ID)
		if err != nil {
			out["audience_error"] = err.Error()
		} else {
			out["audience"] = aud
		}
	}
	if has("deliveries") {
		dl, err := s.campaignToolDeliveries(ctx, rid, c)
		if err != nil {
			out["deliveries_error"] = err.Error()
		} else {
			out["deliveries"] = dl
		}
	}
	return out, nil
}

// campaignToolAudience is "Calcular" of Ajustes: counts only (no sample).
func (s *Server) campaignToolAudience(ctx context.Context, rid int, id int64) (map[string]any, error) {
	out, err := s.campaignToolHandler(ctx, rid, s.handleBOCampaignAudience, assistantHandlerInput{URLParam: campaignIDParam(id)})
	if err != nil {
		return nil, err
	}
	keep := map[string]any{}
	for _, k := range []string{"total", "emails", "whatsapp", "new", "new_emails", "new_whatsapp", "already_in_ledger"} {
		keep[k] = out[k]
	}
	return keep, nil
}

// campaignToolDeliveries is "Registro de envíos" aggregated: counts per
// channel and status plus the most frequent errors, never the targets.
func (s *Server) campaignToolDeliveries(ctx context.Context, rid int, c boCampaign) (map[string]any, error) {
	out, err := s.campaignToolHandler(ctx, rid, s.handleBOCampaignRecipients, assistantHandlerInput{URLParam: campaignIDParam(c.ID)})
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(out["recipients"])
	var rows []struct {
		Channel string `json:"channel"`
		Status  string `json:"status"`
		Error   string `json:"error"`
		SentAt  string `json:"sent_at"`
	}
	_ = json.Unmarshal(raw, &rows)
	byChannel := map[string]map[string]int{}
	errs := map[string]int{}
	last := ""
	for _, r := range rows {
		if byChannel[r.Channel] == nil {
			byChannel[r.Channel] = map[string]int{}
		}
		byChannel[r.Channel][r.Status]++
		if e := strings.TrimSpace(r.Error); e != "" {
			errs[truncateRunes(campaignMaskTargets(e), 120)]++
		}
		if r.SentAt > last {
			last = r.SentAt
		}
	}
	type errCount struct {
		Error string `json:"error"`
		Count int    `json:"count"`
	}
	top := make([]errCount, 0, len(errs))
	for e, n := range errs {
		top = append(top, errCount{e, n})
	}
	sort.Slice(top, func(i, j int) bool { return top[i].Count > top[j].Count })
	if len(top) > 5 {
		top = top[:5]
	}
	res := map[string]any{"stats": c.Stats, "by_channel": byChannel, "top_errors": top, "last_sent_at": last}
	if len(rows) >= 500 {
		res["note"] = "Desglose de los 500 envíos más recientes; stats tiene los totales exactos."
	}
	return res, nil
}

var (
	campaignEmailRe = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)
	campaignPhoneRe = regexp.MustCompile(`\+?\d[\d\s-]{6,}\d`)
)

// campaignMaskTargets hides the addresses and phone numbers a provider error
// can quote ("550 5.1.1 <ana@x.com>: user unknown"): the tools never hand
// recipient data to a model.
func campaignMaskTargets(text string) string {
	return campaignPhoneRe.ReplaceAllString(campaignEmailRe.ReplaceAllString(text, "<email>"), "<teléfono>")
}

// ── Settings ───────────────────────────────────────────────────────────────

func campaignInputOf(c boCampaign) boCampaignInput {
	return boCampaignInput{
		Name: c.Name, Subject: c.Subject, BodyMarkdown: c.BodyMarkdown, BodyHTML: c.BodyHTML, EmailHTML: c.EmailHTML,
		Theme: c.Theme, Channels: append([]string{}, c.Channels...), Audience: c.Audience, AudienceDays: c.AudienceDays,
		ManualRecipients: append([]string{}, c.ManualRecipients...), EmailPerMinute: c.EmailPerMinute, WhatsAppPerMin: c.WhatsAppPerMin,
	}
}

// campaignToolPatch applies the Ajustes/header fields the caller sent and
// lists what changed, in words, for the confirmation preview.
func campaignToolPatch(in *boCampaignInput, a campaignToolArgs) ([]string, error) {
	changes := []string{}
	if a.Name != nil {
		v := strings.TrimSpace(*a.Name)
		if v == "" {
			return nil, errors.New("el nombre no puede quedar vacío")
		}
		if v != in.Name {
			changes = append(changes, fmt.Sprintf("nombre → «%s»", v))
		}
		in.Name = v
	}
	if a.Subject != nil {
		v := strings.TrimSpace(*a.Subject)
		if v != in.Subject {
			changes = append(changes, fmt.Sprintf("asunto → «%s»", v))
		}
		in.Subject = v
	}
	if a.Channels != nil {
		for _, ch := range *a.Channels {
			if v := strings.ToLower(strings.TrimSpace(ch)); v != "email" && v != "whatsapp" {
				return nil, fmt.Errorf("canal desconocido: %s (email o whatsapp)", ch)
			}
		}
		if len(*a.Channels) == 0 {
			return nil, errors.New("indica al menos un canal (email y/o whatsapp)")
		}
		next := normalizeCampaignChannels(*a.Channels)
		if strings.Join(next, ",") != strings.Join(in.Channels, ",") {
			changes = append(changes, "canales → "+strings.Join(next, " y "))
		}
		in.Channels = next
	}
	if a.Audience != nil {
		v := strings.ToLower(strings.TrimSpace(*a.Audience))
		if v != "bookings" && v != "manual" {
			return nil, fmt.Errorf("audiencia desconocida: %s (bookings o manual)", *a.Audience)
		}
		if v != in.Audience {
			changes = append(changes, map[string]string{"bookings": "audiencia → clientes con reserva", "manual": "audiencia → lista manual"}[v])
		}
		in.Audience = v
	}
	if a.AudienceDays != nil {
		if *a.AudienceDays < 1 || *a.AudienceDays > 3650 {
			return nil, errors.New("audience_days debe estar entre 1 y 3650")
		}
		if *a.AudienceDays != in.AudienceDays {
			changes = append(changes, fmt.Sprintf("últimos días → %d", *a.AudienceDays))
		}
		in.AudienceDays = *a.AudienceDays
	}
	if a.ManualRecipients != nil {
		list := []string{}
		for _, v := range *a.ManualRecipients {
			if v = strings.TrimSpace(v); v != "" {
				list = append(list, v)
			}
		}
		if len(list) > campaignMaxRecipients {
			return nil, fmt.Errorf("la lista manual admite como máximo %d contactos", campaignMaxRecipients)
		}
		changes = append(changes, fmt.Sprintf("lista manual → %d contactos", len(list)))
		in.ManualRecipients = list
	}
	if a.EmailPerMinute != nil {
		if *a.EmailPerMinute < campaignMinPerMinute || *a.EmailPerMinute > campaignMaxEmailPerMinute {
			return nil, fmt.Errorf("email_per_minute debe estar entre %d y %d", campaignMinPerMinute, campaignMaxEmailPerMinute)
		}
		if *a.EmailPerMinute != in.EmailPerMinute {
			changes = append(changes, fmt.Sprintf("emails por minuto → %d", *a.EmailPerMinute))
		}
		in.EmailPerMinute = *a.EmailPerMinute
	}
	if a.WhatsAppPerMinute != nil {
		if *a.WhatsAppPerMinute < campaignMinPerMinute || *a.WhatsAppPerMinute > campaignMaxWhatsAppPerMinute {
			return nil, fmt.Errorf("whatsapp_per_minute debe estar entre %d y %d", campaignMinPerMinute, campaignMaxWhatsAppPerMinute)
		}
		if *a.WhatsAppPerMinute != in.WhatsAppPerMin {
			changes = append(changes, fmt.Sprintf("WhatsApp por minuto → %d", *a.WhatsAppPerMinute))
		}
		in.WhatsAppPerMin = *a.WhatsAppPerMinute
	}
	return changes, nil
}

func campaignInputBody(in boCampaignInput) map[string]any {
	raw, _ := json.Marshal(in)
	body := map[string]any{}
	_ = json.Unmarshal(raw, &body)
	return body
}

func campaignFromHandler(out map[string]any) (boCampaign, error) {
	raw, _ := json.Marshal(out["campaign"])
	var c boCampaign
	if err := json.Unmarshal(raw, &c); err != nil || c.ID == 0 {
		return boCampaign{}, errors.New("respuesta de campaña ilegible")
	}
	return c, nil
}

// campaignToolCreate is "Nueva campaña" + Guardar: the editor starts every
// campaign from the brand template (or the design the caller provides).
func (s *Server) campaignToolCreate(ctx context.Context, rid int, a campaignToolArgs) (boCampaign, error) {
	in := boCampaignInput{Channels: []string{"email"}, Audience: "bookings", AudienceDays: 365,
		EmailPerMinute: campaignDefaultEmailPerMinute, WhatsAppPerMin: campaignDefaultWhatsAppPerMinut,
		Theme: normalizeCampaignTheme(campaignTheme{}), ManualRecipients: []string{}}
	if a.Name == nil || strings.TrimSpace(*a.Name) == "" {
		return boCampaign{}, errors.New("el nombre de la campaña es obligatorio")
	}
	if _, err := campaignToolPatch(&in, a); err != nil {
		return boCampaign{}, err
	}
	res, err := s.campaignDesignCall(ctx, campaignDesignRequest{
		Action: "ensure", HTML: a.DesignHTML, Ctx: s.campaignDesignContext(ctx, rid, in.Theme),
		Compile: &struct {
			Title string `json:"title"`
		}{Title: firstNonEmpty(in.Subject, in.Name)},
		CoordID: "camp-new",
	})
	if err != nil {
		return boCampaign{}, err
	}
	in.BodyHTML, in.EmailHTML, in.BodyMarkdown = res.HTML, res.EmailHTML, res.WhatsAppMarkdown
	out, err := s.campaignToolHandler(ctx, rid, s.handleBOCampaignCreate, assistantHandlerInput{Method: http.MethodPost, Body: campaignInputBody(in)})
	if err != nil {
		return boCampaign{}, err
	}
	return campaignFromHandler(out)
}

// campaignToolUpdate is Ajustes + the header (name/subject) + Guardar. The
// subject is the <title> of the compiled email, so a renamed campaign with a
// design is recompiled before it is stored.
func (s *Server) campaignToolUpdate(ctx context.Context, rid int, a campaignToolArgs) (boCampaign, []string, error) {
	c, err := s.campaignToolLoad(ctx, rid, a.CampaignID)
	if err != nil {
		return boCampaign{}, nil, err
	}
	in := campaignInputOf(c)
	changes, err := campaignToolPatch(&in, a)
	if err != nil {
		return boCampaign{}, nil, err
	}
	if len(changes) == 0 {
		return boCampaign{}, nil, errors.New("no hay nada que cambiar: indica algún campo nuevo")
	}
	if in.BodyHTML != "" && firstNonEmpty(in.Subject, in.Name) != campaignTitle(c) {
		res, err := s.campaignDesignCall(ctx, campaignDesignRequest{Action: "compile", HTML: in.BodyHTML, Ctx: s.campaignDesignContext(ctx, rid, c.Theme),
			Compile: &struct {
				Title string `json:"title"`
			}{Title: firstNonEmpty(in.Subject, in.Name)}, CoordID: c.CoordID})
		if err != nil {
			return boCampaign{}, nil, err
		}
		in.EmailHTML = res.EmailHTML
	}
	out, err := s.campaignToolHandler(ctx, rid, s.handleBOCampaignUpdate, assistantHandlerInput{Method: http.MethodPut, URLParam: campaignIDParam(c.ID), Body: campaignInputBody(in)})
	if err != nil {
		return boCampaign{}, nil, err
	}
	next, err := campaignFromHandler(out)
	return next, changes, err
}

// ── Design ─────────────────────────────────────────────────────────────────

// campaignToolBaseDesign is the design an edit starts from: the stored one,
// or the one the editor would create on open (template or legacy message).
func (s *Server) campaignToolBaseDesign(ctx context.Context, rid int, c boCampaign) (string, error) {
	if strings.TrimSpace(c.BodyHTML) != "" {
		return c.BodyHTML, nil
	}
	theme := c.Theme
	res, err := s.campaignDesignCall(ctx, campaignDesignRequest{Action: "ensure", Markdown: c.BodyMarkdown, Theme: &theme, Ctx: s.campaignDesignContext(ctx, rid, c.Theme), CoordID: c.CoordID})
	if err != nil {
		return "", err
	}
	return res.HTML, nil
}

// campaignToolStoreDesign saves a new design exactly like Guardar does (design
// + compiled email + derived WhatsApp text) and, when keep is set, records the
// design it replaced so the change can be undone.
func (s *Server) campaignToolStoreDesign(ctx context.Context, rid int, c boCampaign, res campaignDesignResult, source, summary string, keep bool) (boCampaign, error) {
	in := campaignInputOf(c)
	in.BodyHTML, in.EmailHTML, in.BodyMarkdown = res.HTML, res.EmailHTML, res.WhatsAppMarkdown
	out, err := s.campaignToolHandler(ctx, rid, s.handleBOCampaignUpdate, assistantHandlerInput{Method: http.MethodPut, URLParam: campaignIDParam(c.ID), Body: campaignInputBody(in)})
	if err != nil {
		return boCampaign{}, err
	}
	if keep && s.db != nil {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO campaign_design_revisions (restaurant_id, campaign_id, body_html, body_markdown, source, summary) VALUES (?, ?, ?, ?, ?, ?)`,
			rid, c.ID, nullIfEmpty(c.BodyHTML), c.BodyMarkdown, source, truncateRunes(summary, 240)); err != nil {
			slog.Default().Warn("campaign.tools.revision_failed", "coord_id", c.CoordID, "campaign_id", c.ID, "err", err.Error())
		} else {
			_, _ = s.db.ExecContext(ctx, `DELETE FROM campaign_design_revisions WHERE restaurant_id = ? AND campaign_id = ? AND id NOT IN (
				SELECT id FROM (SELECT id FROM campaign_design_revisions WHERE restaurant_id = ? AND campaign_id = ? ORDER BY id DESC LIMIT ?) newest)`,
				rid, c.ID, rid, c.ID, campaignToolRevisionsKept)
		}
	}
	return campaignFromHandler(out)
}

func campaignDesignWarnings(c boCampaign, design json.RawMessage) []string {
	warnings := []string{}
	var sum struct {
		HasUnsubscribe bool `json:"has_unsubscribe_link"`
	}
	if json.Unmarshal(design, &sum) == nil && !sum.HasUnsubscribe {
		warnings = append(warnings, "El diseño no tiene el enlace de baja ({{unsubscribe_url}}): el envío añadirá un pie de baja automático, pero conviene incluirlo en el pie.")
	}
	if c.Status == "sending" {
		warnings = append(warnings, "La campaña se está enviando: el cambio solo afecta a los mensajes que aún no han salido.")
	}
	return warnings
}

func (s *Server) campaignToolDesignEdit(ctx context.Context, rid int, a campaignToolArgs, source string) (map[string]any, string, error) {
	c, err := s.campaignToolLoad(ctx, rid, a.CampaignID)
	if err != nil {
		return nil, "", err
	}
	if a.Version != "" && a.Version != campaignDesignVersion(c.BodyHTML) {
		return nil, "", errors.New("el diseño cambió desde tu última lectura: vuelve a leer las capas (include layers) y repite el cambio con los ids nuevos")
	}
	ops, err := campaignToolOps(a.Ops)
	if err != nil {
		return nil, "", err
	}
	base, err := s.campaignToolBaseDesign(ctx, rid, c)
	if err != nil {
		return nil, "", err
	}
	dctx := s.campaignDesignContext(ctx, rid, c.Theme)
	before, err := s.campaignDesignCall(ctx, campaignDesignRequest{Action: "inspect", HTML: base, Ctx: dctx, CoordID: c.CoordID})
	if err != nil {
		return nil, "", err
	}
	res, err := s.campaignDesignCall(ctx, campaignDesignRequest{Action: "apply", HTML: base, Ops: a.Ops, Ctx: dctx,
		Compile: &struct {
			Title string `json:"title"`
		}{Title: campaignTitle(c)}, CoordID: c.CoordID})
	if err != nil {
		return nil, "", err
	}
	summary := campaignOpsSummary(ops, campaignLayerIndex(before.Layers))
	next, err := s.campaignToolStoreDesign(ctx, rid, c, res, source, summary, true)
	if err != nil {
		return nil, "", err
	}
	slog.Default().Info("campaign.tools.design_edit", "coord_id", c.CoordID, "campaign_id", c.ID, "restaurant_id", rid, "source", source, "ops", len(ops))
	return map[string]any{
		"ok": true, "campaign_id": c.ID, "version": campaignDesignVersion(next.BodyHTML), "applied": summary,
		"created": res.Created, "design": res.Design, "layers": res.Layers, "warnings": campaignDesignWarnings(next, res.Design),
		"editor_url": campaignEditorURL(c.ID, "diseno"),
	}, fmt.Sprintf("Diseño de «%s» actualizado: %s.", c.Name, summary), nil
}

type campaignToolOp struct {
	Op         string         `json:"op"`
	Type       string         `json:"type"`   // alias models write for op
	Action     string         `json:"action"` // alias models write for op
	ID         string         `json:"id"`
	Block      string         `json:"block"`
	Target     string         `json:"target"`
	Position   string         `json:"position"`
	Text       *string        `json:"text"`
	Href       *string        `json:"href"`
	Src        *string        `json:"src"`
	Alt        *string        `json:"alt"`
	Styles     map[string]any `json:"styles"`
	Scope      string         `json:"scope"`
	Visibility string         `json:"visibility"`
	Channel    string         `json:"channel"` // campaign_block_channel_v1: all|email|whatsapp
	Direction  string         `json:"direction"`
}

// campaignLayerRef is what a summary needs from one layer of the engine.
type campaignLayerRef struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Text  string `json:"text"`
	Alt   string `json:"alt"`
}

func campaignLayerIndex(raw json.RawMessage) map[string]campaignLayerRef {
	var layers []campaignLayerRef
	_ = json.Unmarshal(raw, &layers)
	out := make(map[string]campaignLayerRef, len(layers))
	for _, l := range layers {
		out[l.ID] = l
	}
	return out
}

func campaignToolOps(raw json.RawMessage) ([]campaignToolOp, error) {
	var ops []campaignToolOp
	if len(raw) == 0 || json.Unmarshal(raw, &ops) != nil || len(ops) == 0 {
		return nil, errors.New("ops debe ser una lista con al menos una operación")
	}
	if len(ops) > 40 {
		return nil, errors.New("máximo 40 operaciones por llamada")
	}
	return ops, nil
}

var campaignOpLabels = map[string]string{
	"add_block": "añadir el bloque", "insert_html": "insertar HTML", "set_text": "cambiar el texto de", "set_html": "cambiar el contenido de",
	"set_attrs": "cambiar", "set_style": "cambiar el estilo de", "set_classes": "cambiar las clases de", "set_visibility": "cambiar la visibilidad de",
	"move": "mover", "duplicate": "duplicar", "remove": "eliminar", "replace_element": "sustituir", "replace_html": "sustituir todo el diseño",
}

// campaignBlockLabels are the palette names of emailBuilderBlocks.ts.
var campaignBlockLabels = map[string]string{
	"header": "Cabecera", "hero": "Portada", "heading": "Título", "text": "Texto", "button": "Botón", "image": "Imagen",
	"columns": "Dos columnas", "list": "Lista", "coupon": "Cupón", "quote": "Opinión", "divider": "Separador", "spacer": "Espacio", "footer": "Pie con baja",
}

// campaignElementWords names an element the way a person recognises it: its
// text (or alt) and its layer label, never only an id.
func campaignElementWords(id string, layers map[string]campaignLayerRef) string {
	l, ok := layers[strings.TrimSpace(id)]
	if !ok {
		return strings.TrimSpace(id)
	}
	if t := firstNonEmpty(strings.TrimSpace(l.Text), strings.TrimSpace(l.Alt)); t != "" {
		return fmt.Sprintf("«%s» (%s)", truncateRunes(t, 50), l.Label)
	}
	return fmt.Sprintf("%s (%s)", l.Label, l.ID)
}

// campaignOpsSummary describes the ops in words, for the confirmation preview
// (Forky and the WhatsApp proposal) and the revision history.
func campaignOpsSummary(ops []campaignToolOp, layers map[string]campaignLayerRef) string {
	parts := make([]string, 0, len(ops))
	for _, op := range ops {
		kind := strings.TrimSpace(firstNonEmpty(op.Op, op.Type, op.Action))
		label := firstNonEmpty(campaignOpLabels[kind], kind)
		switch kind {
		case "add_block":
			label += " " + firstNonEmpty(campaignBlockLabels[op.Block], op.Block)
			if op.Target != "" {
				label += " junto a " + campaignElementWords(op.Target, layers)
			}
		case "insert_html", "replace_html":
		case "set_text":
			label += " " + campaignElementWords(op.ID, layers)
			if op.Text != nil {
				label += fmt.Sprintf(" → «%s»", truncateRunes(*op.Text, 60))
			}
		case "set_attrs":
			label += " " + campaignElementWords(op.ID, layers) + ":"
			if op.Href != nil {
				label += " enlace → " + truncateRunes(*op.Href, 80)
			}
			if op.Src != nil {
				label += " imagen → " + truncateRunes(*op.Src, 80)
			}
			if op.Alt != nil {
				label += fmt.Sprintf(" texto alternativo → «%s»", truncateRunes(*op.Alt, 40))
			}
		case "set_style":
			keys := make([]string, 0, len(op.Styles))
			for k := range op.Styles {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			styles := make([]string, 0, len(keys))
			for _, k := range keys {
				styles = append(styles, fmt.Sprintf("%s %v", k, op.Styles[k]))
			}
			label += " " + campaignElementWords(op.ID, layers) + ": " + strings.Join(styles, ", ")
			if op.Scope == "md" {
				label += " (tablet y escritorio)"
			}
		case "set_visibility":
			var parts []string
			if v := map[string]string{"all": "todos los tamaños", "mobile": "solo móvil", "desktop": "solo tablet y escritorio"}[op.Visibility]; v != "" {
				parts = append(parts, v)
			}
			if ch := map[string]string{"all": "email y WhatsApp", "email": "solo email", "whatsapp": "solo WhatsApp"}[op.Channel]; ch != "" {
				parts = append(parts, ch)
			}
			label += " " + campaignElementWords(op.ID, layers) + " → " + strings.Join(parts, ", ")
		case "move":
			label += " " + campaignElementWords(op.ID, layers)
			if op.Direction != "" {
				label += map[string]string{"up": " hacia arriba", "down": " hacia abajo"}[op.Direction]
			} else if op.Target != "" {
				label += " junto a " + campaignElementWords(op.Target, layers)
			}
		default:
			if op.ID != "" {
				label += " " + campaignElementWords(op.ID, layers)
			}
		}
		parts = append(parts, label)
	}
	if len(parts) > 8 {
		parts = append(parts[:8], fmt.Sprintf("y %d cambios más", len(ops)-8))
	}
	return strings.Join(parts, "; ")
}

// campaignToolDryRun validates ops against the real engine on the current
// design (nothing is stored) and describes them; any refusal surfaces here,
// before a confirmation exists.
func (s *Server) campaignToolDryRun(ctx context.Context, rid int, c boCampaign, raw json.RawMessage, ops []campaignToolOp) (string, error) {
	base, err := s.campaignToolBaseDesign(ctx, rid, c)
	if err != nil {
		return "", err
	}
	dctx := s.campaignDesignContext(ctx, rid, c.Theme)
	before, err := s.campaignDesignCall(ctx, campaignDesignRequest{Action: "inspect", HTML: base, Ctx: dctx, CoordID: c.CoordID})
	if err != nil {
		return "", err
	}
	if _, err := s.campaignDesignCall(ctx, campaignDesignRequest{Action: "apply", HTML: base, Ops: raw, Ctx: dctx, CoordID: c.CoordID}); err != nil {
		return "", err
	}
	return campaignOpsSummary(ops, campaignLayerIndex(before.Layers)), nil
}

// campaignToolDesignAI is the builder AI assistant from a chat: the restaurant
// model rewrites the design following the instruction (optionally focused on
// one element) and the result is stored like any other design change.
func (s *Server) campaignToolDesignAI(ctx context.Context, rid int, a campaignToolArgs, source string) (map[string]any, string, error) {
	c, err := s.campaignToolLoad(ctx, rid, a.CampaignID)
	if err != nil {
		return nil, "", err
	}
	if a.Instruction == "" {
		return nil, "", errors.New("falta instruction: describe el cambio que quieres en el diseño")
	}
	base, err := s.campaignToolBaseDesign(ctx, rid, c)
	if err != nil {
		return nil, "", err
	}
	selected := ""
	if a.ElementID != "" {
		res, err := s.campaignDesignCall(ctx, campaignDesignRequest{Action: "element", HTML: base, ID: a.ElementID, CoordID: c.CoordID})
		if err != nil {
			return nil, "", err
		}
		selected = res.HTML
	}
	ai, err := s.campaignAIRun(ctx, rid, campaignAIChatRequest{
		Model: a.Model, HTML: base, SelectedHTML: selected, CoordID: c.CoordID,
		Messages: []campaignAIChatMessage{{Role: "user", Content: truncateRunes(a.Instruction, campaignToolMaxInstruction)}},
	})
	if err != nil {
		return nil, "", err
	}
	if strings.TrimSpace(ai.HTML) == "" {
		// The model's free text never reaches a chat as-is (it can leak its
		// reasoning); Forky reads it in the tool result, WhatsApp gets this.
		return map[string]any{"ok": true, "changed": false, "reply": ai.Reply, "model": ai.Model}, fmt.Sprintf("La IA no ha cambiado el diseño de «%s»: pídeselo de otra forma o con más detalle.", c.Name), nil
	}
	ops, _ := json.Marshal([]map[string]string{{"op": "replace_html", "html": ai.HTML}})
	res, err := s.campaignDesignCall(ctx, campaignDesignRequest{Action: "apply", HTML: base, Ops: ops, Ctx: s.campaignDesignContext(ctx, rid, c.Theme),
		Compile: &struct {
			Title string `json:"title"`
		}{Title: campaignTitle(c)}, CoordID: c.CoordID})
	if err != nil {
		return nil, "", err
	}
	next, err := s.campaignToolStoreDesign(ctx, rid, c, res, source, "IA: "+a.Instruction, true)
	if err != nil {
		return nil, "", err
	}
	slog.Default().Info("campaign.tools.design_ai", "coord_id", c.CoordID, "campaign_id", c.ID, "restaurant_id", rid, "source", source, "model", ai.Model)
	warnings := campaignDesignWarnings(next, res.Design)
	summary := fmt.Sprintf("Diseño de «%s» actualizado por la IA (%s) según lo pedido: «%s». Revísalo en %s.", c.Name, ai.Model, truncateRunes(a.Instruction, 200), campaignEditorURL(c.ID, "diseno"))
	if warning := campaignLanguageWarning(ai.LanguageWarnings); warning != "" {
		warnings = append(warnings, warning)
		summary += " " + warning
	}
	return map[string]any{
		"ok": true, "changed": true, "reply": ai.Reply, "model": ai.Model, "campaign_id": c.ID,
		"version": campaignDesignVersion(next.BodyHTML), "design": res.Design, "warnings": warnings,
		"editor_url": campaignEditorURL(c.ID, "diseno"),
	}, summary, nil
}

type campaignRevision struct {
	ID        int64
	BodyHTML  sql.NullString
	Markdown  sql.NullString
	Source    string
	Summary   string
	CreatedAt string
}

func (s *Server) campaignLatestRevision(ctx context.Context, rid int, id int64) (campaignRevision, error) {
	var r campaignRevision
	err := s.db.QueryRowContext(ctx, `SELECT id, body_html, body_markdown, source, summary, DATE_FORMAT(created_at, '%Y-%m-%d %H:%i')
		FROM campaign_design_revisions WHERE restaurant_id = ? AND campaign_id = ? ORDER BY id DESC LIMIT 1`, rid, id).
		Scan(&r.ID, &r.BodyHTML, &r.Markdown, &r.Source, &r.Summary, &r.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return r, errors.New("no hay cambios de diseño hechos desde un chat que deshacer en esta campaña")
	}
	return r, err
}

// campaignToolDesignUndo restores the design the last tool change replaced.
func (s *Server) campaignToolDesignUndo(ctx context.Context, rid int, a campaignToolArgs) (map[string]any, string, error) {
	c, err := s.campaignToolLoad(ctx, rid, a.CampaignID)
	if err != nil {
		return nil, "", err
	}
	rev, err := s.campaignLatestRevision(ctx, rid, c.ID)
	if err != nil {
		return nil, "", err
	}
	res := campaignDesignResult{WhatsAppMarkdown: rev.Markdown.String}
	if rev.BodyHTML.Valid && strings.TrimSpace(rev.BodyHTML.String) != "" {
		res, err = s.campaignDesignCall(ctx, campaignDesignRequest{Action: "compile", HTML: rev.BodyHTML.String, Ctx: s.campaignDesignContext(ctx, rid, c.Theme),
			Compile: &struct {
				Title string `json:"title"`
			}{Title: campaignTitle(c)}, CoordID: c.CoordID})
		if err != nil {
			return nil, "", err
		}
	} else {
		// The campaign had no design yet: back to its original message.
		res.WhatsAppMarkdown = rev.Markdown.String
	}
	next, err := s.campaignToolStoreDesign(ctx, rid, c, res, "undo", "", false)
	if err != nil {
		return nil, "", err
	}
	_, _ = s.db.ExecContext(ctx, `DELETE FROM campaign_design_revisions WHERE id = ? AND restaurant_id = ?`, rev.ID, rid)
	slog.Default().Info("campaign.tools.design_undo", "coord_id", c.CoordID, "campaign_id", c.ID, "restaurant_id", rid, "revision", rev.ID)
	return map[string]any{
		"ok": true, "campaign_id": c.ID, "undone": rev.Summary, "undone_at": rev.CreatedAt,
		"version": campaignDesignVersion(next.BodyHTML), "has_design": next.BodyHTML != "",
		"editor_url": campaignEditorURL(c.ID, "diseno"),
	}, fmt.Sprintf("Deshecho en «%s»: %s (%s).", c.Name, firstNonEmpty(rev.Summary, "último cambio"), rev.CreatedAt), nil
}

// ── Images from the chat ───────────────────────────────────────────────────

// campaignImagePreviewURL stands in for the CDN URL while a preview validates
// the placement (the upload only happens when the change is applied).
const campaignImagePreviewURL = "https://cdn.invalid/imagen-adjunta.jpg"

// campaignImagePlacement resolves where the image goes: replace an <img>
// (element_id), insert an image block (after insert_after, or at the end of
// the content) or none (only upload and return the URL).
func campaignImagePlacement(a campaignToolArgs) (string, error) {
	switch a.Placement {
	case "":
		if a.ElementID != "" {
			return "replace", nil
		}
		return "insert", nil
	case "replace":
		if a.ElementID == "" {
			return "", errors.New("placement=replace necesita element_id: la imagen (<img>) que se sustituye (capas de get con include layers)")
		}
		return "replace", nil
	case "insert", "none":
		return a.Placement, nil
	}
	return "", fmt.Errorf("placement desconocido: %s (replace, insert o none)", a.Placement)
}

// campaignImageOps are the builder ops that place an uploaded image.
func campaignImageOps(a campaignToolArgs, placement, url string) []map[string]any {
	if placement == "replace" {
		op := map[string]any{"op": "set_attrs", "id": a.ElementID, "src": url}
		if a.Alt != nil {
			op["alt"] = *a.Alt
		}
		return []map[string]any{op}
	}
	op := map[string]any{"op": "add_block", "block": "image", "src": url}
	if a.InsertAfter != "" {
		op["target"], op["position"] = a.InsertAfter, "after"
	}
	if a.Alt != nil {
		op["alt"] = *a.Alt
	}
	return []map[string]any{op}
}

func campaignOpsFromMaps(ops []map[string]any) []campaignToolOp {
	raw, _ := json.Marshal(ops)
	var out []campaignToolOp
	_ = json.Unmarshal(raw, &out)
	return out
}

// campaignAttachmentPath validates a Forky attachment ref: it must live in the
// restaurant's own Forky folder (the same tenant rule the chat applies when it
// reads attachments, validateAssistantAttachmentRefs).
func campaignAttachmentPath(rid int, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", errors.New("falta attachment: la ref de la imagen adjunta (aparece como «ref …» en la línea [Adjunto: …] del mensaje)")
	}
	if i := strings.Index(ref, "/forky/"); strings.HasPrefix(ref, "http") && i >= 0 {
		ref = ref[i+1:] // a pull URL of the same object is accepted as its ref
	}
	clean := path.Clean("/" + ref)
	if !strings.HasPrefix(clean, "/forky/"+strconv.Itoa(rid)+"/") || strings.HasSuffix(clean, "/") {
		return "", errors.New("attachment no es un adjunto de este chat (usa la ref que aparece en la línea [Adjunto: …])")
	}
	return strings.TrimPrefix(clean, "/"), nil
}

// campaignImageSource is an image ready to be stored.
type campaignImageSource struct {
	Name        string
	Data        []byte
	ContentType string
}

// campaignAttachmentImage reads a Forky chat attachment back from Bunny.
func (s *Server) campaignAttachmentImage(ctx context.Context, rid int, ref string) (campaignImageSource, error) {
	p, err := campaignAttachmentPath(rid, ref)
	if err != nil {
		return campaignImageSource{}, err
	}
	data, ct, err := s.bunnyGet(ctx, rid, p)
	if err != nil {
		return campaignImageSource{}, errors.New("no se pudo leer la imagen adjunta: vuelve a adjuntarla")
	}
	return campaignCheckImage(path.Base(p), data, ct)
}

func campaignCheckImage(name string, data []byte, ct string) (campaignImageSource, error) {
	if len(data) == 0 || len(data) > specialmenuimage.MaxInputBytes {
		return campaignImageSource{}, fmt.Errorf("la imagen debe ocupar entre 1 byte y %d MB", specialmenuimage.MaxInputBytes>>20)
	}
	sniffed := http.DetectContentType(data)
	if !strings.HasPrefix(sniffed, "image/") {
		return campaignImageSource{}, errors.New("el adjunto no es una imagen")
	}
	return campaignImageSource{Name: name, Data: data, ContentType: firstNonEmpty(sniffed, ct)}, nil
}

// campaignToolUploadImage stores an image for a campaign in the email format
// and returns its CDN URL.
func (s *Server) campaignToolUploadImage(ctx context.Context, rid int, campaignID int64, img campaignImageSource) (string, error) {
	url, err := s.campaignStoreImage(ctx, rid, campaignID, img.Data, img.Name, img.ContentType, campaignImageEmail)
	if err != nil {
		var refusal campaignImageError
		if errors.As(err, &refusal) {
			return "", errors.New(strings.ToLower(refusal.msg[:1]) + refusal.msg[1:])
		}
		return "", err
	}
	return url, nil
}

// campaignToolImage is action "image": the image is uploaded to the CDN (unless
// the WhatsApp proposal already did it) and placed in the design like any
// other builder change (revision, recompiled email, derived WhatsApp text).
func (s *Server) campaignToolImage(ctx context.Context, rid int, a campaignToolArgs, source string) (map[string]any, string, error) {
	c, err := s.campaignToolLoad(ctx, rid, a.CampaignID)
	if err != nil {
		return nil, "", err
	}
	placement, err := campaignImagePlacement(a)
	if err != nil {
		return nil, "", err
	}
	url := a.ImageURL
	if url == "" {
		img, err := s.campaignAttachmentImage(ctx, rid, a.Attachment)
		if err != nil {
			return nil, "", err
		}
		if url, err = s.campaignToolUploadImage(ctx, rid, c.ID, img); err != nil {
			return nil, "", err
		}
		a.ImageName = img.Name
	}
	slog.Default().Info("campaign.tools.image", "coord_id", c.CoordID, "campaign_id", c.ID, "restaurant_id", rid, "source", source, "placement", placement)
	if placement == "none" {
		return map[string]any{"ok": true, "campaign_id": c.ID, "url": url, "note": "Imagen subida al CDN; el diseño no ha cambiado. Úsala con design_edit (set_attrs src) o pídesela a la IA de diseño con esta URL."},
			fmt.Sprintf("Imagen subida al CDN para «%s»: %s", c.Name, url), nil
	}
	edit := a
	edit.Action = "design_edit"
	edit.Ops, _ = json.Marshal(campaignImageOps(a, placement, url))
	out, summary, err := s.campaignToolDesignEdit(ctx, rid, edit, source)
	if err != nil {
		return nil, "", fmt.Errorf("la imagen se subió al CDN (%s) pero no se pudo poner en el diseño: %w", url, err)
	}
	out["url"] = url
	return out, fmt.Sprintf("Imagen «%s» subida al CDN (%s). %s", firstNonEmpty(a.ImageName, "adjunta"), url, summary), nil
}

// ── Delivery ───────────────────────────────────────────────────────────────

func (s *Server) campaignToolTest(ctx context.Context, rid int, a campaignToolArgs) (map[string]any, string, error) {
	c, err := s.campaignToolLoad(ctx, rid, a.CampaignID)
	if err != nil {
		return nil, "", err
	}
	channel, err := campaignToolTestChannel(a)
	if err != nil {
		return nil, "", err
	}
	if _, err := s.campaignToolHandler(ctx, rid, s.handleBOCampaignTest, assistantHandlerInput{Method: http.MethodPost, URLParam: campaignIDParam(c.ID), Body: map[string]any{"channel": channel, "target": a.Target}}); err != nil {
		return nil, "", fmt.Errorf("la prueba no se pudo enviar: %w", err)
	}
	return map[string]any{"ok": true, "campaign_id": c.ID, "channel": channel, "target": a.Target},
		fmt.Sprintf("Prueba de «%s» enviada por %s a %s.", c.Name, channel, a.Target), nil
}

func campaignToolTestChannel(a campaignToolArgs) (string, error) {
	if a.Target == "" {
		return "", errors.New("falta target: el email o el teléfono que recibe la prueba")
	}
	channel := a.Channel
	if channel == "" {
		channel = "email"
		if !strings.Contains(a.Target, "@") {
			channel = "whatsapp"
		}
	}
	if channel != "email" && channel != "whatsapp" {
		return "", errors.New("channel debe ser email o whatsapp")
	}
	if channel == "email" && !strings.Contains(a.Target, "@") {
		return "", errors.New("para una prueba por email, target debe ser un email")
	}
	return channel, nil
}

func (s *Server) campaignToolSend(ctx context.Context, rid int, a campaignToolArgs) (map[string]any, string, error) {
	c, err := s.campaignToolLoad(ctx, rid, a.CampaignID)
	if err != nil {
		return nil, "", err
	}
	out, err := s.campaignToolHandler(ctx, rid, s.handleBOCampaignSend, assistantHandlerInput{Method: http.MethodPost, URLParam: campaignIDParam(c.ID), Body: map[string]any{}})
	if err != nil {
		return nil, "", fmt.Errorf("no se pudo iniciar el envío: %w", err)
	}
	slog.Default().Info("campaign.tools.send", "coord_id", c.CoordID, "campaign_id", c.ID, "restaurant_id", rid, "queued", out["queued"], "queued_new", out["queued_new"])
	return map[string]any{"ok": true, "campaign_id": c.ID, "queued": out["queued"], "queued_new": out["queued_new"], "already": out["already"], "insert_errors": out["insert_errors"]},
		fmt.Sprintf("Envío de «%s» iniciado: %v destinatarios en cola (%v nuevos). Sigue el progreso en %s.", c.Name, out["queued"], out["queued_new"], campaignEditorURL(c.ID, "ajustes")), nil
}

func (s *Server) campaignToolResume(ctx context.Context, rid int, a campaignToolArgs) (map[string]any, string, error) {
	c, err := s.campaignToolLoad(ctx, rid, a.CampaignID)
	if err != nil {
		return nil, "", err
	}
	if a.Ack == "" {
		return nil, "", errors.New("falta ack: el motivo resuelto que autoriza reanudar (queda registrado)")
	}
	if a.Channel != "" && a.Channel != "email" && a.Channel != "whatsapp" {
		return nil, "", errors.New("channel debe ser email, whatsapp o vacío (ambos)")
	}
	out, err := s.campaignToolHandler(ctx, rid, s.handleBOCampaignResume, assistantHandlerInput{Method: http.MethodPost, URLParam: campaignIDParam(c.ID), Body: map[string]any{"channel": a.Channel, "ack": a.Ack}})
	if err != nil {
		return nil, "", err
	}
	return map[string]any{"ok": true, "campaign_id": c.ID, "requeued": out["requeued"], "skipped": out["skipped"], "running": out["running"]},
		fmt.Sprintf("Reanudados los envíos pausados de «%s»: %v en cola, %v descartados por baja.", c.Name, out["requeued"], out["skipped"]), nil
}

func (s *Server) campaignToolDelete(ctx context.Context, rid int, a campaignToolArgs) (map[string]any, string, error) {
	c, err := s.campaignToolLoad(ctx, rid, a.CampaignID)
	if err != nil {
		return nil, "", err
	}
	if c.Status == "sending" && campaignRunners.running(c.ID) {
		return nil, "", errors.New("la campaña se está enviando ahora mismo: no se puede eliminar hasta que termine")
	}
	if _, err := s.campaignToolHandler(ctx, rid, s.handleBOCampaignDelete, assistantHandlerInput{Method: http.MethodDelete, URLParam: campaignIDParam(c.ID)}); err != nil {
		return nil, "", err
	}
	_, _ = s.db.ExecContext(ctx, `DELETE FROM campaign_design_revisions WHERE restaurant_id = ? AND campaign_id = ?`, rid, c.ID)
	slog.Default().Info("campaign.tools.delete", "coord_id", c.CoordID, "campaign_id", c.ID, "restaurant_id", rid)
	return map[string]any{"ok": true, "deleted": c.ID}, fmt.Sprintf("Campaña «%s» (#%d) eliminada.", c.Name, c.ID), nil
}

// ── Dispatch ───────────────────────────────────────────────────────────────

// campaignToolRun executes one action. The second value is the outcome in
// words: what the WhatsApp chat receives after the approval.
func (s *Server) campaignToolRun(ctx context.Context, rid int, a campaignToolArgs, source string) (map[string]any, string, error) {
	normalizeCampaignToolArgs(&a)
	switch a.Action {
	case "list", "get":
		out, err := s.campaignToolRead(ctx, rid, a)
		return out, "", err
	case "create":
		c, err := s.campaignToolCreate(ctx, rid, a)
		if err != nil {
			return nil, "", err
		}
		slog.Default().Info("campaign.tools.create", "coord_id", c.CoordID, "campaign_id", c.ID, "restaurant_id", rid, "source", source)
		return map[string]any{"ok": true, "campaign": campaignToolView(c), "version": campaignDesignVersion(c.BodyHTML)},
			fmt.Sprintf("Campaña «%s» creada (#%d) con el diseño de la plantilla. Ábrela en %s.", c.Name, c.ID, campaignEditorURL(c.ID, "diseno")), nil
	case "update":
		c, changes, err := s.campaignToolUpdate(ctx, rid, a)
		if err != nil {
			return nil, "", err
		}
		slog.Default().Info("campaign.tools.update", "coord_id", c.CoordID, "campaign_id", c.ID, "restaurant_id", rid, "source", source, "changes", len(changes))
		return map[string]any{"ok": true, "campaign": campaignToolView(c), "changed": changes},
			fmt.Sprintf("Campaña «%s» actualizada: %s.", c.Name, strings.Join(changes, ", ")), nil
	case "design_edit":
		return s.campaignToolDesignEdit(ctx, rid, a, source)
	case "design_ai":
		return s.campaignToolDesignAI(ctx, rid, a, source)
	case "design_undo":
		return s.campaignToolDesignUndo(ctx, rid, a)
	case "image":
		return s.campaignToolImage(ctx, rid, a, source)
	case "test":
		return s.campaignToolTest(ctx, rid, a)
	case "send":
		return s.campaignToolSend(ctx, rid, a)
	case "resume":
		return s.campaignToolResume(ctx, rid, a)
	case "delete":
		return s.campaignToolDelete(ctx, rid, a)
	}
	return nil, "", fmt.Errorf("action desconocida: %q", a.Action)
}

// campaignToolPreview validates a write and describes it, in words and as
// data, BEFORE anything changes: the Forky confirmation and the WhatsApp
// proposal both show it. It never mutates.
func (s *Server) campaignToolPreview(ctx context.Context, rid int, a campaignToolArgs) (map[string]any, string, error) {
	normalizeCampaignToolArgs(&a)
	if a.Action == "create" {
		in := boCampaignInput{Channels: []string{"email"}, Audience: "bookings", AudienceDays: 365, EmailPerMinute: campaignDefaultEmailPerMinute, WhatsAppPerMin: campaignDefaultWhatsAppPerMinut}
		if a.Name == nil || strings.TrimSpace(*a.Name) == "" {
			return nil, "", errors.New("el nombre de la campaña es obligatorio")
		}
		if _, err := campaignToolPatch(&in, a); err != nil {
			return nil, "", err
		}
		design := "el diseño de la plantilla de marca"
		if strings.TrimSpace(a.DesignHTML) != "" {
			design = "el diseño HTML indicado"
		}
		return map[string]any{"action": "create", "name": in.Name, "subject": in.Subject, "channels": in.Channels, "audience": in.Audience},
			fmt.Sprintf("crear la campaña «%s» (canales: %s; audiencia: %s) con %s", in.Name, strings.Join(in.Channels, " y "), campaignAudienceText(boCampaign{Audience: in.Audience, AudienceDays: in.AudienceDays, ManualRecipients: in.ManualRecipients}), design), nil
	}
	if !campaignToolWriteActions[a.Action] {
		return nil, "", fmt.Errorf("action desconocida: %q", a.Action)
	}
	c, err := s.campaignToolLoad(ctx, rid, a.CampaignID)
	if err != nil {
		return nil, "", err
	}
	label := fmt.Sprintf("«%s» (#%d)", c.Name, c.ID)
	base := map[string]any{"action": a.Action, "campaign_id": c.ID, "campaign": c.Name, "status": c.Status}
	switch a.Action {
	case "update":
		in := campaignInputOf(c)
		changes, err := campaignToolPatch(&in, a)
		if err != nil {
			return nil, "", err
		}
		if len(changes) == 0 {
			return nil, "", errors.New("no hay nada que cambiar: indica algún campo nuevo")
		}
		base["changes"] = changes
		return base, fmt.Sprintf("cambiar en la campaña %s: %s", label, strings.Join(changes, ", ")), nil
	case "design_edit":
		if a.Version != "" && a.Version != campaignDesignVersion(c.BodyHTML) {
			return nil, "", errors.New("el diseño cambió desde tu última lectura: vuelve a leer las capas (include layers) y repite el cambio con los ids nuevos")
		}
		ops, err := campaignToolOps(a.Ops)
		if err != nil {
			return nil, "", err
		}
		summary, err := s.campaignToolDryRun(ctx, rid, c, a.Ops, ops)
		if err != nil {
			return nil, "", err
		}
		base["changes"] = summary
		return base, fmt.Sprintf("editar el diseño de la campaña %s: %s", label, summary), nil
	case "design_ai":
		if a.Instruction == "" {
			return nil, "", errors.New("falta instruction: describe el cambio que quieres en el diseño")
		}
		models, def := s.campaignAIModels(ctx, rid)
		if len(models) == 0 {
			return nil, "", errors.New("no hay ningún modelo de IA configurado: se configura en /app/config?content=ia")
		}
		model := def
		for _, m := range models {
			if m.ID == a.Model {
				model = m.ID
			}
		}
		base["instruction"], base["model"] = a.Instruction, model
		return base, fmt.Sprintf("pedir a la IA (%s) que cambie el diseño de la campaña %s: «%s»", model, label, truncateRunes(a.Instruction, 300)), nil
	case "image":
		placement, err := campaignImagePlacement(a)
		if err != nil {
			return nil, "", err
		}
		if a.ImageURL == "" {
			if _, err := campaignAttachmentPath(rid, a.Attachment); err != nil {
				return nil, "", err
			}
		}
		name := firstNonEmpty(a.ImageName, path.Base(a.Attachment), "la imagen adjunta")
		what := fmt.Sprintf("subir «%s» al CDN del restaurante (formato apto para email)", name)
		if a.ImageURL != "" {
			what = fmt.Sprintf("usar la imagen «%s» ya subida al CDN (%s)", name, a.ImageURL)
		}
		if placement == "none" {
			base["placement"] = placement
			return base, what + fmt.Sprintf(" para la campaña %s, sin cambiar el diseño", label), nil
		}
		ops := campaignImageOps(a, placement, firstNonEmpty(a.ImageURL, campaignImagePreviewURL))
		raw, _ := json.Marshal(ops)
		summary, err := s.campaignToolDryRun(ctx, rid, c, raw, campaignOpsFromMaps(ops))
		if err != nil {
			return nil, "", err
		}
		base["placement"], base["changes"] = placement, summary
		return base, fmt.Sprintf("%s y, en la campaña %s, %s", what, label, strings.ReplaceAll(summary, campaignImagePreviewURL, "la imagen subida")), nil
	case "design_undo":
		rev, err := s.campaignLatestRevision(ctx, rid, c.ID)
		if err != nil {
			return nil, "", err
		}
		base["undo"], base["undo_at"] = rev.Summary, rev.CreatedAt
		return base, fmt.Sprintf("deshacer en la campaña %s el último cambio de diseño hecho desde un chat (%s, %s)", label, firstNonEmpty(rev.Summary, "sin descripción"), rev.CreatedAt), nil
	case "test":
		channel, err := campaignToolTestChannel(a)
		if err != nil {
			return nil, "", err
		}
		base["channel"], base["target"] = channel, a.Target
		return base, fmt.Sprintf("enviar una PRUEBA de la campaña %s por %s a %s", label, channel, a.Target), nil
	case "send":
		if c.Status == "sending" && campaignRunners.running(c.ID) {
			return nil, "", errors.New("la campaña ya se está enviando")
		}
		aud, err := s.campaignToolAudience(ctx, rid, c.ID)
		if err != nil {
			return nil, "", err
		}
		base["audience"] = aud
		base["channels"] = c.Channels
		return base, fmt.Sprintf("ENVIAR la campaña %s por %s a %v destinatarios (%v nuevos; %v ya la recibieron). El envío no se puede deshacer",
			label, strings.Join(c.Channels, " y "), aud["total"], aud["new"], aud["already_in_ledger"]), nil
	case "resume":
		if a.Ack == "" {
			return nil, "", errors.New("falta ack: el motivo resuelto que autoriza reanudar (queda registrado)")
		}
		base["channel"], base["ack"] = firstNonEmpty(a.Channel, "ambos"), a.Ack
		return base, fmt.Sprintf("reanudar los envíos pausados de la campaña %s (canal: %s). Motivo: %s", label, firstNonEmpty(a.Channel, "ambos"), a.Ack), nil
	case "delete":
		base["stats"] = c.Stats
		return base, fmt.Sprintf("ELIMINAR la campaña %s (%s, %d/%d enviados) y su registro de envíos. No se puede deshacer", label, campaignStatusLabel(c.Status), c.Stats.Sent, c.Stats.Total), nil
	}
	return nil, "", fmt.Errorf("action desconocida: %q", a.Action)
}
