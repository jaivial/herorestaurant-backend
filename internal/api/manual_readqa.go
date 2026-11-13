package api

// Coordination id: manual_readqa_v1 (repair-semantic1). MANUAL read-only
// diagnostic entry used only by cmd/pad-gate-check -read-qa. It calls REAL
// ported read functions (actual handler SQL + Go Scan paths) against an
// already read-only PG pool. It is not a test, fixture or suite and asserts
// nothing: it reports aggregate counts/types only (no row values, no PII).
// Scope: read query + Go Scan; NOT full HTTP handlers, auth, concurrency or
// writes. It never starts the server and never touches readiness globals
// beyond selecting the SQL dialect for this separate process.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"preactvillacarmen/internal/sqldb"
)

// ReadQAResult is one manual observation (aggregate only).
type ReadQAResult struct {
	Function string `json:"function"`
	Source   string `json:"source"`
	OK       bool   `json:"ok"`
	Rows     int    `json:"rows"`
	Detail   string `json:"detail,omitempty"`
	Error    string `json:"error,omitempty"`
}

var errReadQAWrite = errors.New("refused: non-SELECT statement in read-only manual QA")

// readOnlyQuerier forwards only SELECT/WITH statements; any write keyword,
// locking clause or Exec is refused before reaching the driver.
type readOnlyQuerier struct{ tx *sql.Tx }

func readQAAllowed(q string) bool {
	u := strings.ToUpper(strings.TrimSpace(q))
	if !strings.HasPrefix(u, "SELECT") && !strings.HasPrefix(u, "WITH") {
		return false
	}
	for _, k := range []string{"INSERT ", "UPDATE ", "DELETE ", "MERGE ", " FOR UPDATE", " FOR SHARE", " FOR NO KEY", "PG_ADVISORY", "SET_CONFIG", "NEXTVAL"} {
		if strings.Contains(u, k) {
			return false
		}
	}
	return true
}

func (r readOnlyQuerier) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	if !readQAAllowed(q) {
		return nil, errReadQAWrite
	}
	return r.tx.QueryContext(ctx, q, args...)
}

func (r readOnlyQuerier) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	if !readQAAllowed(q) {
		panic(errReadQAWrite) // *sql.Row cannot carry an error; recovered per check
	}
	return r.tx.QueryRowContext(ctx, q, args...)
}

func (r readOnlyQuerier) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	return nil, errReadQAWrite
}

// RunManualReadQA runs the selected real read functions for one operator
// chosen restaurant/date. db must be a PG pool opened read-only (verified by
// the caller); every guarded call also runs in a READ ONLY transaction.
func RunManualReadQA(ctx context.Context, db *sql.DB, rid int, date string, domain string) []ReadQAResult {
	SetSQLDialect(sqldb.Postgres)
	var out []ReadQAResult
	run := func(fn, src string, f func() (int, string, error)) {
		r := ReadQAResult{Function: fn, Source: src}
		func() {
			defer func() {
				if p := recover(); p != nil {
					r.Error = fmt.Sprint(p)
				}
			}()
			n, d, err := f()
			r.Rows, r.Detail = n, d
			if err != nil {
				r.Error = err.Error()
			}
		}()
		r.OK = r.Error == ""
		out = append(out, r)
	}
	day, derr := time.Parse("2006-01-02", date)
	if derr != nil {
		return []ReadQAResult{{Function: "args", Error: "date must be YYYY-MM-DD"}}
	}
	// *sql.DB-typed helper: runs before the tx (single-conn pool, session read-only).
	run("affluenceDailyRows", "backoffice_affluence.go:195", func() (int, string, error) {
		rows, err := affluenceDailyRows(ctx, db, rid, day.AddDate(0, 0, -30), day)
		b, c := 0, 0
		for _, x := range rows {
			b, c = b+x.Bookings, c+x.Covers
		}
		return len(rows), fmt.Sprintf("bookings=%d covers=%d", b, c), err
	})
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return append(out, ReadQAResult{Function: "begin_read_only", Error: err.Error()})
	}
	defer tx.Rollback()
	q := readOnlyQuerier{tx: tx}
	s := &Server{}
	run("botDayCapacityOn", "whatsapp_bot_executor.go:492", func() (int, string, error) {
		limit, total, err := s.botDayCapacityOn(ctx, q, rid, date)
		return 1, fmt.Sprintf("limit=%d total=%d", limit, total), err
	})
	run("botLoadDateFloorsFrom", "whatsapp_bot_floor.go:65", func() (int, string, error) {
		f, err := s.botLoadDateFloorsFrom(ctx, q, rid, date)
		a := 0
		for _, x := range f {
			if x.Active {
				a++
			}
		}
		return len(f), fmt.Sprintf("active=%d", a), err
	})
	run("botLoadBookingExtras", "whatsapp_bot_extras_admin.go:85", func() (int, string, error) {
		e, err := botLoadBookingExtras(ctx, q, rid)
		return len(e), "", err
	})
	run("loadPOSModifierGroupsFrom", "backoffice_pos_modifiers.go:78", func() (int, string, error) {
		g, err := loadPOSModifierGroupsFrom(ctx, q, rid, 0)
		kinds := map[string]int{}
		for _, x := range g {
			kinds[x.Kind]++
		}
		return len(g), fmt.Sprintf("kinds=%v", kinds), err
	})
	run("loadStockBOM", "backoffice_stock_recipes.go:105", func() (int, string, error) {
		m, err := loadStockBOM(ctx, tx, rid) // *sql.Tx-typed: tx is READ ONLY (PG rejects writes)
		c := 0
		for _, x := range m {
			c += len(x.Components)
		}
		return len(m), fmt.Sprintf("components=%d", c), err
	})
	if domain != "" {
		run("restaurantDomainOwner", "domain_ownership.go:32", func() (int, string, error) {
			owner, found, err := restaurantDomainOwner(ctx, q, domain, false)
			return 1, fmt.Sprintf("found=%t owner_is_rid=%t", found, found && owner == rid), err
		})
	}
	return out
}
