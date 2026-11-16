package api

import (
	"context"
	"log"
	"net/http"
	"sync"
	"time"

	"preactvillacarmen/internal/httpx"
)

// Legacy DIA/FINDE (menú del día / fin de semana antiguos) were retired by the
// v2 menu migration and are absent from production (MySQL and PG). Handlers
// that still reach them check legacyMenuTableExists first so they answer
// "empty" (reads) or 410 (writes) instead of firing a failing query.
// Coordination id: LEGACY-DIA-FINDE-GUARD.

const legacyMenuTableTTL = 10 * time.Minute

const legacyMenuGoneMessage = "El menú legado DIA/FINDE no existe; usa los menús de grupo"

type legacyMenuTableEntry struct {
	exists bool
	at     time.Time
}

var legacyMenuTableCache sync.Map // table -> legacyMenuTableEntry

// legacyMenuTableExists reports whether the exact table (DIA/FINDE) exists in
// the current schema. Cached for legacyMenuTableTTL; on lookup error it returns
// true (uncached) so the previous behaviour is kept.
func (s *Server) legacyMenuTableExists(ctx context.Context, table string) bool {
	if v, ok := legacyMenuTableCache.Load(table); ok {
		if e := v.(legacyMenuTableEntry); time.Since(e.at) < legacyMenuTableTTL {
			return e.exists
		}
	}
	var n int
	err := s.db.QueryRowContext(ctx, nativeSQL(
		"SELECT COUNT(*) FROM information_schema.tables WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?",
		"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = ?"),
		table).Scan(&n)
	if err != nil {
		log.Printf("legacy.menu.table.check coord=LEGACY-DIA-FINDE-GUARD table=%s err=%v", table, err)
		return true
	}
	legacyMenuTableCache.Store(table, legacyMenuTableEntry{exists: n > 0, at: time.Now()})
	return n > 0
}

// writeLegacyMenuGone answers a write against a missing DIA/FINDE table.
func writeLegacyMenuGone(w http.ResponseWriter) {
	httpx.WriteError(w, http.StatusGone, legacyMenuGoneMessage)
}
