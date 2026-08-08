package api

import (
	"context"
	"encoding/json"
)

// --- Site builder write (reuse site_builder.go handler, confirmation on) ---

// assistantSitePublish publishes the current draft of a site of the active
// restaurant (creates a version snapshot and marks the site published).
// Requires confirmation.
func (s *Server) assistantSitePublish(ctx context.Context, rid int, input json.RawMessage) (string, error) {
	var in struct {
		SiteID string `json:"site_id"`
	}
	_ = json.Unmarshal(input, &in)
	return s.assistantConfirmedMutation(ctx, rid, "site_publish", handlePublishSite(s.db), input, assistantHandlerInput{
		URLParam: map[string]string{"siteId": in.SiteID},
	})
}
