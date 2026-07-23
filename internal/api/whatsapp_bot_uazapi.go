package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// botUazapiSend posts a JSON payload to a UAZAPI /send/{kind} endpoint of the
// tenant's provisioned instance.
func botUazapiSend(ctx context.Context, baseURL string, token string, kind string, payload map[string]any) error {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return fmt.Errorf("uazapi no configurado")
	}
	endpoint := base + "/send/" + kind
	if token != "" {
		endpoint += "?token=" + url.QueryEscape(token)
	}
	body, code, err := sendUazAPI(ctx, endpoint, payload)
	if err != nil {
		return err
	}
	if code != http.StatusOK && code != http.StatusCreated {
		return fmt.Errorf("uazapi %s http %d: %s", kind, code, truncate(body, 200))
	}
	return nil
}

// botUazapiConfigureWebhook registers the tenant instance webhook so inbound
// WhatsApp events are delivered to our multi-tenant /bot/webhook endpoint.
// UAZAPI exposes POST /webhook (instance token in query).
// We send the union of field spellings seen across UAZAPI versions so the call
// is resilient; the provider ignores unknown keys.
func botUazapiConfigureWebhook(ctx context.Context, baseURL string, instanceToken string, callbackURL string, events []string) error {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	callbackURL = strings.TrimSpace(callbackURL)
	if base == "" || callbackURL == "" {
		return fmt.Errorf("uazapi webhook: base o callback vacios")
	}
	if len(events) == 0 {
		events = []string{"messages", "connection"}
	}
	endpoint := base + "/webhook"
	if instanceToken != "" {
		endpoint += "?token=" + url.QueryEscape(instanceToken)
	}
	payload := map[string]any{
		"enabled":             true,
		"url":                 callbackURL,
		"webhook":             callbackURL,
		"events":              events,
		"excludeMessages":     []string{"wasSentByApi", "fromMe"},
		"addUrlEvents":        false,
		"addUrlTypesMessages": false,
	}
	body, code, err := sendUazAPI(ctx, endpoint, payload)
	if err != nil {
		return err
	}
	if code != http.StatusOK && code != http.StatusCreated {
		return fmt.Errorf("uazapi webhook http %d: %s", code, truncate(body, 200))
	}
	return nil
}
