package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"time"
)

// Coordination id: wa_bot_menus_full_crud_v1
//
// The menu image uploads whose handlers mix multipart parsing with their
// persistence (carta item photo, v2 section dish photo, menu preview image and
// slider image) had no media_upload kind: the bot could create every dish but
// never attach the photo the staff sent. They are replayed as the panel would
// send them — a multipart POST through the real router with the service
// session — so ownership checks, normalisation and storage stay in the one
// handler that owns them.

func init() {
	botMediaUploadOps["comida"] = botMediaUploadComida
	botMediaUploadOps["section_dish"] = botMediaUploadSectionDish
	botMediaUploadOps["menu_preview"] = botMediaUploadMenuPreview
	botMediaUploadOps["slider"] = botMediaUploadSlider
}

func botMediaUploadComida(s *Server, ctx context.Context, rid int, p map[string]any, payload []byte) (any, error) {
	tipo, ok := normalizeComidaTipo(botPanelString(p, "tipo"))
	id := botPanelInt64(p, "target_id")
	if !ok || tipo == comidaTipoVinos || tipo == comidaTipoPostres || id <= 0 {
		return nil, errors.New("tipo (platos|bebidas|cafes) y target_id (id del elemento) son obligatorios; para vinos usa kind=vino")
	}
	return s.botMediaMultipartReplay(ctx, rid, fmt.Sprintf("/comida/%s/%d/image", tipo, id), p, payload)
}

func botMediaUploadSectionDish(s *Server, ctx context.Context, rid int, p map[string]any, payload []byte) (any, error) {
	menuID, sectionID, dishID := botPanelInt64(p, "menu_id"), botPanelInt64(p, "section_id"), botPanelInt64(p, "dish_id")
	if menuID <= 0 || sectionID <= 0 || dishID <= 0 {
		return nil, errors.New("menu_id, section_id y dish_id son obligatorios")
	}
	return s.botMediaMultipartReplay(ctx, rid, fmt.Sprintf("/group-menus-v2/%d/sections/%d/dishes/%d/image", menuID, sectionID, dishID), p, payload)
}

func botMediaUploadMenuPreview(s *Server, ctx context.Context, rid int, p map[string]any, payload []byte) (any, error) {
	menuID := botPanelInt64(p, "menu_id")
	if menuID <= 0 {
		return nil, errors.New("menu_id es obligatorio")
	}
	return s.botMediaMultipartReplay(ctx, rid, fmt.Sprintf("/group-menus-v2/%d/preview-image", menuID), p, payload)
}

func botMediaUploadSlider(s *Server, ctx context.Context, rid int, p map[string]any, payload []byte) (any, error) {
	menuID := botPanelInt64(p, "menu_id")
	if menuID <= 0 {
		return nil, errors.New("menu_id es obligatorio")
	}
	return s.botMediaMultipartReplay(ctx, rid, fmt.Sprintf("/group-menus-v2/%d/slider/images", menuID), p, payload)
}

// botMediaMultipartReplay posts the image as the panel's "image" form field to
// one /admin route, authenticated as the bot service session of the tenant.
func (s *Server) botMediaMultipartReplay(ctx context.Context, rid int, path string, p map[string]any, payload []byte) (any, error) {
	if _, err := botMediaImagePayload(payload); err != nil {
		return nil, err
	}
	token, err := s.botServiceSession(ctx, rid)
	if err != nil || token == "" {
		return nil, errors.New("sesión del panel no disponible")
	}
	filename := botPanelString(p, "filename")
	if filename == "" {
		filename = "imagen"
	}
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("image", filename)
	if err == nil {
		_, err = part.Write(payload)
	}
	if err == nil {
		err = form.Close()
	}
	if err != nil {
		return nil, errors.New("no se pudo preparar la imagen")
	}
	callCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	req := httptest.NewRequest(http.MethodPost, "/admin"+path, &body).WithContext(callCtx)
	req.Header.Set("Content-Type", form.FormDataContentType())
	req.Header.Set("Accept", "application/json")
	req.AddCookie(&http.Cookie{Name: boSessionCookieName, Value: token})
	if v := s.cfg.VaultKey; v != "" {
		req.Header.Set(vaultKeyHeader, v)
	}
	if v := s.cfg.BearerTokenKey; v != "" {
		req.Header.Set(bearerTokenHeader, v)
	}
	req.Header.Set("X-Forky-Coord", forkyAdminCoord)
	rec := httptest.NewRecorder()
	started := time.Now()
	s.forkyAdminRoutes().ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	ok := rec.Code < 400 && out != nil && out["success"] != false
	log.Printf("wa_bot_menus_full_crud_v1 media_upload restaurant=%d path=%s status=%d ok=%t bytes=%d ms=%d", rid, path, rec.Code, ok, len(payload), time.Since(started).Milliseconds())
	if !ok {
		msg, _ := out["message"].(string)
		if msg == "" {
			msg = "status " + strconv.Itoa(rec.Code)
		}
		return nil, errors.New("el panel no guardó la imagen: " + msg)
	}
	return out, nil
}
