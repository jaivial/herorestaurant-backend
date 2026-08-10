package api

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-sql-driver/mysql"

	"preactvillacarmen/internal/config"
)

// --- counting connector: count every query executed through *sql.DB ---
// sql.DB is a concrete type, so counting is done at the driver layer by
// wrapping mysql.NewConnector and counting calls on each connection.

type countingConn struct {
	driver.Conn
	n *int
}

func (c *countingConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	*c.n++
	return c.Conn.(driver.ConnPrepareContext).PrepareContext(ctx, query)
}

func (c *countingConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	*c.n++
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, query, args)
}

func (c *countingConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	*c.n++
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
}

func (c *countingConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	*c.n++
	return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, opts)
}

type countingConnector struct {
	driver.Connector
	n *int
}

func (c *countingConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &countingConn{Conn: conn, n: c.n}, nil
}

// openCountingDB opens a real mysql pool whose every query is counted.
func openCountingDB(t *testing.T) (*sql.DB, *int) {
	t.Helper()
	dsn := getTestDBDSN(t)
	n := 0
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	connector, err := mysql.NewConnector(cfg)
	if err != nil {
		t.Fatalf("new connector: %v", err)
	}
	db := sql.OpenDB(&countingConnector{Connector: connector, n: &n})
	t.Cleanup(func() { _ = db.Close() })
	return db, &n
}

func getTestDBDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("TEST_DB_DSN")
	if dsn == "" {
		t.Skip("TEST_DB_DSN not set, skipping integration test")
	}
	return dsn
}

// --- auth middleware tests ---

// authStubHandler serves 200 without touching the DB, so any DB query below it
// is attributable to requireBOSession (session+roles).
func authStubHandler(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}

// sessionAuthMux builds a mux whose /protected is gated by requireBOSession and
// whose /logout uses the real handler (for cache-invalidation coverage).
func sessionAuthMux(s *Server) *chi.Mux {
	if s.sessionCache == nil {
		s.sessionCache = newBOSessionCache(30 * time.Second)
	}
	r := chi.NewRouter()
	r.Use(s.requireBOSession)
	r.Get("/protected", authStubHandler)
	r.Post("/logout", s.handleBOLogout)
	return r
}

// seedAuthRow creates a user + session usable by requireBOSession.
func seedAuthRow(t *testing.T, db *sql.DB, token, email string) {
	t.Helper()
	hash := sha256Hex(token)
	if _, err := db.ExecContext(context.Background(),
		"INSERT INTO bo_users (email, name, password_hash) VALUES (?, 'Test User', 'x') ON DUPLICATE KEY UPDATE id=id",
		email); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	var userID int
	if err := db.QueryRowContext(context.Background(),
		"SELECT id FROM bo_users WHERE email = ?", email).Scan(&userID); err != nil {
		t.Fatalf("user id: %v", err)
	}
	var rid int
	if err := db.QueryRowContext(context.Background(),
		"SELECT id FROM restaurants LIMIT 1").Scan(&rid); err != nil {
		t.Fatalf("seed restaurant: %v", err)
	}
	if _, err := db.ExecContext(context.Background(),
		"INSERT INTO bo_user_restaurants (user_id, restaurant_id, role) VALUES (?, ?, 'admin')",
		userID, rid); err != nil {
		t.Fatalf("seed user-restaurant: %v", err)
	}
	if _, err := db.ExecContext(context.Background(),
		"INSERT INTO bo_sessions (token_sha256, user_id, active_restaurant_id, expires_at) VALUES (?, ?, ?, ?)",
		hash, userID, rid, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), "DELETE FROM bo_sessions WHERE token_sha256 = ?", hash)
		_, _ = db.ExecContext(context.Background(), "DELETE FROM bo_user_restaurants WHERE user_id = ?", userID)
		_, _ = db.ExecContext(context.Background(), "DELETE FROM bo_users WHERE email = ?", email)
	})
}

func authedRequest(mux *chi.Mux, token, method string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/protected", nil)
	req.AddCookie(&http.Cookie{Name: boSessionCookieName, Value: token})
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr
}

// requireBOSession must not hit the DB on the second request with the same
// (valid) session token: the session+roles lookup is cached after the first hit.
func TestRequireBOSessionCache(t *testing.T) {
	db, queries := openCountingDB(t)
	srv := &Server{db: db, cfg: testConfig()}
	mux := sessionAuthMux(srv)

	token := "sess-cache-test-token"
	seedAuthRow(t, db, token, "cachetest@example.com")

	*queries = 0
	if rr := authedRequest(mux, token, http.MethodGet); rr.Code != http.StatusOK {
		t.Fatalf("req1 status %d (queries=%d)", rr.Code, *queries)
	}
	if *queries == 0 {
		t.Fatal("expected at least 1 DB query on first auth")
	}

	*queries = 0
	if rr := authedRequest(mux, token, http.MethodGet); rr.Code != http.StatusOK {
		t.Fatalf("req2 status %d", rr.Code)
	}
	if *queries != 0 {
		t.Fatalf("second auth should be served from cache (0 DB queries), got %d", *queries)
	}
}

// Logging out must invalidate the cached auth so a follow-up request is 401.
func TestRequireBOSessionCacheInvalidatedOnLogout(t *testing.T) {
	db, queries := openCountingDB(t)
	srv := &Server{db: db, cfg: testConfig()}
	mux := sessionAuthMux(srv)

	token := "sess-logout-invalidate-token"
	seedAuthRow(t, db, token, "logout-invalidate@example.com")

	if rr := authedRequest(mux, token, http.MethodGet); rr.Code != http.StatusOK {
		t.Fatalf("pre-logout auth status %d", rr.Code)
	}

	// Real logout handler: deletes the row AND must drop the cache entry.
	logoutReq := httptest.NewRequest(http.MethodPost, "/logout", nil)
	logoutReq.AddCookie(&http.Cookie{Name: boSessionCookieName, Value: token})
	logoutRR := httptest.NewRecorder()
	mux.ServeHTTP(logoutRR, logoutReq)
	if logoutRR.Code != http.StatusOK {
		t.Fatalf("logout status %d", logoutRR.Code)
	}

	*queries = 0
	if rr := authedRequest(mux, token, http.MethodGet); rr.Code != http.StatusUnauthorized {
		t.Fatalf("after logout expected 401, got %d", rr.Code)
	}
}

func testConfig() config.Config { return config.Config{} }
