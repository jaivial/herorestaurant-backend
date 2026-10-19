package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Coordination id: wa_bot_group_membership_v1
//
// The membership check needs the group's participant list from the provider.
// The call lives in its own function so it can be exercised against a live
// provider in a test (the apikey header is easy to forget and the failure
// mode is a silent 401).

// botProviderFetchAllGroups asks the provider for the groups of an instance,
// with their participants. It returns the raw body, or "" when the provider
// does not answer with a success status.
//
// The apikey header is mandatory: the provider answers 401 without it, and
// uazapiJSONRequest does not add it on its own.
func botProviderFetchAllGroups(ctx context.Context, baseURL, apiKey, instanceName string) []byte {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	instanceName = strings.TrimSpace(instanceName)
	if baseURL == "" || instanceName == "" {
		return nil
	}
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	endpoint := baseURL + "/group/fetchAllGroups/" + instanceName + "?getParticipants=true"
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil
	}
	if k := strings.TrimSpace(apiKey); k != "" {
		req.Header.Set("apikey", k)
	}
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil
	}
	buf := make([]byte, 0, 64*1024)
	tmp := make([]byte, 32*1024)
	for {
		n, rerr := resp.Body.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if rerr != nil {
			break
		}
		if len(buf) > 4*1024*1024 {
			break
		}
	}
	return buf
}

// errBotProviderUnavailable is returned when the provider cannot be reached
// or the gateway does not speak its API.
var errBotProviderUnavailable = errors.New("el proveedor de WhatsApp no esta disponible")

// botProviderFindChats asks the provider for the conversations of the
// instance, with their display names.
//
// The apikey header is mandatory: the provider answers 401 without it.
func botProviderFindChats(ctx context.Context, baseURL, apiKey, instanceName string) ([]byte, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	instanceName = strings.TrimSpace(instanceName)
	if baseURL == "" || instanceName == "" {
		return nil, errBotProviderUnavailable
	}
	reqCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	endpoint := baseURL + "/chat/findChats/" + instanceName
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, strings.NewReader("{}"))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if k := strings.TrimSpace(apiKey); k != "" {
		req.Header.Set("apikey", k)
	}
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("el proveedor respondio %d", resp.StatusCode)
	}
	buf, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if err != nil {
		return nil, err
	}
	return buf, nil
}
