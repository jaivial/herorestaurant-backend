package api

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"preactvillacarmen/internal/lib/specialmenuimage"
)

// Coordination id: wa_bot_panel_media_v1
//
// The MEDIA half of the panel_change direct ops (wa_bot_panel_write_v1): the
// replay of panel_change is pure JSON, so multipart uploads and image jobs can
// never travel as a REST body. Two parameterised operations cover the media
// surface and dispatch to the very same domain functions the panel handlers
// call (minimum common multiple, one operation family per lifecycle):
//   - media_upload: decoded bytes + metadata into a stored media object
//     (booking documents, special section images, wine images, ad images);
//   - media_ai: provider-bound generations and edits (wine, wine cutout,
//     comida, section dish, menu preview, slider, ads enhance/generate),
//     always async exactly like the panel jobs, with the domain state machine
//     of each kind owning the visible feedback.
//
// Only cases whose domain work lives behind reusable functions are covered:
// where a handler mixes multipart parsing with inline persistence logic there
// is nothing to dispatch to, so the case is omitted instead of duplicated.

// botMediaMaxPayloadBytes caps the decoded payload of one media operation.
const botMediaMaxPayloadBytes = 12 << 20

// botMediaOpFunc is one media mutation over the already-decoded bytes; the
// parameter map is the exact `params` payload of the panel_change proposal.
type botMediaOpFunc func(s *Server, ctx context.Context, rid int, p map[string]any, payload []byte) (any, error)

// botMediaUploadOps / botMediaAIOps dispatch `kind` to its domain case, so a
// new case is one entry plus one function (open for extension).
var botMediaUploadOps = map[string]botMediaOpFunc{
	"booking_document": botMediaUploadBookingDocument,
	"special_section":  botMediaUploadSpecialSection,
	"vino":             botMediaUploadVino,
	"ads":              botMediaUploadAd,
}

var botMediaAIOps = map[string]botMediaOpFunc{
	"vino":         botMediaAIVino,
	"vino_cutout":  botMediaAIVinoCutout,
	"comida":       botMediaAIComida,
	"section_dish": botMediaAISectionDish,
	"menu_preview": botMediaAIMenuPreview,
	"slider":       botMediaAISlider,
	"ads_enhance":  botMediaAIAdsEnhance,
	"ads_generate": botMediaAIAdsGenerate,
}

// init registers both media operations on the panel_change direct-op registry
// (the map is a package var, so it is already initialized when init runs).
func init() {
	botPanelDirectOps["media_upload"] = botMediaUploadOp
	botPanelDirectOps["media_ai"] = botMediaAIOp
	botPanelDirectOpDesc["media_upload"] = "subir un archivo (documento o imagen) al panel"
	botPanelDirectOpDesc["media_ai"] = "generar o editar una imagen con IA"
}

// botMediaUploadOp stores uploaded bytes: params {kind, data_base64,
// filename, mime_type, title?, booking_id?|target_id?|menu_id?+section_id?}.
func botMediaUploadOp(s *Server, ctx context.Context, rid int, p map[string]any) (any, error) {
	kind := botPanelString(p, "kind")
	run, ok := botMediaUploadOps[kind]
	if !ok {
		return nil, errors.New("kind de media_upload no soportado (usa booking_document, special_section, vino o ads)")
	}
	payload, err := botMediaPayload(p, true)
	if err != nil {
		return nil, err
	}
	botCheckpoint("wa_bot_panel_media_v1", "restaurant_id=%d op=media_upload kind=%s bytes=%d", rid, kind, len(payload))
	return run(s, ctx, rid, p, payload)
}

// botMediaAIOp runs a generation/edit: params {kind, data_base64 (salvo
// ads_generate), target_id?|menu_id?+section_id?+dish_id?, prompt?, ...}.
func botMediaAIOp(s *Server, ctx context.Context, rid int, p map[string]any) (any, error) {
	kind := botPanelString(p, "kind")
	run, ok := botMediaAIOps[kind]
	if !ok {
		return nil, errors.New("kind de media_ai no soportado (usa vino, vino_cutout, comida, section_dish, menu_preview, slider, ads_enhance o ads_generate)")
	}
	payload, err := botMediaPayload(p, false)
	if err != nil {
		return nil, err
	}
	botCheckpoint("wa_bot_panel_media_v1", "restaurant_id=%d op=media_ai kind=%s bytes=%d", rid, kind, len(payload))
	return run(s, ctx, rid, p, payload)
}

// botMediaPayload decodes data_base64 fail-safe under the size cap; a missing
// payload is only acceptable when the kind needs no input image.
func botMediaPayload(p map[string]any, required bool) ([]byte, error) {
	raw := botPanelString(p, "data_base64")
	if raw == "" {
		if required {
			return nil, errors.New("data_base64 es obligatorio")
		}
		return nil, nil
	}
	if len(raw) > base64.StdEncoding.EncodedLen(botMediaMaxPayloadBytes) {
		return nil, fmt.Errorf("el archivo supera el máximo de %d MB", botMediaMaxPayloadBytes/(1024*1024))
	}
	payload, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, errors.New("data_base64 ilegible")
	}
	if len(payload) == 0 {
		if required {
			return nil, errors.New("el archivo está vacío")
		}
		return nil, nil
	}
	if len(payload) > botMediaMaxPayloadBytes {
		return nil, fmt.Errorf("el archivo supera el máximo de %d MB", botMediaMaxPayloadBytes/(1024*1024))
	}
	return payload, nil
}

// botMediaImagePayload demands an input image and returns its detected type.
func botMediaImagePayload(payload []byte) (string, error) {
	if len(payload) == 0 {
		return "", errors.New("data_base64 (imagen) es obligatorio")
	}
	contentType := strings.ToLower(strings.TrimSpace(http.DetectContentType(payload)))
	switch contentType {
	case "image/jpeg", "image/png", "image/webp", "image/gif":
		return contentType, nil
	}
	return "", errors.New("formato no permitido: usa JPG, PNG, WebP o GIF")
}

// botMediaAsync runs one provider-bound pipeline in the background with its
// own context and timeout: the approval tx must never be held open across an
// AI call and the WhatsApp turn cannot block on one (the panel jobs are async
// too). Failures land on the log checkpoint; each kind's own state machine
// owns the visible feedback.
func (s *Server) botMediaAsync(rid int, label string, timeout time.Duration, run func(ctx context.Context) error) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		if err := run(ctx); err != nil {
			botCheckpoint("wa_bot_panel_media_v1", "restaurant_id=%d %s failed err=%v", rid, label, err)
		}
	}()
}

// botMediaVinoTipo resolves the wine tipo (the object-path slug) and proves
// the legacy num exists in the tenant, exactly like the vino handlers do.
func (s *Server) botMediaVinoTipo(ctx context.Context, rid int, wineNum int) (string, error) {
	var tipo string
	if err := s.db.QueryRowContext(ctx,
		"SELECT COALESCE(tipo,'') FROM group_menu_wines_v2 WHERE "+vinoV2WhereLegacyNum+" LIMIT 1",
		vinoV2LegacyNumArgs(rid, wineNum)...).Scan(&tipo); err != nil {
		return "", errors.New("vino no encontrado")
	}
	return tipo, nil
}

// botMediaSetVinoFotoPath persists the uploaded object as the wine photo.
func (s *Server) botMediaSetVinoFotoPath(ctx context.Context, rid int, wineNum int, objectPath string) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE group_menu_wines_v2 SET foto_path = ? WHERE "+vinoV2WhereLegacyNum,
		append([]any{objectPath}, vinoV2LegacyNumArgs(rid, wineNum)...)...)
	return err
}

// botMediaUploadBookingDocument stores one attachment via saveBookingDocument,
// the exact domain function bookingDocumentWSUpload calls.
func botMediaUploadBookingDocument(s *Server, ctx context.Context, rid int, p map[string]any, payload []byte) (any, error) {
	userID, err := s.botServiceSessionUser(ctx)
	if err != nil {
		return nil, errors.New("no hay usuario de servicio para firmar el documento")
	}
	var bookingID *int64
	if id := botPanelInt64(p, "booking_id"); id > 0 {
		if !s.bookingBelongsToRestaurant(ctx, rid, id) {
			return nil, errors.New("reserva no encontrada")
		}
		bookingID = &id
	}
	doc, err := s.saveBookingDocument(ctx, rid, userID, bookingID, payload,
		botPanelString(p, "filename"), botPanelString(p, "mime_type"), botPanelString(p, "title"))
	if err != nil {
		return nil, errors.New(bookingDocumentErrorMessage(err))
	}
	return map[string]any{"id": doc.ID, "title": doc.Title, "url": doc.URL, "cdn_url": doc.CDNURL}, nil
}

// botMediaUploadSpecialSection queues the special section image job, the exact
// domain task handleBOSpecialSectionImageWSMessage queues.
func botMediaUploadSpecialSection(s *Server, ctx context.Context, rid int, p map[string]any, payload []byte) (any, error) {
	menuID, sectionID := botPanelInt64(p, "menu_id"), botPanelInt64(p, "section_id")
	if menuID <= 0 || sectionID <= 0 {
		return nil, errors.New("menu_id y section_id son obligatorios")
	}
	if err := s.setBOSpecialSectionImageState(ctx, rid, menuID, sectionID, boSpecialSectionImageStateUploading); err != nil {
		return nil, errors.New("no se pudo guardar el estado de la subida")
	}
	s.broadcastBOGroupMenuV2AIEvent(rid, menuID, "special_section_image_started", map[string]any{
		"section_id":  sectionID,
		"image_state": boSpecialSectionImageStateUploading,
	})
	go s.runBOSpecialSectionImageJob(boSpecialSectionImageJob{
		RestaurantID: rid,
		MenuID:       menuID,
		SectionID:    sectionID,
		Filename:     botPanelString(p, "filename"),
		RawImage:     payload,
	})
	return map[string]any{"menu_id": menuID, "section_id": sectionID, "image_state": boSpecialSectionImageStateUploading}, nil
}

// botMediaUploadVino stores the photo through UploadWineImageV2 (timestamped
// object name, same as the AI and cutout paths) and points the wine at it.
func botMediaUploadVino(s *Server, ctx context.Context, rid int, p map[string]any, payload []byte) (any, error) {
	wineNum := int(botPanelInt64(p, "target_id"))
	if wineNum <= 0 {
		return nil, errors.New("target_id (número de vino) es obligatorio")
	}
	contentType, err := botMediaImagePayload(payload)
	if err != nil {
		return nil, err
	}
	tipo, err := s.botMediaVinoTipo(ctx, rid, wineNum)
	if err != nil {
		return nil, err
	}
	objectPath, err := s.UploadWineImageV2(ctx, rid, tipo, wineNum, payload, contentType)
	if err != nil {
		return nil, errors.New("no se pudo subir la imagen del vino")
	}
	if err := s.botMediaSetVinoFotoPath(ctx, rid, wineNum, objectPath); err != nil {
		return nil, errors.New("no se pudo guardar la imagen del vino")
	}
	return map[string]any{"wine_num": wineNum, "foto_url": s.bunnyPullURL(ctx, rid, objectPath)}, nil
}

// botMediaUploadAd stores the image through saveBOAdImage and commits it into
// the ad content, the same domain calls handleBOAdImageUpload chains.
func botMediaUploadAd(s *Server, ctx context.Context, rid int, p map[string]any, payload []byte) (any, error) {
	adID := botPanelInt64(p, "target_id")
	if adID <= 0 {
		return nil, errors.New("target_id (id del anuncio) es obligatorio")
	}
	if _, err := s.ensureBOAd(ctx, rid, adID); err != nil {
		return nil, errors.New("anuncio no encontrado")
	}
	url, err := s.saveBOAdImage(ctx, rid, adID, payload, botPanelString(p, "filename"), botPanelString(p, "mime_type"), "upload")
	if err != nil {
		return nil, errors.New("no se pudo subir la imagen del anuncio")
	}
	if err := s.persistBOAdImageURL(ctx, rid, adID, url); err != nil {
		s.setBOAdImageGenerationStatus(ctx, rid, adID, boAdImageGenerationFailed, false)
		return nil, errors.New("no se pudo guardar la imagen en el anuncio")
	}
	s.setBOAdImageGenerationStatus(ctx, rid, adID, boAdImageGenerationReady, false)
	return map[string]any{"ad_id": adID, "url": url}, nil
}

// botMediaAIProvider resolves the image AI provider or refuses the operation
// before anything is queued.
func botMediaAIProvider(ctx context.Context, s *Server, rid int) (resolvedAIImageProvider, error) {
	ai := s.resolveAIImageProvider(ctx, rid)
	if strings.TrimSpace(ai.APIKey) == "" {
		return resolvedAIImageProvider{}, errors.New("el proveedor de IA de imágenes no está configurado")
	}
	return ai, nil
}

// botMediaAIVino marks the wine and dispatches runBOVinoAIImageJob, the exact
// job handleBOVinoAIImageGenerate dispatches.
func botMediaAIVino(s *Server, ctx context.Context, rid int, p map[string]any, payload []byte) (any, error) {
	wineNum := int(botPanelInt64(p, "target_id"))
	if wineNum <= 0 {
		return nil, errors.New("target_id (número de vino) es obligatorio")
	}
	contentType, err := botMediaImagePayload(payload)
	if err != nil {
		return nil, err
	}
	ai, err := botMediaAIProvider(ctx, s, rid)
	if err != nil {
		return nil, err
	}
	res, err := s.db.ExecContext(ctx,
		"UPDATE group_menu_wines_v2 SET ai_requested_img = 1, ai_generating_img = 1 WHERE "+vinoV2WhereLegacyNum,
		vinoV2LegacyNumArgs(rid, wineNum)...)
	if err != nil {
		return nil, errors.New("no se pudo marcar el vino para la generación")
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, errors.New("vino no encontrado")
	}
	s.broadcastBOVinoAIEvent(rid, "wine_ai_started", map[string]any{
		"wine_num":      wineNum,
		"ai_requested":  true,
		"ai_generating": true,
	})
	go s.runBOVinoAIImageJob(boVinoAIImageJob{
		RestaurantID: rid,
		WineNum:      wineNum,
		RawImage:     payload,
		ContentType:  contentType,
		APIKey:       ai.APIKey,
		EditURL:      aiImageEditURLForModel(ai.BaseURL, ai.I2IModelSlug),
	})
	return map[string]any{"wine_num": wineNum, "status": "en_curso"}, nil
}

// botMediaAIVinoCutout composes the exact cutout pipeline of
// handleBOVinoImageCutout from its domain primitives, in the background.
func botMediaAIVinoCutout(s *Server, ctx context.Context, rid int, p map[string]any, payload []byte) (any, error) {
	wineNum := int(botPanelInt64(p, "target_id"))
	if wineNum <= 0 {
		return nil, errors.New("target_id (número de vino) es obligatorio")
	}
	contentType, err := botMediaImagePayload(payload)
	if err != nil {
		return nil, err
	}
	ai, err := botMediaAIProvider(ctx, s, rid)
	if err != nil {
		return nil, err
	}
	tipo, err := s.botMediaVinoTipo(ctx, rid, wineNum)
	if err != nil {
		return nil, err
	}
	s.botMediaAsync(rid, "op=media_ai kind=vino_cutout", cutoutTimeout, func(ctx context.Context) error {
		cutout, err := s.callWaveSpeedBgRemover(ctx, ai.BaseURL, ai.APIKey, payload, contentType)
		if err != nil {
			return err
		}
		trimmed, err := trimTransparentPNG(cutout)
		if err != nil {
			return err
		}
		webp, err := specialmenuimage.NormalizeToWebP(ctx, trimmed, "cutout.png", "image/png")
		if err != nil {
			return err
		}
		objectPath, err := s.UploadWineImageV2(ctx, rid, tipo, wineNum, webp, "image/webp")
		if err != nil {
			return err
		}
		return s.botMediaSetVinoFotoPath(ctx, rid, wineNum, objectPath)
	})
	return map[string]any{"wine_num": wineNum, "status": "en_curso"}, nil
}

// botMediaAIComida marks the item and dispatches runBOComidaAIImageJob, the
// exact job handleBOComidaImageAI dispatches.
func botMediaAIComida(s *Server, ctx context.Context, rid int, p map[string]any, payload []byte) (any, error) {
	contentType, err := botMediaImagePayload(payload)
	if err != nil {
		return nil, err
	}
	tipo, ok := normalizeComidaTipo(botPanelString(p, "tipo"))
	if !ok || tipo == comidaTipoVinos || tipo == comidaTipoPostres {
		return nil, errors.New("tipo no soportado para IA de imágenes")
	}
	itemNum := int(botPanelInt64(p, "target_id"))
	if itemNum <= 0 {
		return nil, errors.New("target_id (id del elemento) es obligatorio")
	}
	ai, err := botMediaAIProvider(ctx, s, rid)
	if err != nil {
		return nil, err
	}
	res, err := s.db.ExecContext(ctx,
		"UPDATE comida_items SET ai_generating = 1 WHERE id = ? AND restaurant_id = ? AND source_type = ?",
		itemNum, rid, string(tipo))
	if err != nil {
		return nil, errors.New("no se pudo marcar el elemento para la generación")
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, errors.New("elemento no encontrado")
	}
	s.broadcastBOComidaAIEvent(rid, "comida_ai_started", map[string]any{"tipo": string(tipo), "item_id": itemNum})
	go s.runBOComidaAIImageJob(boComidaAIImageJob{
		RestaurantID: rid,
		Tipo:         string(tipo),
		ItemNum:      itemNum,
		RawImage:     payload,
		ContentType:  contentType,
		APIKey:       ai.APIKey,
		EditURL:      aiImageEditURLForModel(ai.BaseURL, ai.I2IModelSlug),
	})
	return map[string]any{"tipo": string(tipo), "item_id": itemNum, "status": "en_curso"}, nil
}

// botMediaAISectionDish marks the dish and dispatches runBOGroupMenuV2AIImageJob,
// the exact job handleBOGroupMenusV2GenerateSectionDishAIImage dispatches.
func botMediaAISectionDish(s *Server, ctx context.Context, rid int, p map[string]any, payload []byte) (any, error) {
	contentType, err := botMediaImagePayload(payload)
	if err != nil {
		return nil, err
	}
	menuID, sectionID, dishID := botPanelInt64(p, "menu_id"), botPanelInt64(p, "section_id"), botPanelInt64(p, "dish_id")
	if menuID <= 0 || sectionID <= 0 || dishID <= 0 {
		return nil, errors.New("menu_id, section_id y dish_id son obligatorios")
	}
	ai, err := botMediaAIProvider(ctx, s, rid)
	if err != nil {
		return nil, err
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE group_menu_section_dishes_v2
		SET ai_requested_img = 1, ai_generating_img = 1
		WHERE id = ? AND section_id = ? AND menu_id = ? AND restaurant_id = ?`,
		dishID, sectionID, menuID, rid)
	if err != nil {
		return nil, errors.New("no se pudo marcar el plato para la generación")
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, errors.New("plato no encontrado")
	}
	s.broadcastBOGroupMenuV2AIEvent(rid, menuID, "ai_image_started", map[string]any{
		"dish_id":       dishID,
		"section_id":    sectionID,
		"ai_requested":  true,
		"ai_generating": true,
	})
	go s.runBOGroupMenuV2AIImageJob(boGroupMenuV2AIImageJob{
		RestaurantID: rid,
		MenuID:       menuID,
		SectionID:    sectionID,
		DishID:       dishID,
		RawImage:     payload,
		ContentType:  contentType,
		APIKey:       ai.APIKey,
		EditURL:      aiImageEditURLForModel(ai.BaseURL, ai.I2IModelSlug),
	})
	return map[string]any{"menu_id": menuID, "section_id": sectionID, "dish_id": dishID, "status": "en_curso"}, nil
}

// botMediaAIMenuPreview marks the menu and dispatches
// runBOGroupMenuV2AIMenuPreviewImageJob, the exact job
// handleBOGroupMenusV2GenerateMenuPreviewAIImage dispatches.
func botMediaAIMenuPreview(s *Server, ctx context.Context, rid int, p map[string]any, payload []byte) (any, error) {
	contentType, err := botMediaImagePayload(payload)
	if err != nil {
		return nil, err
	}
	menuID := botPanelInt64(p, "menu_id")
	if menuID <= 0 {
		return nil, errors.New("menu_id es obligatorio")
	}
	ai, err := botMediaAIProvider(ctx, s, rid)
	if err != nil {
		return nil, err
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE menus
		SET show_menu_preview_image = 1, menu_preview_ai_requested = 1, menu_preview_ai_generating = 1
		WHERE id = ? AND restaurant_id = ?`, menuID, rid)
	if err != nil {
		return nil, errors.New("no se pudo marcar el menú para la generación")
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, errors.New("menú no encontrado")
	}
	s.broadcastBOGroupMenuV2AIEvent(rid, menuID, "preview_image_started", map[string]any{
		"menu_preview_ai_requested":  true,
		"menu_preview_ai_generating": true,
		"ai_requested_img":           true,
		"ai_generating_img":          true,
	})
	go s.runBOGroupMenuV2AIMenuPreviewImageJob(boGroupMenuV2AIMenuPreviewImageJob{
		RestaurantID: rid,
		MenuID:       menuID,
		RawImage:     payload,
		ContentType:  contentType,
		APIKey:       ai.APIKey,
		EditURL:      aiImageEditURLForModel(ai.BaseURL, ai.I2IModelSlug),
	})
	return map[string]any{"menu_id": menuID, "status": "en_curso"}, nil
}

// botMediaAISlider marks the menu and dispatches runBOGroupMenuV2AISliderImageJob,
// the exact job handleBOGroupMenusV2GenerateSliderAIImage dispatches.
func botMediaAISlider(s *Server, ctx context.Context, rid int, p map[string]any, payload []byte) (any, error) {
	contentType, err := botMediaImagePayload(payload)
	if err != nil {
		return nil, err
	}
	menuID := botPanelInt64(p, "menu_id")
	if menuID <= 0 {
		return nil, errors.New("menu_id es obligatorio")
	}
	ai, err := botMediaAIProvider(ctx, s, rid)
	if err != nil {
		return nil, err
	}
	generationID := botPanelString(p, "generation_id")
	if generationID == "" {
		generationID = fmt.Sprintf("slider-ai-%d", time.Now().UTC().UnixMilli())
	}
	res, err := s.db.ExecContext(ctx,
		"UPDATE menus SET slider_ai_generating = slider_ai_generating + 1 WHERE id = ? AND restaurant_id = ?",
		menuID, rid)
	if err != nil {
		return nil, errors.New("no se pudo marcar el menú para la generación")
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, errors.New("menú no encontrado")
	}
	s.broadcastBOGroupMenuV2AIEvent(rid, menuID, "slider_image_started", map[string]any{"generation_id": generationID})
	go s.runBOGroupMenuV2AISliderImageJob(boGroupMenuV2AISliderImageJob{
		RestaurantID: rid,
		MenuID:       menuID,
		GenerationID: generationID,
		RawImage:     payload,
		ContentType:  contentType,
		APIKey:       ai.APIKey,
		EditURL:      aiImageEditURLForModel(ai.BaseURL, ai.I2IModelSlug),
	})
	return map[string]any{"menu_id": menuID, "generation_id": generationID, "status": "en_curso"}, nil
}

// botMediaAdsEnhancePrompt mirrors the enhance prompt of handleBOAdImageEnhance
// (backoffice_ads.go); the prompt lives inline there, so it is repeated here
// verbatim rather than edited into a shared constant.
const botMediaAdsEnhancePrompt = "Enhance this restaurant advertising image into a premium website campaign photo. Preserve the subject and composition, improve lighting, detail and polish, and do not add any text or logos."

// botMediaAIAdsEnhance runs the enhance pipeline of handleBOAdImageEnhance
// (domain calls only) keeping the ad's image_generation_status honest.
func botMediaAIAdsEnhance(s *Server, ctx context.Context, rid int, p map[string]any, payload []byte) (any, error) {
	adID := botPanelInt64(p, "target_id")
	if adID <= 0 {
		return nil, errors.New("target_id (id del anuncio) es obligatorio")
	}
	contentType, err := botMediaImagePayload(payload)
	if err != nil {
		return nil, err
	}
	if _, err := s.botMediaAdImageStart(ctx, rid, adID); err != nil {
		return nil, err
	}
	ai := s.resolveAIImageProvider(ctx, rid)
	filename, declaredType := botPanelString(p, "filename"), botPanelString(p, "mime_type")
	if declaredType == "" {
		declaredType = contentType
	}
	s.botMediaAsync(rid, "op=media_ai kind=ads_enhance", s.openAIRequestTimeout(), func(ctx context.Context) (err error) {
		defer func() {
			if err != nil {
				s.setBOAdImageGenerationStatus(ctx, rid, adID, boAdImageGenerationFailed, false)
			}
		}()
		compressed, err := specialmenuimage.NormalizeToWebPWithLimit(ctx, payload, filename, declaredType, boAdMaxImageBytes)
		if err != nil {
			return err
		}
		output, err := s.callComidaImageEdit(ctx, boAdEnhanceURL(ai.BaseURL), ai.APIKey, botMediaAdsEnhancePrompt, compressed, "image/webp")
		if err != nil {
			return err
		}
		url, err := s.saveBOAdImage(ctx, rid, adID, output, "enhanced", http.DetectContentType(output), "ai")
		if err != nil {
			return err
		}
		if err := s.persistBOAdImageURL(ctx, rid, adID, url); err != nil {
			return err
		}
		s.setBOAdImageGenerationStatus(ctx, rid, adID, boAdImageGenerationReady, false)
		return nil
	})
	return map[string]any{"ad_id": adID, "status": "en_curso"}, nil
}

// botMediaAIAdsGenerate runs the text-to-image pipeline of
// handleBOAdImageGenerate (domain calls only) with the ad's own prompt.
func botMediaAIAdsGenerate(s *Server, ctx context.Context, rid int, p map[string]any, payload []byte) (any, error) {
	adID := botPanelInt64(p, "target_id")
	if adID <= 0 {
		return nil, errors.New("target_id (id del anuncio) es obligatorio")
	}
	ad, err := s.botMediaAdImageStart(ctx, rid, adID)
	if err != nil {
		return nil, err
	}
	// The ad's own text is the only description of what to draw.
	prompt := boAdTextToImagePrompt(ad.Content)
	if strings.TrimSpace(prompt) == "" {
		return nil, errors.New("escribe el texto del anuncio antes de generar la imagen")
	}
	ai := s.resolveAIImageProvider(ctx, rid)
	s.botMediaAsync(rid, "op=media_ai kind=ads_generate", s.openAIRequestTimeout(), func(ctx context.Context) (err error) {
		defer func() {
			if err != nil {
				s.setBOAdImageGenerationStatus(ctx, rid, adID, boAdImageGenerationFailed, false)
			}
		}()
		output, err := s.callBOAdTextToImage(ctx, boAdTextToImageURL(ai.BaseURL), ai.APIKey, prompt)
		if err != nil {
			return err
		}
		url, err := s.saveBOAdImage(ctx, rid, adID, output, "generated", http.DetectContentType(output), "generated")
		if err != nil {
			return err
		}
		if err := s.persistBOAdImageURL(ctx, rid, adID, url); err != nil {
			return err
		}
		s.setBOAdImageGenerationStatus(ctx, rid, adID, boAdImageGenerationReady, false)
		return nil
	})
	return map[string]any{"ad_id": adID, "status": "en_curso"}, nil
}

// botMediaAdImageStart proves the ad exists, the AI and storage are configured
// and flags the generation as pending: the shared opening of the ad AI paths.
func (s *Server) botMediaAdImageStart(ctx context.Context, rid int, adID int64) (boAd, error) {
	ad, err := s.ensureBOAd(ctx, rid, adID)
	if err != nil {
		return boAd{}, errors.New("anuncio no encontrado")
	}
	if strings.TrimSpace(s.resolveAIImageProvider(ctx, rid).APIKey) == "" {
		return boAd{}, errors.New("el proveedor de IA de imágenes no está configurado")
	}
	if !s.bunnyConfigured(ctx, rid) {
		return boAd{}, errors.New("almacenamiento de imágenes no configurado")
	}
	s.setBOAdImageGenerationStatus(ctx, rid, adID, boAdImageGenerationPending, true)
	return ad, nil
}
