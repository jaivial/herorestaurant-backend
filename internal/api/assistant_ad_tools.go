package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Coordination id: ads_html_editor_v1 (Forky half)
//
// Forky tools of /app/anuncios (registered in assistant_registry.go, section
// "anuncios"): thin adapters over the ad core (ad_tools.go). Reads answer at
// once; writes go through the Forky confirmation flow of the editor tools
// (assistant_tool_flow.go), publishing and deleting waiting for the user's
// own yes (adHumanConfirmActions).

var adAssistantToolTimeouts = map[string]time.Duration{
	"ad_get":    90 * time.Second,
	"ad_save":   90 * time.Second,
	"ad_design": campaignAITimeout + time.Minute,
	"ad_image":  90 * time.Second,
	"ad_delete": 30 * time.Second,
}

// adArgsFingerprint is what a person confirms in a publish/delete preview.
func adArgsFingerprint(a adToolArgs) string {
	normalizeAdToolArgs(&a)
	active := ""
	if a.Active != nil {
		active = fmt.Sprint(*a.Active)
	}
	return fmt.Sprintf("%s|%d|%s", a.Action, a.AdID, active)
}

var adWriteFlow = assistantWriteFlow[adToolArgs]{
	IDField:     "ad_id",
	Of:          "del anuncio",
	Human:       adHumanConfirmActions,
	Action:      func(a adToolArgs) string { return a.Action },
	Target:      func(a adToolArgs) int64 { return a.AdID },
	Fingerprint: adArgsFingerprint,
	Preview:     (*Server).adToolPreview,
	Execute:     (*Server).assistantAdExecute,
	Log:         "ad.tools",
}

func (s *Server) assistantAdExecute(ctx context.Context, rid int, tool string, a adToolArgs) (string, error) {
	out, summary, err := s.adToolRun(ctx, rid, a, "forky")
	if err != nil {
		return "", err
	}
	out["tool"] = tool
	out["summary"] = summary
	return botJSON(out), nil
}

// assistantAdGet is ad_get: the list, or one ad with its pages.
func (s *Server) assistantAdGet(ctx context.Context, rid int, input json.RawMessage) (string, error) {
	var a adToolArgs
	if err := json.Unmarshal(input, &a); err != nil {
		return "", errors.New("argumentos ilegibles")
	}
	a.Action = "get"
	out, _, err := s.adToolRun(ctx, rid, a, "forky")
	if err != nil {
		return "", err
	}
	out["tool"] = "ad_get"
	return botJSON(out), nil
}

func (s *Server) assistantAdWrite(ctx context.Context, rid int, tool string, input json.RawMessage, mapAction func(a *adToolArgs) error) (string, error) {
	var a adToolArgs
	if err := json.Unmarshal(input, &a); err != nil {
		return "", errors.New("argumentos ilegibles")
	}
	normalizeAdToolArgs(&a)
	if err := mapAction(&a); err != nil {
		return "", err
	}
	return assistantFlowWrite(s, ctx, rid, tool, input, a, adWriteFlow)
}

func (s *Server) assistantAdSave(ctx context.Context, rid int, input json.RawMessage) (string, error) {
	return s.assistantAdWrite(ctx, rid, "ad_save", input, func(a *adToolArgs) error {
		switch a.Action {
		case "create", "update", "convert", "publish":
			return nil
		}
		return errors.New("action debe ser create, update, convert o publish")
	})
}

func (s *Server) assistantAdDesign(ctx context.Context, rid int, input json.RawMessage) (string, error) {
	return s.assistantAdWrite(ctx, rid, "ad_design", input, func(a *adToolArgs) error {
		switch a.Action {
		case "ai":
			a.Action = "design_ai"
		case "undo":
			a.Action = "design_undo"
		default:
			return errors.New("action debe ser ai o undo")
		}
		return nil
	})
}

// assistantAdImage is ad_image: an image attached in this chat, uploaded to
// the restaurant's CDN and placed in a page of the ad.
func (s *Server) assistantAdImage(ctx context.Context, rid int, input json.RawMessage) (string, error) {
	return s.assistantAdWrite(ctx, rid, "ad_image", input, func(a *adToolArgs) error {
		a.Action = "image"
		// The CDN URL is server-side: a Forky image always comes from an
		// attachment of the restaurant's own chat.
		a.ImageURL, a.ImageName, a.ImageRef = "", "", ""
		if strings.TrimSpace(a.Attachment) == "" {
			return errors.New("falta attachment: la ref de la imagen adjunta (aparece como «ref …» en la línea [Adjunto: …])")
		}
		return nil
	})
}

func (s *Server) assistantAdDelete(ctx context.Context, rid int, input json.RawMessage) (string, error) {
	return s.assistantAdWrite(ctx, rid, "ad_delete", input, func(a *adToolArgs) error {
		a.Action = "delete"
		return nil
	})
}
