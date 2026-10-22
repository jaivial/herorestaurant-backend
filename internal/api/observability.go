package api

import (
	"context"
	"log"
	"net/http"
	"strings"

	"preactvillacarmen/internal/diag"
)

// Coordination id: wa_diag_sink_v1 — the process-wide diag sink: every diag
// event is rendered as one greppable line on stdout (docker logs in prod) in
// addition to the in-memory ring buffer. Installed once at package init; the
// diag package itself never logs.
func init() {
	diag.SetSink(func(e diag.Event) { log.Printf("%s", e.Line()) })
}

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
// The event goes through the diag package (wa_diag_pkg_v1): the installed
// sink renders exactly the historical line, so there is one emission point.
func logCheckpoint(r *http.Request, name string, kv ...string) {
	var b strings.Builder
	for i := 0; i+1 < len(kv); i += 2 {
		b.WriteString(" ")
		b.WriteString(sanitizeLogValue(kv[i]))
		b.WriteString("=")
		b.WriteString(sanitizeLogValue(kv[i+1]))
	}
	diag.Record(diag.Event{
		CoordinationID: correlationIDFromRequest(r),
		Checkpoint:     name,
		Message:        strings.TrimPrefix(b.String(), " "),
	})
}

// botCheckpoint is the single observation path of the WhatsApp bot (and the
// mechanical replacement of the old log.Printf("[bot] checkpoint ...") call
// sites): the emitted line keeps the exact same greppable format operators
// and scripts depend on — "[bot] checkpoint <name> key=value..." — while the
// event also lands in the diag ring buffer (wa_diag_pkg_v1) so a whole turn
// can be reconstructed afterwards.
func botCheckpoint(name, format string, args ...any) {
	diag.Recordf("bot", "", name, format, args...)
}

// botCheckpointCtx is botCheckpoint with the turn correlation id taken from
// the context (see botProcessMessage, which carries the bot session id).
func botCheckpointCtx(ctx context.Context, name, format string, args ...any) {
	diag.Recordf("bot", diag.CoordinationFrom(ctx), name, format, args...)
}
