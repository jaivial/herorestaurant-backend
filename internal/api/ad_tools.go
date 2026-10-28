package api

import (
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"strings"
	"time"
)

// Coordination id: ads_html_editor_v1 (ad tools)
//
// The web ads editor (/app/anuncios) as tools for Forky (ad_* in
// assistant_registry.go) and the privileged WhatsApp bot (manage_ad). One core
// over the same model as the backoffice editor: the design engine of the
// backoffice server builds, syncs, converts and compiles the pages
// (server/adDesign.ts) and the ad design AI is the editor's assistant
// (campaignAIRun with the "ad" profile). Writes go through updateBOAd, the
// exact path of the editor. Every design change a tool applies first stores
// the design it replaces (ad_design_revisions) so it can be undone.
//
// An ACTIVE ad is live on the website: a design change shows at once, which
// is why publishing (active on/off) and deleting wait for the person's yes.

type adToolArgs struct {
	Action     string   `json:"action"`
	AdID       int64    `json:"ad_id"`
	Include    []string `json:"include,omitempty"`
	Name       *string  `json:"name,omitempty"`
	Active     *bool    `json:"active,omitempty"`
	StartsAt   *string  `json:"starts_at,omitempty"`
	EndsAt     *string  `json:"ends_at,omitempty"`
	Always     bool     `json:"always,omitempty"`
	Kind       string   `json:"kind,omitempty"`
	MaxWidth   int      `json:"max_width,omitempty"`
	Radius     *int     `json:"radius,omitempty"`
	Background string   `json:"background,omitempty"`
	Overlay    string   `json:"overlay,omitempty"`
	Page       string   `json:"page,omitempty"`
	// Dates of one card (the step named by page) of a multiple ad.
	StepStartsAt *string `json:"step_starts_at,omitempty"`
	StepEndsAt   *string `json:"step_ends_at,omitempty"`
	StepAlways   bool    `json:"step_always,omitempty"`
	Instruction  string  `json:"instruction,omitempty"`
	Model        string  `json:"model,omitempty"`
	Attachment   string  `json:"attachment,omitempty"`
	ImageRef     string  `json:"image_ref,omitempty"`
	ImageURL     string  `json:"image_url,omitempty"`
	ImageName    string  `json:"image_name,omitempty"`
	ElementID    string  `json:"element_id,omitempty"`
	Placement    string  `json:"placement,omitempty"`
	Alt          string  `json:"alt,omitempty"`
	Version      string  `json:"version,omitempty"`
}

// adToolWriteActions change something (the rest only read).
var adToolWriteActions = map[string]bool{
	"create": true, "update": true, "convert": true, "publish": true,
	"design_ai": true, "design_undo": true, "image": true, "delete": true,
}

// adHumanConfirmActions reach the public website at once or cannot be undone.
var adHumanConfirmActions = map[string]bool{"publish": true, "delete": true}

const (
	adToolMaxInstruction = 4000
	adRevisionsKept      = 20
)

func normalizeAdToolArgs(a *adToolArgs) {
	a.Action = strings.ToLower(strings.TrimSpace(a.Action))
	a.Kind = strings.ToLower(strings.TrimSpace(a.Kind))
	a.Page = strings.TrimSpace(a.Page)
	a.Instruction = strings.TrimSpace(a.Instruction)
	a.ElementID = strings.TrimSpace(a.ElementID)
	a.Placement = strings.ToLower(strings.TrimSpace(a.Placement))
	switch a.Kind {
	case "individual", "unico", "único":
		a.Kind = "single"
	case "multiple", "múltiple", "multiple_steps":
		a.Kind = "multiple"
	}
}

func adEditorURL(id int64, tab string) string {
	return fmt.Sprintf("/app/anuncios/%d?tab=%s", id, tab)
}

// adDesignFingerprint fingerprints a design: a change made since a read is
// detected before an element id of that read is used.
func adDesignFingerprint(d *boAdDesign) string {
	if d == nil {
		return ""
	}
	raw, _ := json.Marshal(struct {
		Main  string
		Steps []boAdDesignPage
	}{d.Main.HTML, d.Steps})
	sum := sha1.Sum(raw)
	return hex.EncodeToString(sum[:6])
}

func adKindLabel(ad boAd) string {
	if ad.Design == nil {
		return "formato anterior (sin convertir)"
	}
	if ad.Design.Kind == "multiple" {
		return fmt.Sprintf("múltiple (%d propuestas)", len(ad.Design.Steps))
	}
	return "individual"
}

func adScheduleText(ad boAd) string {
	if ad.StartsAt != nil && ad.EndsAt != nil {
		return fmt.Sprintf("del %s al %s", *ad.StartsAt, *ad.EndsAt)
	}
	return "sin fechas (no aparece en la web hasta que tenga fechas)"
}

func adStepScheduleText(st boAdDesignPage) string {
	if st.StartsAt != nil && st.EndsAt != nil {
		return fmt.Sprintf("del %s al %s", *st.StartsAt, *st.EndsAt)
	}
	return "las mismas fechas que el anuncio"
}

func adToolView(ad boAd) map[string]any {
	out := map[string]any{
		"id": ad.ID, "name": ad.Name, "active": ad.Active, "schedule": adScheduleText(ad), "kind": adKindLabel(ad),
		"format": "html", "editor_url": adEditorURL(ad.ID, "diseno"),
	}
	if ad.Design == nil {
		out["format"] = "anterior"
		out["note"] = "Hecho con el editor anterior: conviértelo (action convert) para editarlo con estas herramientas."
		return out
	}
	steps := make([]map[string]any, 0, len(ad.Design.Steps))
	today := adTodayMadrid()
	for _, st := range ad.Design.Steps {
		steps = append(steps, map[string]any{"page": st.ID, "title": st.Title, "schedule": adStepScheduleText(st), "shown_today": adStepVisibleOn(st, today)})
	}
	if len(steps) > 0 {
		out["steps"] = steps
	}
	out["window"] = ad.Design.Container
	return out
}

func (s *Server) adToolLoad(ctx context.Context, rid int, id int64) (boAd, error) {
	if id <= 0 {
		return boAd{}, errors.New("falta ad_id")
	}
	ad, err := s.readBOAd(ctx, rid, id)
	if err != nil {
		return boAd{}, fmt.Errorf("no existe el anuncio #%d en este restaurante", id)
	}
	return ad, nil
}

func (s *Server) adToolList(ctx context.Context, rid int) ([]map[string]any, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM restaurant_ads WHERE restaurant_id = ? ORDER BY active DESC, updated_at DESC, id DESC LIMIT 50`, rid)
	if err != nil {
		return nil, errors.New("no se pudieron leer los anuncios")
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	out := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		if ad, err := s.readBOAd(ctx, rid, id); err == nil {
			out = append(out, adToolView(ad))
		}
	}
	return out, nil
}

// adPageRef resolves the page a call addresses: "" / main / tarjetas = the
// main page (the ad, or the cards of a multiple ad); else a step by id or
// title.
func adPageRef(d *boAdDesign, page string) (int, string, error) {
	p := strings.TrimSpace(page)
	switch strings.ToLower(p) {
	case "", "main", "principal", "tarjetas", "cards":
		return -1, "principal", nil
	}
	for i, st := range d.Steps {
		if st.ID == p || strings.EqualFold(strings.TrimSpace(st.Title), p) {
			return i, "«" + st.Title + "»", nil
		}
	}
	return 0, "", fmt.Errorf("no existe la página %q: usa main o el page/título de uno de sus pasos", p)
}

func adPageHTML(d *boAdDesign, idx int) string {
	if idx < 0 {
		return d.Main.HTML
	}
	return d.Steps[idx].HTML
}

func adSetPageHTML(d *boAdDesign, idx int, html string) {
	if idx < 0 {
		d.Main.HTML = html
		return
	}
	d.Steps[idx].HTML = html
}

// adDesignCtx is the block context of the ad blocks (brand + website).
func (s *Server) adDesignCtx(ctx context.Context, rid int) campaignDesignCtx {
	brand := s.campaignTemplateBrand(ctx, rid)
	own, _ := s.loadRestaurantBranding(ctx, rid)
	return campaignDesignCtx{BrandName: brand.BrandName, LogoURL: brand.LogoURL, WebsiteURL: brand.WebsiteURL,
		Accent: firstNonEmpty(hexColorOrEmpty(own.PrimaryColor), hexColorOrEmpty(own.AccentColor), "#1e4636")}
}

// adEngineDesign runs an ad_* action of the engine that answers a design.
func (s *Server) adEngineDesign(ctx context.Context, rid int, req campaignDesignRequest) (*boAdDesign, error) {
	req.Ctx = s.adDesignCtx(ctx, rid)
	res, err := s.campaignDesignCall(ctx, req)
	if err != nil {
		return nil, err
	}
	var d boAdDesign
	if err := json.Unmarshal(res.Design, &d); err != nil || d.Main.HTML == "" {
		return nil, errors.New("el motor de diseño no devolvió un anuncio válido")
	}
	return &d, nil
}

func (s *Server) adToolRead(ctx context.Context, rid int, a adToolArgs) (map[string]any, error) {
	if a.AdID <= 0 {
		list, err := s.adToolList(ctx, rid)
		if err != nil {
			return nil, err
		}
		return map[string]any{"ok": true, "ads": list, "count": len(list)}, nil
	}
	ad, err := s.adToolLoad(ctx, rid, a.AdID)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"ok": true, "ad": adToolView(ad)}
	if ad.Design == nil {
		return out, nil
	}
	out["version"] = adDesignFingerprint(ad.Design)
	include := map[string]bool{}
	for _, v := range a.Include {
		include[strings.ToLower(strings.TrimSpace(v))] = true
	}
	pages := []map[string]any{}
	for i := -1; i < len(ad.Design.Steps); i++ {
		page := map[string]any{"page": "main", "title": "Página principal"}
		if ad.Design.Kind == "multiple" && i < 0 {
			page["title"] = "Tarjetas (cada tarjeta abre su paso)"
		}
		if i >= 0 {
			page["page"], page["title"] = ad.Design.Steps[i].ID, ad.Design.Steps[i].Title
		}
		pageHTML := adPageHTML(ad.Design, i)
		res, err := s.campaignDesignCall(ctx, campaignDesignRequest{Action: "inspect", HTML: pageHTML, CoordID: fmt.Sprintf("ad-%d", ad.ID)})
		if err == nil {
			page["design"] = res.Design
			if include["layers"] {
				page["layers"] = res.Layers
			}
		}
		if include["html"] {
			page["html"] = pageHTML
		}
		pages = append(pages, page)
	}
	out["pages"] = pages
	return out, nil
}

// adInputOf is the update input that keeps everything of the ad as it is.
func adInputOf(ad boAd) boAdInput {
	return boAdInput{Name: ad.Name, Active: ad.Active, Content: ad.Content, CTAs: ad.CTAs, Layout: ad.Layout, Canvas: ad.Canvas,
		Design: ad.Design, StartsAt: ad.StartsAt, EndsAt: ad.EndsAt}
}

// adToolSave persists through the editor's path (validation + compile).
func (s *Server) adToolSave(ctx context.Context, rid int, id int64, in boAdInput) (boAd, error) {
	if in.Content == nil {
		in.Content = []boAdContentElement{}
	}
	if in.CTAs == nil {
		in.CTAs = []boAdCTA{}
	}
	ad, err := s.updateBOAd(ctx, rid, id, in)
	if err != nil {
		var v *boAdValidationError
		if errors.As(err, &v) {
			return boAd{}, errors.New(v.Error())
		}
		return boAd{}, errors.New("no se pudo guardar el anuncio")
	}
	return ad, nil
}

// adToolPatch applies the settings of update (name, dates, kind, window).
func (s *Server) adToolPatch(ctx context.Context, rid int, in *boAdInput, a adToolArgs) ([]string, error) {
	var changes []string
	if a.Name != nil {
		name := strings.TrimSpace(*a.Name)
		if name == "" {
			return nil, errors.New("el nombre no puede quedar vacío")
		}
		if name != in.Name {
			in.Name = name
			changes = append(changes, fmt.Sprintf("nombre «%s»", name))
		}
	}
	if a.Always {
		in.StartsAt, in.EndsAt = nil, nil
		changes = append(changes, "sin fechas (siempre que esté activo)")
	} else if a.StartsAt != nil || a.EndsAt != nil {
		if a.StartsAt == nil || a.EndsAt == nil {
			return nil, errors.New("starts_at y ends_at van juntas (o always=true para quitar las fechas)")
		}
		start, end := strings.TrimSpace(*a.StartsAt), strings.TrimSpace(*a.EndsAt)
		ts, err1 := time.Parse("2006-01-02", start)
		te, err2 := time.Parse("2006-01-02", end)
		if err1 != nil || err2 != nil || te.Before(ts) {
			return nil, errors.New("fechas no válidas: YYYY-MM-DD y ends_at no anterior a starts_at")
		}
		in.StartsAt, in.EndsAt = &start, &end
		changes = append(changes, fmt.Sprintf("fechas del %s al %s", start, end))
	}
	stepDates := a.StepAlways || a.StepStartsAt != nil || a.StepEndsAt != nil
	if stepDates {
		if in.Design == nil || in.Design.Kind != "multiple" {
			return nil, errors.New("las fechas por tarjeta son de los anuncios múltiples (y en el formato nuevo)")
		}
		idx, label, err := adPageRef(in.Design, a.Page)
		if err != nil || idx < 0 {
			return nil, errors.New("para programar una tarjeta indica en page el page o el título de su paso")
		}
		d := *in.Design
		d.Steps = append([]boAdDesignPage(nil), in.Design.Steps...)
		st := d.Steps[idx]
		if a.StepAlways {
			st.StartsAt, st.EndsAt = nil, nil
			changes = append(changes, fmt.Sprintf("tarjeta %s con las fechas del anuncio", label))
		} else {
			if a.StepStartsAt == nil || a.StepEndsAt == nil {
				return nil, errors.New("step_starts_at y step_ends_at van juntas (o step_always=true para que la tarjeta siga las fechas del anuncio)")
			}
			st.StartsAt, st.EndsAt = a.StepStartsAt, a.StepEndsAt
			if err := normalizeAdStepSchedule(&st); err != nil {
				return nil, err
			}
			changes = append(changes, fmt.Sprintf("tarjeta %s del %s al %s", label, *st.StartsAt, *st.EndsAt))
		}
		d.Steps[idx] = st
		in.Design = &d
	}
	window := a.MaxWidth > 0 || a.Radius != nil || a.Background != "" || a.Overlay != ""
	if (a.Kind != "" || window) && in.Design == nil {
		return nil, errors.New("este anuncio está en el formato anterior: conviértelo primero (action convert)")
	}
	if a.Kind != "" && a.Kind != in.Design.Kind {
		if a.Kind != "single" && a.Kind != "multiple" {
			return nil, errors.New("kind debe ser single (individual) o multiple")
		}
		d, err := s.adEngineDesign(ctx, rid, campaignDesignRequest{Action: "ad_kind", Kind: a.Kind, AdDesign: in.Design})
		if err != nil {
			return nil, err
		}
		d.Container = in.Design.Container
		in.Design = d
		changes = append(changes, map[string]string{"single": "pasa a individual", "multiple": "pasa a múltiple (tarjetas + páginas de detalle)"}[a.Kind])
	}
	if window {
		d := *in.Design
		c := d.Container
		if a.MaxWidth > 0 {
			c.MaxWidth = a.MaxWidth
			changes = append(changes, fmt.Sprintf("ancho máximo %dpx", a.MaxWidth))
		}
		if a.Radius != nil {
			r := *a.Radius
			c.Radius = &r
			changes = append(changes, fmt.Sprintf("esquinas %dpx", r))
		}
		if a.Background != "" {
			c.Background = a.Background
			changes = append(changes, "fondo de la ventana "+a.Background)
		}
		if a.Overlay != "" {
			c.Overlay = a.Overlay
			changes = append(changes, "oscurecido de la web "+a.Overlay)
		}
		d.Container = c
		in.Design = &d
	}
	return changes, nil
}

func (s *Server) adToolCreate(ctx context.Context, rid int, a adToolArgs) (boAd, error) {
	if a.Name == nil || strings.TrimSpace(*a.Name) == "" {
		return boAd{}, errors.New("el nombre del anuncio es obligatorio")
	}
	kind := a.Kind
	if kind == "" {
		kind = "single"
	}
	d, err := s.adEngineDesign(ctx, rid, campaignDesignRequest{Action: "ad_default", Kind: kind})
	if err != nil {
		return boAd{}, err
	}
	in := boAdInput{Name: strings.TrimSpace(*a.Name), Design: d}
	probe := a
	probe.Name, probe.Kind = nil, ""
	if _, err := s.adToolPatch(ctx, rid, &in, probe); err != nil {
		return boAd{}, err
	}
	// A new ad is never live: publishing is its own confirmed step.
	in.Active = false
	return s.adToolSave(ctx, rid, 0, in)
}

// adStoreRevision keeps the design a tool is about to replace.
func (s *Server) adStoreRevision(ctx context.Context, rid int, ad boAd, source, summary string) {
	var raw any
	if ad.Design != nil {
		b, _ := json.Marshal(ad.Design)
		raw = string(b)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO ad_design_revisions (restaurant_id, ad_id, design_json, source, summary) VALUES (?, ?, ?, ?, ?)`,
		rid, ad.ID, raw, truncateRunes(source, 31), truncateRunes(summary, 240)); err != nil {
		slog.Default().Warn("ad.tools.revision_failed", "restaurant_id", rid, "ad_id", ad.ID, "err", err.Error())
		return
	}
	_, _ = s.db.ExecContext(ctx, `DELETE FROM ad_design_revisions WHERE restaurant_id = ? AND ad_id = ? AND id NOT IN (
		SELECT id FROM (SELECT id FROM ad_design_revisions WHERE restaurant_id = ? AND ad_id = ? ORDER BY id DESC LIMIT ?) newest)`,
		rid, ad.ID, rid, ad.ID, adRevisionsKept)
}

// adToolStoreDesign saves a new design keeping the previous one for undo.
func (s *Server) adToolStoreDesign(ctx context.Context, rid int, ad boAd, d *boAdDesign, source, summary string) (boAd, error) {
	in := adInputOf(ad)
	in.Design = d
	return s.adToolStoreInput(ctx, rid, ad, in, source, summary)
}

// adToolStoreInput saves an input whose design changed, keeping the previous
// design for undo.
func (s *Server) adToolStoreInput(ctx context.Context, rid int, ad boAd, in boAdInput, source, summary string) (boAd, error) {
	next, err := s.adToolSave(ctx, rid, ad.ID, in)
	if err != nil {
		return boAd{}, err
	}
	s.adStoreRevision(ctx, rid, ad, source, summary)
	return next, nil
}

func (s *Server) adToolConvert(ctx context.Context, rid int, a adToolArgs, source string) (map[string]any, string, error) {
	ad, err := s.adToolLoad(ctx, rid, a.AdID)
	if err != nil {
		return nil, "", err
	}
	if ad.Design != nil {
		return nil, "", errors.New("este anuncio ya está en el nuevo formato")
	}
	legacy := ad
	d, err := s.adEngineDesign(ctx, rid, campaignDesignRequest{Action: "ad_convert", Ad: &legacy, Website: s.campaignTemplateBrand(ctx, rid).WebsiteURL})
	if err != nil {
		return nil, "", err
	}
	next, err := s.adToolStoreDesign(ctx, rid, ad, d, source, "Conversión al nuevo editor")
	if err != nil {
		return nil, "", err
	}
	return map[string]any{"ok": true, "ad": adToolView(next), "version": adDesignFingerprint(next.Design), "editor_url": adEditorURL(next.ID, "previsualizacion")},
		fmt.Sprintf("Anuncio «%s» convertido al nuevo editor (%s). Revísalo en %s.", next.Name, adKindLabel(next), adEditorURL(next.ID, "previsualizacion")), nil
}

func (s *Server) adToolDesignAI(ctx context.Context, rid int, a adToolArgs, source string) (map[string]any, string, error) {
	ad, err := s.adToolLoad(ctx, rid, a.AdID)
	if err != nil {
		return nil, "", err
	}
	if ad.Design == nil {
		return nil, "", errors.New("este anuncio está en el formato anterior: conviértelo primero (action convert)")
	}
	if a.Instruction == "" {
		return nil, "", errors.New("falta instruction: describe el cambio que quieres en el anuncio")
	}
	idx, label, err := adPageRef(ad.Design, a.Page)
	if err != nil {
		return nil, "", err
	}
	base := adPageHTML(ad.Design, idx)
	coord := fmt.Sprintf("ad-%d", ad.ID)
	selected := ""
	if a.ElementID != "" {
		res, err := s.campaignDesignCall(ctx, campaignDesignRequest{Action: "element", HTML: base, ID: a.ElementID, CoordID: coord})
		if err != nil {
			return nil, "", err
		}
		selected = res.HTML
	}
	instruction := truncateRunes(a.Instruction, adToolMaxInstruction)
	if ad.Design.Kind == "multiple" {
		if idx < 0 {
			instruction = "[Página de tarjetas de un anuncio múltiple] " + instruction
		} else {
			instruction = fmt.Sprintf("[Página de detalle «%s» de un anuncio múltiple] %s", ad.Design.Steps[idx].Title, instruction)
		}
	}
	ai, err := s.campaignAIRun(ctx, rid, campaignAIChatRequest{
		Model: a.Model, HTML: base, SelectedHTML: selected, CoordID: coord, Profile: "ad",
		Messages: []campaignAIChatMessage{{Role: "user", Content: instruction}},
	})
	if err != nil {
		return nil, "", err
	}
	if strings.TrimSpace(ai.HTML) == "" {
		return map[string]any{"ok": true, "changed": false, "reply": ai.Reply, "model": ai.Model},
			fmt.Sprintf("La IA no ha cambiado el anuncio «%s»: pídeselo de otra forma o con más detalle.", ad.Name), nil
	}
	d := *ad.Design
	d.Steps = append([]boAdDesignPage(nil), ad.Design.Steps...)
	adSetPageHTML(&d, idx, ai.HTML)
	next := &d
	if d.Kind == "multiple" && idx < 0 {
		// New cards get their detail page; removed cards drop theirs.
		if next, err = s.adEngineDesign(ctx, rid, campaignDesignRequest{Action: "ad_sync", AdDesign: &d}); err != nil {
			return nil, "", err
		}
		next.Container = d.Container
	}
	saved, err := s.adToolStoreDesign(ctx, rid, ad, next, source, "IA: "+a.Instruction)
	if err != nil {
		return nil, "", err
	}
	slog.Default().Info("ad.tools.design_ai", "ad_id", ad.ID, "restaurant_id", rid, "source", source, "model", ai.Model, "page", a.Page)
	summary := fmt.Sprintf("Página %s del anuncio «%s» actualizada por la IA (%s) según lo pedido: «%s». Revísala en %s.", label, saved.Name, ai.Model, truncateRunes(a.Instruction, 200), adEditorURL(saved.ID, "previsualizacion"))
	if saved.Active {
		summary += " El anuncio está activo: el cambio ya se ve en la web."
	}
	if warning := campaignLanguageWarning(ai.LanguageWarnings); warning != "" {
		summary += " " + warning
	}
	return map[string]any{"ok": true, "changed": true, "reply": ai.Reply, "model": ai.Model, "ad": adToolView(saved),
		"version": adDesignFingerprint(saved.Design), "editor_url": adEditorURL(saved.ID, "previsualizacion")}, summary, nil
}

type adRevision struct {
	ID        int64
	Design    sql.NullString
	Summary   string
	CreatedAt time.Time
}

func (s *Server) adLatestRevision(ctx context.Context, rid int, id int64) (adRevision, error) {
	var rev adRevision
	err := s.db.QueryRowContext(ctx, `SELECT id, design_json, summary, created_at FROM ad_design_revisions WHERE restaurant_id = ? AND ad_id = ? ORDER BY id DESC LIMIT 1`, rid, id).
		Scan(&rev.ID, &rev.Design, &rev.Summary, &rev.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return rev, errors.New("no hay ningún cambio de diseño hecho desde un chat que deshacer")
	}
	return rev, err
}

func (s *Server) adToolDesignUndo(ctx context.Context, rid int, a adToolArgs) (map[string]any, string, error) {
	ad, err := s.adToolLoad(ctx, rid, a.AdID)
	if err != nil {
		return nil, "", err
	}
	rev, err := s.adLatestRevision(ctx, rid, ad.ID)
	if err != nil {
		return nil, "", err
	}
	in := adInputOf(ad)
	if rev.Design.Valid && strings.TrimSpace(rev.Design.String) != "" && rev.Design.String != "null" {
		var d boAdDesign
		if err := json.Unmarshal([]byte(rev.Design.String), &d); err != nil {
			return nil, "", errors.New("la versión guardada no se puede leer")
		}
		in.Design = &d
	} else {
		// The change being undone was the conversion: back to the previous
		// format (the legacy fields were never touched).
		if _, err := s.db.ExecContext(ctx, `UPDATE restaurant_ads SET design_json = NULL WHERE id = ? AND restaurant_id = ?`, ad.ID, rid); err != nil {
			return nil, "", errors.New("no se pudo deshacer")
		}
		in.Design = nil
	}
	next, err := s.adToolSave(ctx, rid, ad.ID, in)
	if err != nil {
		return nil, "", err
	}
	_, _ = s.db.ExecContext(ctx, `DELETE FROM ad_design_revisions WHERE id = ? AND restaurant_id = ?`, rev.ID, rid)
	return map[string]any{"ok": true, "ad": adToolView(next), "version": adDesignFingerprint(next.Design), "undone": rev.Summary},
		fmt.Sprintf("Deshecho en «%s»: %s.", next.Name, truncateRunes(rev.Summary, 160)), nil
}

// adImagePlacement: replace an <img> (element_id, or the first image of the
// page), insert a new image block at the end of the page, or none (upload only).
func adImagePlacement(a adToolArgs) (string, error) {
	switch a.Placement {
	case "", "replace":
		return "replace", nil
	case "insert", "none":
		return a.Placement, nil
	}
	return "", errors.New("placement debe ser replace, insert o none")
}

func (s *Server) adImageOps(ctx context.Context, a adToolArgs, page, placement, url string) ([]map[string]any, error) {
	alt := firstNonEmpty(strings.TrimSpace(a.Alt), "Imagen del anuncio")
	if placement == "insert" {
		block := fmt.Sprintf(`<div data-eb-name="Imagen" class="px-6 pt-5 sm:px-8"><img src="%s" alt="%s" class="block h-auto w-full rounded-xl"></div>`, html.EscapeString(url), html.EscapeString(alt))
		return []map[string]any{{"op": "insert_html", "html": block}}, nil
	}
	id := a.ElementID
	if id == "" {
		id = s.adFirstImageID(ctx, page)
		if id == "" {
			return nil, errors.New("la página no tiene ninguna imagen que sustituir: usa placement=insert")
		}
	}
	attrs := map[string]any{"op": "set_attrs", "id": id, "src": url}
	if strings.TrimSpace(a.Alt) != "" {
		attrs["alt"] = alt
	}
	return []map[string]any{attrs}, nil
}

// adFirstImageID is the engine id (e<n>, document order) of the first <img>
// of a page, read from the engine's own layers.
func (s *Server) adFirstImageID(ctx context.Context, page string) string {
	res, err := s.campaignDesignCall(ctx, campaignDesignRequest{Action: "inspect", HTML: page})
	if err != nil {
		return ""
	}
	var layers []struct {
		ID   string `json:"id"`
		Kind string `json:"kind"`
	}
	_ = json.Unmarshal(res.Layers, &layers)
	for _, l := range layers {
		if l.Kind == "image" {
			return l.ID
		}
	}
	return ""
}

func (s *Server) adToolImage(ctx context.Context, rid int, a adToolArgs, source string) (map[string]any, string, error) {
	ad, err := s.adToolLoad(ctx, rid, a.AdID)
	if err != nil {
		return nil, "", err
	}
	placement, err := adImagePlacement(a)
	if err != nil {
		return nil, "", err
	}
	url := strings.TrimSpace(a.ImageURL)
	if url == "" && a.Attachment != "" {
		img, err := s.campaignAttachmentImage(ctx, rid, a.Attachment)
		if err != nil {
			return nil, "", err
		}
		if url, err = s.saveBOAdImage(ctx, rid, ad.ID, img.Data, img.Name, img.ContentType, "design"); err != nil {
			return nil, "", errors.New("no se pudo subir la imagen al CDN del restaurante")
		}
	}
	if url == "" {
		return nil, "", errors.New("falta la imagen")
	}
	if placement == "none" {
		return map[string]any{"ok": true, "url": url}, "Imagen subida al CDN del restaurante; el anuncio no ha cambiado.", nil
	}
	if ad.Design == nil {
		return nil, "", errors.New("este anuncio está en el formato anterior: conviértelo primero (action convert)")
	}
	if a.Version != "" && a.Version != adDesignFingerprint(ad.Design) {
		return nil, "", errors.New("el anuncio ha cambiado desde tu última lectura: vuelve a leerlo (include layers) antes de colocar la imagen")
	}
	idx, label, err := adPageRef(ad.Design, a.Page)
	if err != nil {
		return nil, "", err
	}
	page := adPageHTML(ad.Design, idx)
	ops, err := s.adImageOps(ctx, a, page, placement, url)
	if err != nil {
		return nil, "", err
	}
	raw, _ := json.Marshal(ops)
	res, err := s.campaignDesignCall(ctx, campaignDesignRequest{Action: "apply", HTML: page, Ops: raw, Ctx: s.adDesignCtx(ctx, rid), CoordID: fmt.Sprintf("ad-%d", ad.ID)})
	if err != nil {
		return nil, "", err
	}
	d := *ad.Design
	d.Steps = append([]boAdDesignPage(nil), ad.Design.Steps...)
	adSetPageHTML(&d, idx, res.HTML)
	what := "sustituye una imagen"
	if placement == "insert" {
		what = "añadida al final"
	}
	saved, err := s.adToolStoreDesign(ctx, rid, ad, &d, source, "Imagen en la página "+label+" ("+what+")")
	if err != nil {
		return nil, "", err
	}
	return map[string]any{"ok": true, "url": url, "ad": adToolView(saved), "version": adDesignFingerprint(saved.Design)},
		fmt.Sprintf("Imagen colocada en la página %s del anuncio «%s» (%s).", label, saved.Name, what), nil
}

func (s *Server) adToolDelete(ctx context.Context, rid int, a adToolArgs) (map[string]any, string, error) {
	ad, err := s.adToolLoad(ctx, rid, a.AdID)
	if err != nil {
		return nil, "", err
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM restaurant_ads WHERE id = ? AND restaurant_id = ?`, ad.ID, rid); err != nil {
		return nil, "", errors.New("no se pudo eliminar el anuncio")
	}
	_, _ = s.db.ExecContext(ctx, `DELETE FROM ad_design_revisions WHERE restaurant_id = ? AND ad_id = ?`, rid, ad.ID)
	return map[string]any{"ok": true, "deleted": ad.ID}, fmt.Sprintf("Anuncio «%s» eliminado.", ad.Name), nil
}

// adToolRun executes one action (reads and confirmed writes).
func (s *Server) adToolRun(ctx context.Context, rid int, a adToolArgs, source string) (map[string]any, string, error) {
	normalizeAdToolArgs(&a)
	switch a.Action {
	case "list", "get":
		out, err := s.adToolRead(ctx, rid, a)
		return out, "", err
	case "create":
		ad, err := s.adToolCreate(ctx, rid, a)
		if err != nil {
			return nil, "", err
		}
		return map[string]any{"ok": true, "ad": adToolView(ad), "version": adDesignFingerprint(ad.Design), "editor_url": adEditorURL(ad.ID, "diseno")},
			fmt.Sprintf("Anuncio «%s» creado (#%d, %s, inactivo). Diséñalo con la IA o en %s.", ad.Name, ad.ID, adKindLabel(ad), adEditorURL(ad.ID, "diseno")), nil
	case "update":
		ad, err := s.adToolLoad(ctx, rid, a.AdID)
		if err != nil {
			return nil, "", err
		}
		in := adInputOf(ad)
		changes, err := s.adToolPatch(ctx, rid, &in, a)
		if err != nil {
			return nil, "", err
		}
		if len(changes) == 0 {
			return nil, "", errors.New("no hay nada que cambiar")
		}
		var next boAd
		if in.Design != ad.Design {
			next, err = s.adToolStoreInput(ctx, rid, ad, in, source, "Ajustes: "+strings.Join(changes, ", "))
		} else {
			next, err = s.adToolSave(ctx, rid, ad.ID, in)
		}
		if err != nil {
			return nil, "", err
		}
		return map[string]any{"ok": true, "ad": adToolView(next), "changed": changes}, fmt.Sprintf("Anuncio «%s» actualizado: %s.", next.Name, strings.Join(changes, ", ")), nil
	case "publish":
		ad, err := s.adToolLoad(ctx, rid, a.AdID)
		if err != nil {
			return nil, "", err
		}
		if a.Active == nil {
			return nil, "", errors.New("falta active (true publica el anuncio en la web, false lo retira)")
		}
		if *a.Active && ad.Design == nil {
			return nil, "", errors.New("conviértelo primero al nuevo formato (action convert) y revísalo antes de publicarlo")
		}
		in := adInputOf(ad)
		in.Active = *a.Active
		next, err := s.adToolSave(ctx, rid, ad.ID, in)
		if err != nil {
			return nil, "", err
		}
		state := "retirado de la web"
		if next.Active {
			state = "publicado en la web (" + adScheduleText(next) + ")"
			if next.StartsAt == nil || next.EndsAt == nil {
				state = "activado, pero sin fechas no aparece en la web: ponle fechas (update con starts_at y ends_at)"
			}
		}
		return map[string]any{"ok": true, "ad": adToolView(next)}, fmt.Sprintf("Anuncio «%s» %s.", next.Name, state), nil
	case "convert":
		return s.adToolConvert(ctx, rid, a, source)
	case "design_ai":
		return s.adToolDesignAI(ctx, rid, a, source)
	case "design_undo":
		return s.adToolDesignUndo(ctx, rid, a)
	case "image":
		return s.adToolImage(ctx, rid, a, source)
	case "delete":
		return s.adToolDelete(ctx, rid, a)
	}
	return nil, "", fmt.Errorf("action desconocida: %q", a.Action)
}

// adToolPreview validates a write and describes it ("Se va a …").
func (s *Server) adToolPreview(ctx context.Context, rid int, a adToolArgs) (map[string]any, string, error) {
	normalizeAdToolArgs(&a)
	if !adToolWriteActions[a.Action] {
		return nil, "", fmt.Errorf("action desconocida: %q", a.Action)
	}
	if a.Action == "create" {
		if a.Name == nil || strings.TrimSpace(*a.Name) == "" {
			return nil, "", errors.New("el nombre del anuncio es obligatorio")
		}
		kind := map[string]string{"": "individual", "single": "individual", "multiple": "múltiple"}[a.Kind]
		if kind == "" {
			return nil, "", errors.New("kind debe ser single (individual) o multiple")
		}
		return map[string]any{"action": "create", "name": strings.TrimSpace(*a.Name), "kind": kind},
			fmt.Sprintf("crear el anuncio «%s» (%s, inactivo) con la plantilla de inicio", strings.TrimSpace(*a.Name), kind), nil
	}
	ad, err := s.adToolLoad(ctx, rid, a.AdID)
	if err != nil {
		return nil, "", err
	}
	label := fmt.Sprintf("«%s» (#%d)", ad.Name, ad.ID)
	base := map[string]any{"action": a.Action, "ad_id": ad.ID, "ad": ad.Name, "active": ad.Active}
	live := ""
	if ad.Active {
		live = " (está activo: se verá al momento en la web)"
	}
	switch a.Action {
	case "update":
		in := adInputOf(ad)
		probe := a
		probe.Kind = "" // the kind switch runs the engine: validated, not executed here
		changes, err := s.adToolPatch(ctx, rid, &in, probe)
		if err != nil {
			return nil, "", err
		}
		if a.Kind != "" {
			if ad.Design == nil {
				return nil, "", errors.New("este anuncio está en el formato anterior: conviértelo primero (action convert)")
			}
			if a.Kind != "single" && a.Kind != "multiple" {
				return nil, "", errors.New("kind debe ser single (individual) o multiple")
			}
			if a.Kind != ad.Design.Kind {
				changes = append(changes, map[string]string{"single": "pasar a individual", "multiple": "pasar a múltiple"}[a.Kind])
			}
		}
		if len(changes) == 0 {
			return nil, "", errors.New("no hay nada que cambiar")
		}
		base["changes"] = changes
		return base, fmt.Sprintf("cambiar en el anuncio %s: %s%s", label, strings.Join(changes, ", "), live), nil
	case "publish":
		if a.Active == nil {
			return nil, "", errors.New("falta active (true publica el anuncio en la web, false lo retira)")
		}
		if *a.Active && ad.Design == nil {
			return nil, "", errors.New("conviértelo primero al nuevo formato (action convert) y revísalo antes de publicarlo")
		}
		if *a.Active {
			if ad.StartsAt == nil || ad.EndsAt == nil {
				return base, fmt.Sprintf("activar el anuncio %s; OJO: no tiene fechas, así que no aparecerá en la web hasta que se le pongan (update con starts_at y ends_at)", label), nil
			}
			return base, fmt.Sprintf("PUBLICAR en la web el anuncio %s (%s; lo verán los clientes que entren en la web)", label, adScheduleText(ad)), nil
		}
		return base, fmt.Sprintf("retirar de la web el anuncio %s", label), nil
	case "convert":
		if ad.Design != nil {
			return nil, "", errors.New("este anuncio ya está en el nuevo formato")
		}
		return base, fmt.Sprintf("pasar el anuncio %s al nuevo editor HTML%s", label, live), nil
	case "design_ai":
		if ad.Design == nil {
			return nil, "", errors.New("este anuncio está en el formato anterior: conviértelo primero (action convert)")
		}
		if a.Instruction == "" {
			return nil, "", errors.New("falta instruction: describe el cambio que quieres en el anuncio")
		}
		_, page, err := adPageRef(ad.Design, a.Page)
		if err != nil {
			return nil, "", err
		}
		base["page"], base["instruction"] = page, truncateRunes(a.Instruction, 300)
		return base, fmt.Sprintf("pedir a la IA de diseño que cambie la página %s del anuncio %s: «%s»%s", page, label, truncateRunes(a.Instruction, 200), live), nil
	case "design_undo":
		rev, err := s.adLatestRevision(ctx, rid, ad.ID)
		if err != nil {
			return nil, "", err
		}
		return base, fmt.Sprintf("deshacer en el anuncio %s el último cambio hecho desde un chat (%s, %s)%s", label, truncateRunes(rev.Summary, 120), rev.CreatedAt.In(boMadridTZ).Format("02/01 15:04"), live), nil
	case "image":
		placement, err := adImagePlacement(a)
		if err != nil {
			return nil, "", err
		}
		if placement == "none" {
			return base, fmt.Sprintf("subir la imagen al CDN del restaurante para el anuncio %s (sin cambiar el diseño)", label), nil
		}
		if ad.Design == nil {
			return nil, "", errors.New("este anuncio está en el formato anterior: conviértelo primero (action convert)")
		}
		idx, page, err := adPageRef(ad.Design, a.Page)
		if err != nil {
			return nil, "", err
		}
		if placement == "replace" && a.ElementID == "" && s.adFirstImageID(ctx, adPageHTML(ad.Design, idx)) == "" {
			return nil, "", errors.New("la página no tiene ninguna imagen que sustituir: usa placement=insert")
		}
		what := "sustituyendo su primera imagen"
		if a.ElementID != "" {
			what = "sustituyendo la imagen " + a.ElementID
		}
		if placement == "insert" {
			what = "añadiéndola al final"
		}
		return base, fmt.Sprintf("poner la imagen en la página %s del anuncio %s, %s%s", page, label, what, live), nil
	case "delete":
		return base, fmt.Sprintf("ELIMINAR el anuncio %s (no se puede deshacer)", label), nil
	}
	return nil, "", fmt.Errorf("action desconocida: %q", a.Action)
}
