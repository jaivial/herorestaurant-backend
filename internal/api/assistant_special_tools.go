package api

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

// Coordination id: forky_bot_special_full_v1
//
// The image half of special menus and special dates for Forky. Every JSON
// operation of both editors is reachable through admin_call (with its body
// contract in admin_describe), but the image uploads are multipart and
// admin_call replays JSON only. This tool feeds a chat attachment to the same
// media ops the WhatsApp bot uses (botMediaUploadOps), so both assistants
// store images through the very same domain functions as the panel.

// assistantSpecialMediaKinds are the media_upload kinds of the special editors.
var assistantSpecialMediaKinds = map[string]bool{"special_menu": true, "special_section": true, "special_date_menu": true}

func init() {
	assistantToolRegistry = append(assistantToolRegistry, assistantTool{
		Name: "special_media_upload", Description: "Sube una IMAGEN ADJUNTA en este chat a un menú especial o a una fecha especial (festiva): " +
			"kind=special_menu (imagen principal del menú especial; menu_id), special_section (imagen de una sección del menú especial; menu_id + section_id), " +
			"special_date_menu (imagen de un menú personalizado de una fecha especial: devuelve la url, que se guarda como custom_image_url con admin_patch_config_special_dates). " +
			"attachment = la ref de la línea [Adjunto: … · ref …]. Primero confirmed=false (vista previa) y, cuando el usuario diga que sí, confirmed=true con los mismos datos.",
		Write: true, Confirm: true, BackofficeOnly: true, Section: "menus",
		Schema: json.RawMessage(`{"type":"object","properties":{"kind":{"type":"string","enum":["special_menu","special_section","special_date_menu"]},"menu_id":{"type":"integer"},"section_id":{"type":"integer"},"attachment":{"type":"string","description":"ref del adjunto (forky/…)"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["kind","attachment","confirmed"]}`),
		Handler: func(s *Server, ctx context.Context, rid int, input json.RawMessage) (string, error) {
			return s.assistantSpecialMediaUpload(ctx, rid, input)
		},
	})
}

func (s *Server) assistantSpecialMediaUpload(ctx context.Context, rid int, input json.RawMessage) (string, error) {
	var in struct {
		Kind              string `json:"kind"`
		MenuID            int64  `json:"menu_id"`
		SectionID         int64  `json:"section_id"`
		Attachment        string `json:"attachment"`
		Confirmed         bool   `json:"confirmed"`
		ConfirmationToken string `json:"confirmation_token"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return "", errors.New("argumentos inválidos")
	}
	if !assistantSpecialMediaKinds[in.Kind] {
		return "", errors.New("kind debe ser special_menu, special_section o special_date_menu")
	}
	if strings.TrimSpace(in.Attachment) == "" {
		return "", errors.New("falta attachment: la ref de la imagen adjunta (aparece como «ref …» en la línea [Adjunto: …])")
	}
	if !in.Confirmed {
		return s.assistantRequireConfirmation(rid, "special_media_upload", input)
	}
	if err := s.assistantConsumeConfirmation(in.ConfirmationToken, rid, "special_media_upload", input); err != nil {
		return "", err
	}
	// The image always comes from this chat's attachment (never inline bytes
	// or a WhatsApp chat).
	res, err := botMediaUploadOp(s, ctx, rid, map[string]any{
		"kind": in.Kind, "menu_id": float64(in.MenuID), "section_id": float64(in.SectionID), "attachment": in.Attachment,
	})
	if err != nil {
		return "", err
	}
	return botJSON(map[string]any{"ok": true, "kind": in.Kind, "result": res}), nil
}
