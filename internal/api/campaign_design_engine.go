package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// Coordination id: campaign_tools_v1
//
// Client of the campaign design engine that lives in the backoffice server
// (backofficereact server/campaignDesign.ts + server/campaignEmailCompile.ts).
//
// Why there: a campaign design is a Tailwind HTML fragment and the email SMTP
// sends is compiled from it with Tailwind (Node) plus a CSS inliner; the
// palette blocks and the builder operations are TypeScript. Running them in the
// backoffice server keeps ONE implementation for the visual editor and for the
// tools of Forky and the WhatsApp bot, so a design edited from a chat is
// stored exactly as the editor would store it. The backend keeps everything
// else: tenant scope, permissions, confirmations, the AI and persistence.
//
// Auth: the same server-only secrets the backoffice sends on every admin API
// call (VAULT_KEY as Bearer, BEARER_TOKEN_KEY as X-Bearer-Token).

const (
	campaignDesignPath         = "/bo-tools/campaign-design"
	campaignDesignTimeout      = 60 * time.Second
	campaignDesignMaxResponse  = 4 << 20
	campaignDesignEngineFailed = "el motor de diseño del backoffice no está disponible; inténtalo de nuevo en un momento"
)

// campaignDesignCtx is the brand the palette blocks are built with (same shape
// as BlockContext in emailBuilderBlocks.ts).
type campaignDesignCtx struct {
	BrandName  string `json:"brandName"`
	LogoURL    string `json:"logoUrl"`
	WebsiteURL string `json:"websiteUrl"`
	Accent     string `json:"accent"`
}

// campaignDesignRequest is the body of POST /bo-tools/campaign-design.
type campaignDesignRequest struct {
	Action   string            `json:"action"`
	HTML     string            `json:"html,omitempty"`
	ID       string            `json:"id,omitempty"`
	Ops      json.RawMessage   `json:"ops,omitempty"`
	Ctx      campaignDesignCtx `json:"ctx"`
	Markdown string            `json:"markdown,omitempty"`
	Theme    *campaignTheme    `json:"theme,omitempty"`
	Compile  *struct {
		Title string `json:"title"`
	} `json:"compile,omitempty"`
	CoordID string `json:"coord_id,omitempty"`
	// HTML ads (ads_html_editor_v1): ad_compile pages; ad_default/ad_kind
	// kind; ad_sync/ad_kind design; ad_convert the legacy ad and website.
	Pages    []campaignDesignPage `json:"pages,omitempty"`
	Kind     string               `json:"kind,omitempty"`
	AdDesign *boAdDesign          `json:"design,omitempty"`
	Ad       *boAd                `json:"ad,omitempty"`
	Website  string               `json:"website,omitempty"`
}

// campaignDesignPage is one page of an HTML ad: the design in, the sanitized
// design and its compiled CSS out (ads_html_editor_v1).
type campaignDesignPage struct {
	HTML string `json:"html"`
	CSS  string `json:"css,omitempty"`
}

// campaignDesignResult is the engine answer. Layers/design/created/blocks are
// passed through to the model untouched.
type campaignDesignResult struct {
	Success          bool                 `json:"success"`
	Message          string               `json:"message"`
	HTML             string               `json:"html"`
	Layers           json.RawMessage      `json:"layers"`
	Design           json.RawMessage      `json:"design"`
	Created          json.RawMessage      `json:"created"`
	EmailHTML        string               `json:"email_html"`
	WhatsAppMarkdown string               `json:"whatsapp_markdown"`
	Source           string               `json:"source"`
	Blocks           json.RawMessage      `json:"blocks"`
	StyleGroups      json.RawMessage      `json:"style_groups"`
	Pages            []campaignDesignPage `json:"pages"`
}

// campaignDesignRefusal is an engine answer the model must read (an invalid
// op, an unknown element id): it is not an infrastructure failure.
type campaignDesignRefusal struct{ msg string }

func (e campaignDesignRefusal) Error() string { return e.msg }

// campaignDesignContext builds the block context of one campaign: brand of the
// restaurant plus the campaign accent.
func (s *Server) campaignDesignContext(ctx context.Context, restaurantID int, theme campaignTheme) campaignDesignCtx {
	brand := s.campaignTemplateBrand(ctx, restaurantID)
	return campaignDesignCtx{BrandName: brand.BrandName, LogoURL: brand.LogoURL, WebsiteURL: brand.WebsiteURL, Accent: theme.Accent}
}

// campaignDesignCall runs one action of the design engine.
func (s *Server) campaignDesignCall(ctx context.Context, req campaignDesignRequest) (campaignDesignResult, error) {
	base := strings.TrimRight(strings.TrimSpace(s.cfg.BackofficeInternalURL), "/")
	if base == "" {
		return campaignDesignResult{}, errors.New(campaignDesignEngineFailed)
	}
	body, err := json.Marshal(req)
	if err != nil {
		return campaignDesignResult{}, errors.New("no se pudo preparar el diseño")
	}
	callCtx, cancel := context.WithTimeout(ctx, campaignDesignTimeout)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(callCtx, http.MethodPost, base+campaignDesignPath, bytes.NewReader(body))
	if err != nil {
		return campaignDesignResult{}, errors.New(campaignDesignEngineFailed)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if v := strings.TrimSpace(s.cfg.VaultKey); v != "" {
		httpReq.Header.Set("Authorization", "Bearer "+v)
	}
	if v := strings.TrimSpace(s.cfg.BearerTokenKey); v != "" {
		httpReq.Header.Set(bearerTokenHeader, v)
	}
	started := time.Now()
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		slog.Default().Warn("campaign.tools.engine.unreachable", "coord_id", req.CoordID, "action", req.Action, "err", err.Error())
		return campaignDesignResult{}, errors.New(campaignDesignEngineFailed)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, campaignDesignMaxResponse))
	if err != nil {
		return campaignDesignResult{}, errors.New(campaignDesignEngineFailed)
	}
	var out campaignDesignResult
	if err := json.Unmarshal(raw, &out); err != nil {
		slog.Default().Warn("campaign.tools.engine.bad_response", "coord_id", req.CoordID, "action", req.Action, "status", resp.StatusCode)
		return campaignDesignResult{}, errors.New(campaignDesignEngineFailed)
	}
	slog.Default().Info("campaign.tools.engine", "coord_id", req.CoordID, "action", req.Action, "status", resp.StatusCode, "ok", out.Success, "ms", time.Since(started).Milliseconds())
	switch {
	case resp.StatusCode == http.StatusBadRequest && out.Message != "":
		slog.Default().Info("campaign.tools.engine.refused", "coord_id", req.CoordID, "action", req.Action, "message", out.Message)
		return out, campaignDesignRefusal{out.Message}
	case resp.StatusCode >= 300 || !out.Success:
		slog.Default().Warn("campaign.tools.engine.failed", "coord_id", req.CoordID, "action", req.Action, "status", resp.StatusCode, "message", out.Message)
		return out, fmt.Errorf("%s (HTTP %d)", campaignDesignEngineFailed, resp.StatusCode)
	}
	return out, nil
}
