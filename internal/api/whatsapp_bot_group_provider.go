package api

import (
	"context"
	"encoding/json"
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
	raw, _ := botProviderFetchAllGroupsWithError(ctx, baseURL, apiKey, instanceName)
	return raw
}

// botProviderFetchAllGroupsWithError is the same call, but it also reports why
// it failed.
//
// The membership check uses the version that swallows the error on purpose: a
// failure there has to deny the privilege, silently and fast. The backoffice
// listing needs the opposite: to tell the operator that the provider could
// not be read, instead of showing an empty list as if the WhatsApp account
// had no conversations at all.
func botProviderFetchAllGroupsWithError(ctx context.Context, baseURL, apiKey, instanceName string) ([]byte, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	instanceName = strings.TrimSpace(instanceName)
	if baseURL == "" || instanceName == "" {
		return nil, errBotProviderUnavailable
	}
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	endpoint := baseURL + "/group/fetchAllGroups/" + instanceName + "?getParticipants=true"
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	if k := strings.TrimSpace(apiKey); k != "" {
		req.Header.Set("apikey", k)
	}
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("el proveedor respondio %d", resp.StatusCode)
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
	return buf, nil
}

// errBotProviderUnavailable is returned when the provider cannot be reached
// or the gateway does not speak its API.
var errBotProviderUnavailable = errors.New("el proveedor de WhatsApp no esta disponible")

// botProviderInstanceOwnerPhone returns the phone number of the number linked
// to an instance, as the provider reports it.
//
// The instance is the authority on which number it is, so this is preferred
// over the copy stored in the database: the stored copy is written from a
// provider payload that can carry Baileys' device suffix
// ("34960255536:28@s.whatsapp.net"), and a number with the device glued to
// it matches nothing.
//
// It returns "" when the provider cannot be reached or does not know the
// instance.
func botProviderInstanceOwnerPhone(ctx context.Context, baseURL, apiKey, instanceName string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	instanceName = strings.TrimSpace(instanceName)
	if baseURL == "" || instanceName == "" {
		return ""
	}
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, baseURL+"/instance/fetchInstances", nil)
	if err != nil {
		return ""
	}
	if k := strings.TrimSpace(apiKey); k != "" {
		req.Header.Set("apikey", k)
	}
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ""
	}
	buf, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return ""
	}
	var instances []struct {
		InstanceName string `json:"instanceName"`
		OwnerJID     string `json:"ownerJid"`
	}
	if json.Unmarshal(buf, &instances) != nil {
		return ""
	}
	for _, in := range instances {
		if strings.TrimSpace(in.InstanceName) != instanceName {
			continue
		}
		return botWhatsappIDDigits(in.OwnerJID)
	}
	return ""
}

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
