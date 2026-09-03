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

// sanitizeLogValue strips control characters (notably newlines) so user- or
// error-derived strings cannot forge extra log lines (log injection).
func sanitizeLogValue(v string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, v)
}

// logCheckpoint emits a named, greppable checkpoint tied to the request's
// correlation id. Format: checkpoint <name> correlation_id=<id> key=value...
func logCheckpoint(r *http.Request, name string, kv ...string) {
	var b strings.Builder
	b.WriteString("checkpoint ")
	b.WriteString(sanitizeLogValue(name))
	if cid := correlationIDFromRequest(r); cid != "" {
		b.WriteString(" correlation_id=")
		b.WriteString(sanitizeLogValue(cid))
	}
	for i := 0; i+1 < len(kv); i += 2 {
		b.WriteString(" ")
		b.WriteString(sanitizeLogValue(kv[i]))
		b.WriteString("=")
		b.WriteString(sanitizeLogValue(kv[i+1]))
	}
	log.Printf("%s", b.String())
}
