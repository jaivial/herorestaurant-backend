package api

import (
	"encoding/json"
	"errors"
	"log"
	"os"
	"strings"
)

// Coordination id: wa_outbound_allowlist_v1
//
// Transport-level hard stop for local/QA runs on copied data. It sits on the
// two HTTP chokepoints every WhatsApp provider call goes through
// (sendUazAPI for UAZAPI sends/webhook, uazapiJSONRequest for Evolution and
// provisioning), so no caller - bot, outbox, campaigns, reminders,
// management group, contact cards - can bypass it.
//
//	WA_OUTBOUND_BLOCK_ALL=1            every provider request fails
//	WA_OUTBOUND_ALLOWLIST=34600000000  only message sends whose "number" is
//	                                   exactly one of these digits pass;
//	                                   groups, @lid, status/provision/webhook
//	                                   calls and anything unparsable fail.
//
// Both unset: production behaviour unchanged. Nothing is logged but the
// decision, the endpoint kind and the last 3 recipient digits.

var errWAOutboundBlocked = errors.New("wa outbound blocked by local allowlist (wa_outbound_allowlist_v1)")

func waOutboundMode() (blockAll bool, allow map[string]bool) {
	blockAll = strings.TrimSpace(os.Getenv("WA_OUTBOUND_BLOCK_ALL")) == "1"
	raw := strings.TrimSpace(os.Getenv("WA_OUTBOUND_ALLOWLIST"))
	if raw == "" {
		return blockAll, nil
	}
	allow = map[string]bool{}
	for _, p := range strings.Split(raw, ",") {
		if d := digitsOnly(p); d != "" {
			allow[d] = true
		}
	}
	return blockAll, allow
}

// waOutboundCheck returns errWAOutboundBlocked when the local guard refuses
// the request. endpoint is only inspected for its path.
func waOutboundCheck(endpoint string, payload any) error {
	blockAll, allow := waOutboundMode()
	if !blockAll && allow == nil {
		return nil
	}
	path := endpoint
	if i := strings.Index(path, "?"); i >= 0 {
		path = path[:i] // never keep ?token=
	}
	if j := strings.Index(path, "://"); j >= 0 {
		if k := strings.Index(path[j+3:], "/"); k >= 0 {
			path = path[j+3+k:]
		}
	}
	isSend := strings.Contains(path, "/send/") || strings.Contains(path, "/message/send")
	to := ""
	if raw, err := json.Marshal(payload); err == nil {
		var m map[string]any
		if json.Unmarshal(raw, &m) == nil {
			to, _ = m["number"].(string)
		}
	}
	ok := !blockAll && isSend && to != "" && !strings.Contains(to, "@g.us") && !strings.Contains(to, "@lid") && allow[digitsOnly(to)]
	tail := digitsOnly(to)
	if len(tail) > 3 {
		tail = tail[len(tail)-3:]
	}
	log.Printf("[wa_outbound_allowlist_v1] decision=%s path=%s send=%t to_tail=%s", map[bool]string{true: "allow", false: "block"}[ok], path, isSend, tail)
	if !ok {
		return errWAOutboundBlocked
	}
	return nil
}
