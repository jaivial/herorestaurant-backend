package api

import (
	"log"
	"net/http"
	"strings"
)

// correlationIDFromRequest extracts the inbound x-correlation-id header, if any.
func correlationIDFromRequest(r *http.Request) string {
	return strings.TrimSpace(r.Header.Get("x-correlation-id"))
}

// echoCorrelationID sets the correlation id on the response so callers can
// verify end-to-end propagation.
func echoCorrelationID(w http.ResponseWriter, r *http.Request) {
	if cid := correlationIDFromRequest(r); cid != "" {
		w.Header().Set("x-correlation-id", cid)
	}
}

// logCheckpoint emits a named, greppable checkpoint tied to the request's
// correlation id. Format: checkpoint <name> correlation_id=<id> key=value...
func logCheckpoint(r *http.Request, name string, kv ...string) {
	var b strings.Builder
	b.WriteString("checkpoint ")
	b.WriteString(name)
	if cid := correlationIDFromRequest(r); cid != "" {
		b.WriteString(" correlation_id=")
		b.WriteString(cid)
	}
	for i := 0; i+1 < len(kv); i += 2 {
		b.WriteString(" ")
		b.WriteString(kv[i])
		b.WriteString("=")
		b.WriteString(kv[i+1])
	}
	log.Printf("%s", b.String())
}
