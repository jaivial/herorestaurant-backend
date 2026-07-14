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
