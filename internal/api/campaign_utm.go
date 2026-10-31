package api

import (
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"
)

// campaignLinkPattern finds absolute links in markdown, plain text and HTML.
var campaignLinkPattern = regexp.MustCompile(`https?://[^\s"'<>()\[\]]+`)

// campaignUTMSkipPaths are own-site links that must stay untouched (opt-out).
var campaignUTMSkipPaths = []string{"/baja-publicidad"}

// campaignUTMCampaign is the utm_campaign value: slug of the campaign name,
// falling back to its id, so analytics can group visits per campaign.
func campaignUTMCampaign(c boCampaign) string {
	if slug := strings.Trim(slugifyCategoryName(c.Name), "-"); slug != "" {
		return slug
	}
	return fmt.Sprintf("campana-%d", c.ID)
}

// campaignTagLinks adds utm_source/utm_medium/utm_campaign to every link that
// points to the restaurant own site (siteURL host, with or without www).
// Links to other hosts, static files, the opt-out page and links that already
// carry utm_source are left as they are. Works on markdown, text and HTML
// (keeps "&amp;" separators inside HTML attributes).
func campaignTagLinks(content, siteURL, source, campaign string) string {
	site, err := url.Parse(strings.TrimSpace(siteURL))
	if err != nil || site.Host == "" || strings.TrimSpace(content) == "" {
		return content
	}
	siteHost := strings.TrimPrefix(strings.ToLower(site.Hostname()), "www.")
	params := "utm_source=" + url.QueryEscape(source) + "&utm_medium=campaign&utm_campaign=" + url.QueryEscape(campaign)
	return campaignLinkPattern.ReplaceAllStringFunc(content, func(raw string) string {
		link := strings.TrimRight(raw, ".,;:!?*_")
		tail := raw[len(link):]
		u, err := url.Parse(strings.ReplaceAll(link, "&amp;", "&"))
		if err != nil || strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.") != siteHost || u.Query().Has("utm_source") {
			return raw
		}
		for _, p := range campaignUTMSkipPaths {
			if strings.HasPrefix(u.Path, p) {
				return raw
			}
		}
		if ext := strings.ToLower(path.Ext(u.Path)); ext != "" && ext != ".html" && ext != ".php" {
			return raw
		}
		base, fragment, hasFragment := strings.Cut(link, "#")
		sep := "?"
		if strings.Contains(base, "?") {
			sep = "&"
			if strings.Contains(base, "&amp;") {
				sep = "&amp;"
			}
		}
		tagged := base + sep + params
		if sep == "&amp;" {
			tagged = base + sep + strings.ReplaceAll(params, "&", "&amp;")
		}
		if hasFragment {
			tagged += "#" + fragment
		}
		return tagged + tail
	})
}
